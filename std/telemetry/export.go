package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// graceWindow is the extra wait past a cancelled drain for the in-flight
// Export to return before Flush or Shutdown reports unresolved work.
const graceWindow = 100 * time.Millisecond

// Pipeline is the export delivery engine: one bounded queue and one worker
// per destination, per-record outcome accounting, admission policy and
// bounded independent diagnostics. It implements types.RecordSink.
type Pipeline struct {
	cfg   exportConfig
	dests map[string]*destState

	mu       sync.Mutex
	shutdown bool
}

type exportConfig struct {
	signals   map[types.ExportSignalKind]bool
	limits    types.ExportLimits
	diag      types.Diagnostics
	replay    types.ReplaySemantics
	redactor  types.Redactor
	policy    types.RedactionPolicy
	content   ContentMapping
	direct    bool
	diagGiven bool
}

// ExportOption adjusts NewPipeline.
type ExportOption func(*exportConfig)

// WithExportSignals narrows the pipeline's signal set; a destination whose
// capabilities do not cover it fails construction.
func WithExportSignals(sigs ...types.ExportSignalKind) ExportOption {
	return func(c *exportConfig) {
		c.signals = make(map[types.ExportSignalKind]bool, len(sigs))
		for _, s := range sigs {
			c.signals[s] = true
		}
	}
}

// WithExportBounds sets the admission bounds. The zero value means defaults.
func WithExportBounds(l types.ExportLimits) ExportOption {
	return func(c *exportConfig) { c.limits = l }
}

// WithExportDiagnostics sets the diagnostics sink.
func WithExportDiagnostics(d types.Diagnostics) ExportOption {
	return func(c *exportConfig) { c.diag = d; c.diagGiven = d != nil }
}

// WithRequiredReplay sets the minimum replay semantics every destination
// must declare.
func WithRequiredReplay(r types.ReplaySemantics) ExportOption {
	return func(c *exportConfig) { c.replay = r }
}

// WithRedactor routes surviving string attribute values through the
// redactor at admission.
func WithRedactor(r types.Redactor, p types.RedactionPolicy) ExportOption {
	return func(c *exportConfig) { c.redactor = r; c.policy = p }
}

// WithContentCapture sets the content-key policy; the default ContentNone
// drops content keys before serialization.
func WithContentCapture(content ContentMapping) ExportOption {
	return func(c *exportConfig) { c.content = content }
}

// DirectDelivery switches to synchronous delivery at Ingest with no queue
// and no worker; a send failure drops the record.
func DirectDelivery() ExportOption {
	return func(c *exportConfig) { c.direct = true }
}

type slogDiagnostics struct{}

func (slogDiagnostics) Report(ctx context.Context, d types.ExportDiagnostic) {
	slog.Default().WarnContext(ctx, "gohan telemetry export", "kind", d.Kind, "name", d.Name, "dropped", d.Dropped, "err", d.Err)
}

func defaultDiagnostics() types.Diagnostics {
	return types.BoundedDiagnostics(slogDiagnostics{})
}

func withExportLimits(l types.ExportLimits) types.ExportLimits {
	if l.MaxQueueDepth <= 0 {
		l.MaxQueueDepth = types.DefaultMaxQueueDepth
	}
	if l.MaxInstruments <= 0 {
		l.MaxInstruments = types.DefaultMaxInstruments
	}
	if l.MaxDimensionValues <= 0 {
		l.MaxDimensionValues = types.DefaultMaxDimensionValues
	}
	if l.MaxSeries <= 0 {
		l.MaxSeries = types.DefaultMaxSeries
	}
	return l
}

// NewPipeline returns a pipeline over dests. Configuration is validated
// before any record is admitted.
func NewPipeline(dests []types.ExportDestination, opts ...ExportOption) (*Pipeline, error) {
	cfg := exportConfig{
		signals: map[types.ExportSignalKind]bool{
			types.SignalSpan:   true,
			types.SignalMetric: true,
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	cfg.limits = withExportLimits(cfg.limits)
	if !cfg.diagGiven {
		cfg.diag = defaultDiagnostics()
	}
	p := &Pipeline{cfg: cfg, dests: make(map[string]*destState, len(dests))}
	for _, dest := range dests {
		if dest == nil {
			return nil, fmt.Errorf("gohan/telemetry: NewPipeline needs a non-nil destination")
		}
		name := dest.Name()
		if _, dup := p.dests[name]; dup {
			return nil, fmt.Errorf("gohan/telemetry: duplicate destination %q", name)
		}
		caps := dest.Capabilities()
		covered := make(map[types.ExportSignalKind]bool, len(caps.Signals))
		for _, s := range caps.Signals {
			covered[s] = true
		}
		for sig := range cfg.signals {
			if !covered[sig] {
				return nil, fmt.Errorf("gohan/telemetry: destination %q: %w: %s", name, types.ErrExportUnsupportedSignal, sig)
			}
		}
		if cfg.replay == types.ReplayDedupByID && caps.Replay != types.ReplayDedupByID {
			return nil, fmt.Errorf("gohan/telemetry: destination %q: %w", name, types.ErrIncompatibleReplay)
		}
		d := &destState{
			dest:    dest,
			name:    name,
			caps:    caps,
			limits:  cfg.limits,
			dimVals: make(map[string]map[string]struct{}),
			series:  make(map[string]struct{}),
			queue:   make(chan types.ExportRecord, cfg.limits.MaxQueueDepth),
			done:    make(chan struct{}),
		}
		d.cond = sync.NewCond(&d.mu)
		p.dests[name] = d
	}
	if !cfg.direct {
		for _, d := range p.dests {
			go d.work(p)
		}
	}
	return p, nil
}

type destState struct {
	dest   types.ExportDestination
	name   string
	caps   types.DestinationCapabilities
	limits types.ExportLimits

	queue chan types.ExportRecord

	mu        sync.Mutex
	cond      *sync.Cond
	backlog   []types.ExportRecord
	queued    int
	inflight  int
	stopped   bool
	drainSeq  int
	lastSeq   int
	drainDone bool
	drainCx   context.Context
	lastErr   error

	dimVals map[string]map[string]struct{}
	series  map[string]struct{}

	done chan struct{}
}

// Ingest admits recs for dest atomically: the full admission order runs
// before any enqueue and on refusal no record enters a queue. Admission
// may redact or drop attributes in place through the caller's record
// pointers, so a caller retaining records must copy first.
func (p *Pipeline) Ingest(ctx context.Context, dest string, recs []types.ExportRecord) error {
	p.mu.Lock()
	if p.shutdown {
		p.mu.Unlock()
		return fmt.Errorf("gohan/telemetry: ingest: %w", types.ErrExportShutdown)
	}
	d, ok := p.dests[dest]
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("gohan/telemetry: ingest %q: %w", dest, types.ErrUnknownDestination)
	}
	for i := range recs {
		if err := validateRecord(&recs[i]); err != nil {
			return err
		}
	}

	dropped := make([]string, 0, len(recs))
	kept := make([]types.ExportRecord, 0, len(recs))
	for _, rec := range recs {
		rec, keys := p.admit(ctx, rec)
		dropped = append(dropped, keys...)
		if hasPayload(rec) {
			kept = append(kept, rec)
		}
	}
	for _, key := range dropped {
		p.report(ctx, types.ExportDiagnostic{Kind: types.DiagAdmissionRejected, Name: key})
	}
	if len(kept) == 0 {
		return nil
	}

	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return fmt.Errorf("gohan/telemetry: ingest: %w", types.ErrExportShutdown)
	}
	if err := d.reserve(kept); err != nil {
		d.mu.Unlock()
		if errors.Is(err, types.ErrExportQueueFull) {
			p.report(ctx, types.ExportDiagnostic{Kind: types.DiagDeliveryDropped, Name: d.name, Dropped: int64(len(kept)), Err: err})
		} else {
			p.report(ctx, types.ExportDiagnostic{Kind: types.DiagAdmissionRejected, Name: d.name, Err: err})
		}
		return err
	}
	if p.cfg.direct {
		d.mu.Unlock()
		return p.deliverDirect(ctx, d, kept)
	}
	d.queued += len(kept)
	for _, rec := range kept {
		d.queue <- rec
	}
	d.cond.Broadcast()
	d.mu.Unlock()
	return nil
}

func validateRecord(rec *types.ExportRecord) error {
	switch {
	case rec.ID == "":
		return fmt.Errorf("gohan/telemetry: record: empty id")
	case rec.SchemaVersion != types.CurrentExportSchemaVersion:
		return fmt.Errorf("gohan/telemetry: record %s: schema version %d, want %d", rec.ID, rec.SchemaVersion, types.CurrentExportSchemaVersion)
	case rec.Signal == types.SignalSpan:
		if rec.Span == nil || rec.Metric != nil {
			return fmt.Errorf("gohan/telemetry: record %s: span signal without span payload", rec.ID)
		}
	case rec.Signal == types.SignalMetric:
		if rec.Metric == nil || rec.Span != nil {
			return fmt.Errorf("gohan/telemetry: record %s: metric signal without metric payload", rec.ID)
		}
	default:
		return fmt.Errorf("gohan/telemetry: record %s: unknown signal %q", rec.ID, rec.Signal)
	}
	return nil
}

func hasPayload(rec types.ExportRecord) bool {
	return rec.Span != nil || rec.Metric != nil
}

// admit runs the content policy and the redactor over one record's
// attributes and returns the record with the surviving keys.
func (p *Pipeline) admit(ctx context.Context, rec types.ExportRecord) (types.ExportRecord, []string) {
	var dropped []string
	apply := func(attrs []types.Attr) ([]types.Attr, []string) {
		out := make([]types.Attr, 0, len(attrs))
		var keys []string
		for _, a := range attrs {
			if isContentKey(a.Key) {
				switch p.cfg.content {
				case ContentNone:
					keys = append(keys, a.Key)
					continue
				case ContentHashes:
					a.Value = hashAttr(a.Value)
				case ContentRedacted:
					a.Value = "[redacted]"
				}
			}
			out = append(out, a)
		}
		return out, keys
	}
	if rec.Span != nil {
		attrs, keys := apply(rec.Span.Attrs)
		dropped = append(dropped, keys...)
		rec.Span.Attrs, _ = p.redact(ctx, attrs)
	}
	if rec.Metric != nil {
		dims, keys := apply(rec.Metric.Dimensions)
		dropped = append(dropped, keys...)
		redacted, _ := p.redact(ctx, dims)
		rec.Metric.Dimensions = dropForbiddenDimensions(redacted)
	}
	return rec, dropped
}

// redact round-trips every surviving string attribute value through the
// configured redactor when one is set. Redaction fails closed: an unusable
// result drops the attribute and reports a rejection, never the raw value.
func (p *Pipeline) redact(ctx context.Context, attrs []types.Attr) ([]types.Attr, []string) {
	if p.cfg.redactor == nil {
		return attrs, nil
	}
	out := make([]types.Attr, 0, len(attrs))
	var dropped []string
	for _, a := range attrs {
		v, ok := a.Value.(string)
		if !ok {
			out = append(out, a)
			continue
		}
		blocks, _, err := p.cfg.redactor.Redact(ctx, []types.Block{types.Text{Text: v}}, p.cfg.policy)
		var t types.Text
		var isText bool
		if len(blocks) > 0 {
			t, isText = blocks[0].(types.Text)
		}
		if err != nil || len(blocks) == 0 || !isText {
			if err == nil {
				err = errors.New("gohan/telemetry: redaction returned no usable result")
			}
			p.report(ctx, types.ExportDiagnostic{Kind: types.DiagAdmissionRejected, Name: a.Key, Err: err})
			dropped = append(dropped, a.Key)
			continue
		}
		a.Value = t.Text
		out = append(out, a)
	}
	return out, dropped
}

func dropForbiddenDimensions(attrs []types.Attr) []types.Attr {
	out := make([]types.Attr, 0, len(attrs))
	for _, a := range attrs {
		if forbiddenLabels[a.Key] || forbiddenLabels[normalizeLabel(a.Key)] || renamedIdentity[a.Key] {
			continue
		}
		out = append(out, a)
	}
	return out
}

// reserve applies the bounds under the destination lock. On refusal no
// state mutates.
func (d *destState) reserve(recs []types.ExportRecord) error {
	dimNew := map[string]map[string]struct{}{}
	seriesNew := map[string]struct{}{}
	for _, rec := range recs {
		if rec.Metric == nil {
			continue
		}
		for _, a := range rec.Metric.Dimensions {
			seen := d.dimVals[a.Key]
			if seen == nil {
				seen = map[string]struct{}{}
			}
			if _, ok := seen[fmt.Sprint(a.Value)]; ok {
				continue
			}
			add := dimNew[a.Key]
			if add == nil {
				add = map[string]struct{}{}
				dimNew[a.Key] = add
			}
			add[fmt.Sprint(a.Value)] = struct{}{}
		}
		key := seriesKey(rec.Metric)
		if _, ok := d.series[key]; !ok {
			seriesNew[key] = struct{}{}
		}
	}
	for key, add := range dimNew {
		if len(d.dimVals[key])+len(add) > d.limits.MaxDimensionValues {
			return fmt.Errorf("gohan/telemetry: dimension %q: %w", key, types.ErrDimensionLimit)
		}
	}
	if len(d.series)+len(seriesNew) > d.limits.MaxSeries {
		return fmt.Errorf("gohan/telemetry: destination %q: %w", d.name, types.ErrSeriesLimit)
	}
	if d.queued+d.inflight+len(d.backlog)+len(recs) > d.limits.MaxQueueDepth {
		return fmt.Errorf("gohan/telemetry: destination %q: %w", d.name, types.ErrExportQueueFull)
	}
	for key, add := range dimNew {
		if d.dimVals[key] == nil {
			d.dimVals[key] = add
			continue
		}
		for v := range add {
			d.dimVals[key][v] = struct{}{}
		}
	}
	for key := range seriesNew {
		d.series[key] = struct{}{}
	}
	return nil
}

func seriesKey(m *types.MetricRecord) string {
	keys := make([]string, 0, len(m.Dimensions))
	for _, a := range m.Dimensions {
		keys = append(keys, a.Key+"="+fmt.Sprint(a.Value))
	}
	sort.Strings(keys)
	return m.Instrument.Name + "|" + strings.Join(keys, ",")
}

func (p *Pipeline) deliverDirect(ctx context.Context, d *destState, recs []types.ExportRecord) error {
	retained := p.attempt(ctx, d, recs)
	if len(retained) == 0 {
		return nil
	}
	err := &types.ExportError{Destination: d.name, Err: d.err()}
	p.report(ctx, types.ExportDiagnostic{Kind: types.DiagDeliveryDropped, Name: d.name, Dropped: int64(len(retained)), Err: err})
	return err
}

// attempt runs one delivery attempt over batch and applies the outcome
// state machine; the return carries records that stay unresolved.
func (p *Pipeline) attempt(ctx context.Context, d *destState, batch []types.ExportRecord) []types.ExportRecord {
	outcomes, err := d.dest.Export(ctx, batch)
	byID := make(map[types.RecordID]types.RecordOutcome, len(outcomes))
	for _, o := range outcomes {
		byID[o.ID] = o
	}
	retained := make([]types.ExportRecord, 0, len(batch))
	for _, rec := range batch {
		o, ok := byID[rec.ID]
		switch {
		case !ok:
			retained = append(retained, rec)
		case o.Status == types.OutcomeAccepted:
		case o.Status == types.OutcomePermanent:
			p.report(ctx, types.ExportDiagnostic{Kind: types.DiagDeliveryDropped, Name: d.name, Err: messageErr(o.Message)})
		case o.Status == types.OutcomeAmbiguous:
			p.report(ctx, types.ExportDiagnostic{Kind: types.DiagDeliveryAmbiguous, Name: d.name})
		default:
			retained = append(retained, rec)
		}
	}
	d.mu.Lock()
	d.lastErr = err
	d.mu.Unlock()
	return retained
}

func messageErr(msg string) error {
	if msg == "" {
		return nil
	}
	return errors.New(msg)
}

func (d *destState) err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastErr
}

// work is the one worker per destination. Delivery attempts happen only on
// an explicit Flush or Shutdown drain, one attempt per drain, always under
// the caller's ctx so a blocking destination cannot hang the pipeline.
func (d *destState) work(p *Pipeline) {
	defer close(d.done)
	for {
		d.mu.Lock()
		for d.lastSeq == d.drainSeq && !d.stopped {
			d.cond.Wait()
		}
		if d.stopped {
			d.mu.Unlock()
			return
		}
		d.lastSeq = d.drainSeq
		d.mu.Unlock()

		batch := d.collect()
		if len(batch) == 0 {
			d.finishDrain()
			continue
		}
		retained := p.attempt(p.workerCtx(d), d, batch)
		d.mu.Lock()
		d.backlog = retained
		d.inflight = 0
		d.drainDone = true
		d.cond.Broadcast()
		d.mu.Unlock()
	}
}

// collect takes the backlog plus everything queued into one attempt batch.
func (d *destState) collect() []types.ExportRecord {
	d.mu.Lock()
	batch := d.backlog
	d.backlog = nil
	take := d.queued
	d.inflight = len(batch) + take
	d.mu.Unlock()
	for range take {
		select {
		case rec := <-d.queue:
			d.mu.Lock()
			d.queued--
			d.mu.Unlock()
			batch = append(batch, rec)
		default:
		}
	}
	return batch
}

func (d *destState) finishDrain() {
	d.mu.Lock()
	d.drainDone = true
	d.cond.Broadcast()
	d.mu.Unlock()
}

func (p *Pipeline) workerCtx(d *destState) context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.drainCx != nil {
		return d.drainCx
	}
	return context.Background()
}

// drainWithin requests one drain attempt and waits for the worker to
// finish it, allowing a grace window past ctx for the in-flight Export to
// land. It returns the unresolved record count.
func (d *destState) drainWithin(ctx context.Context) int64 {
	finished := make(chan struct{})
	var grace atomic.Bool
	go func() {
		select {
		case <-ctx.Done():
			t := time.AfterFunc(graceWindow, func() {
				grace.Store(true)
				d.mu.Lock()
				d.cond.Broadcast()
				d.mu.Unlock()
			})
			select {
			case <-t.C:
			case <-finished:
				t.Stop()
			}
		case <-finished:
		}
	}()
	defer close(finished)
	d.mu.Lock()
	d.drainSeq++
	d.drainDone = false
	d.drainCx = ctx
	d.cond.Broadcast()
	for !d.drainDone && !grace.Load() {
		d.cond.Wait()
	}
	unresolved := int64(len(d.backlog) + d.queued)
	d.drainCx = nil
	d.mu.Unlock()
	return unresolved
}

func (p *Pipeline) report(ctx context.Context, diag types.ExportDiagnostic) {
	p.cfg.diag.Report(ctx, diag)
}

func workOf(counts map[string]int64) types.UnresolvedWork {
	var w types.UnresolvedWork
	for dest, n := range counts {
		if n == 0 {
			continue
		}
		w.Records += n
		if w.ByDestination == nil {
			w.ByDestination = make(map[string]int64, len(counts))
		}
		w.ByDestination[dest] = n
	}
	return w
}

// Flush drains within ctx and reports what remains. After Shutdown it
// returns immediately.
func (p *Pipeline) Flush(ctx context.Context) (types.UnresolvedWork, error) {
	p.mu.Lock()
	shut := p.shutdown
	p.mu.Unlock()
	if shut || p.cfg.direct {
		p.closeCycle(ctx, false)
		return types.UnresolvedWork{}, nil
	}
	unresolved, errs := p.drainAll(ctx)
	p.closeCycle(ctx, false)
	err := errors.Join(errs...)
	if ctx.Err() != nil {
		err = errors.Join(err, fmt.Errorf("gohan/telemetry: flush: %w", ctx.Err()))
	}
	return workOf(unresolved), err
}

// Shutdown stops workers after one final bounded drain and is idempotent.
func (p *Pipeline) Shutdown(ctx context.Context) (types.UnresolvedWork, error) {
	p.mu.Lock()
	already := p.shutdown
	p.shutdown = true
	p.mu.Unlock()
	if p.cfg.direct {
		p.closeCycle(ctx, true)
		return types.UnresolvedWork{}, nil
	}
	var counts map[string]int64
	var errs []error
	if !already {
		counts, errs = p.drainAll(ctx)
	}
	for _, d := range p.dests {
		d.mu.Lock()
		d.stopped = true
		d.cond.Broadcast()
		d.mu.Unlock()
		<-d.done
	}
	if already {
		return types.UnresolvedWork{}, nil
	}
	p.closeCycle(ctx, true)
	err := errors.Join(errs...)
	if ctx.Err() != nil {
		err = errors.Join(err, fmt.Errorf("gohan/telemetry: shutdown: %w", ctx.Err()))
	}
	return workOf(counts), err
}

func (p *Pipeline) drainAll(ctx context.Context) (map[string]int64, []error) {
	counts := make(map[string]int64, len(p.dests))
	var errs []error
	for name, d := range p.dests {
		counts[name] = d.drainWithin(ctx)
		if err := d.err(); err != nil {
			errs = append(errs, &types.ExportError{Destination: name, Err: err})
		}
	}
	return counts, errs
}

type cycleEnder interface {
	Flush(ctx context.Context)
	Shutdown(ctx context.Context)
}

func (p *Pipeline) closeCycle(ctx context.Context, shutdown bool) {
	ender, ok := p.cfg.diag.(cycleEnder)
	if !ok {
		return
	}
	if shutdown {
		ender.Shutdown(ctx)
		return
	}
	ender.Flush(ctx)
}

var _ types.RecordSink = (*Pipeline)(nil)
