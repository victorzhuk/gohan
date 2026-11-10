package telemetry

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type capDest struct {
	name string
	caps types.DestinationCapabilities

	mu    sync.Mutex
	batch []types.ExportRecord
	seen  [][]types.ExportRecord
}

func newCapDest(name string) *capDest {
	return &capDest{
		name: name,
		caps: types.DestinationCapabilities{
			Signals:     []types.ExportSignalKind{types.SignalSpan, types.SignalMetric},
			Replay:      types.ReplayDedupByID,
			Temporality: types.TemporalityCumulative,
		},
	}
}

func (d *capDest) Name() string                                { return d.name }
func (d *capDest) Capabilities() types.DestinationCapabilities { return d.caps }

func (d *capDest) Export(ctx context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]types.RecordOutcome, 0, len(recs))
	for _, rec := range recs {
		out = append(out, types.RecordOutcome{ID: rec.ID, Status: types.OutcomeAccepted})
	}
	d.batch = append(d.batch, recs...)
	d.seen = append(d.seen, recs)
	return out, nil
}

func (d *capDest) records() []types.ExportRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]types.ExportRecord(nil), d.batch...)
}

type fakeRedactor struct {
	mu     sync.Mutex
	inputs []string
}

func (r *fakeRedactor) Redact(ctx context.Context, blocks []types.Block, p types.RedactionPolicy) ([]types.Block, types.RedactionMap, error) {
	t, ok := blocks[0].(types.Text)
	if !ok {
		return blocks, types.RedactionMap{}, nil
	}
	r.mu.Lock()
	r.inputs = append(r.inputs, t.Text)
	r.mu.Unlock()
	return []types.Block{types.Text{Text: "<redacted>" + t.Text}}, types.RedactionMap{}, nil
}

func (r *fakeRedactor) Rehydrate(ctx context.Context, blocks []types.Block, m types.RedactionMap) ([]types.Block, error) {
	return blocks, nil
}

func spanRec(id string, attrs ...types.Attr) types.ExportRecord {
	return types.ExportRecord{
		SchemaVersion: types.CurrentExportSchemaVersion,
		ID:            types.RecordID(id),
		Resource:      types.ResourceIdentity{Name: "gohan"},
		Signal:        types.SignalSpan,
		Span:          &types.SpanRecord{Name: "chat", Outcome: types.SpanOK, Attrs: attrs},
	}
}

func metricRec(id string, dims ...types.Attr) types.ExportRecord {
	return types.ExportRecord{
		SchemaVersion: types.CurrentExportSchemaVersion,
		ID:            types.RecordID(id),
		Resource:      types.ResourceIdentity{Name: "gohan"},
		Signal:        types.SignalMetric,
		Metric: &types.MetricRecord{
			Instrument:  types.Instrument{Name: "gohan.model.ttft", Kind: types.InstrumentCounter},
			Temporality: types.TemporalityCumulative,
			Dimensions:  dims,
			Value:       types.MetricValue{Int: new(int64(1))},
		},
	}
}

func attrByKey(recs []types.ExportRecord, key string) (types.Attr, bool) {
	for _, rec := range recs {
		if rec.Span == nil {
			continue
		}
		for _, a := range rec.Span.Attrs {
			if a.Key == key {
				return a, true
			}
		}
	}
	return types.Attr{}, false
}

func TestExportAdmissionOrder(t *testing.T) {
	ctx := t.Context()
	dest := newCapDest("mem")
	red := &fakeRedactor{}
	p, err := NewPipeline([]types.ExportDestination{dest},
		WithRedactor(red, types.RedactionPolicy{}),
		DirectDelivery(),
	)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	rec := spanRec("s1",
		types.String("gohan.content.input", "hunter2-secret"),
		types.String("user.email", "alice@example.com"),
		types.String("plain", "ok"),
	)
	if err := p.Ingest(ctx, dest.name, []types.ExportRecord{rec}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	got := dest.records()
	if len(got) != 1 {
		t.Fatalf("admitted %d records, want 1", len(got))
	}
	for _, a := range got[0].Span.Attrs {
		if strings.Contains(fmtAttr(a), "hunter2-secret") {
			t.Fatalf("content secret reached the destination: %v", got[0].Span.Attrs)
		}
	}
	if _, ok := attrByKey(got, "gohan.content.input"); ok {
		t.Fatal("content attr survived ContentNone")
	}
	a, ok := attrByKey(got, "user.email")
	if !ok || a.Value != "<redacted>alice@example.com" {
		t.Fatalf("PII attr not redacted: %v", got[0].Span.Attrs)
	}
	for _, in := range red.snapshot() {
		if strings.Contains(in, "hunter2-secret") {
			t.Fatal("redactor saw a content-key value before the content policy dropped it")
		}
	}
}

func fmtAttr(a types.Attr) string {
	v, _ := a.Value.(string)
	return a.Key + "=" + v
}

func (r *fakeRedactor) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.inputs...)
}

func TestExportDimensionFiltering(t *testing.T) {
	ctx := t.Context()
	dest := newCapDest("mem")
	p, err := NewPipeline([]types.ExportDestination{dest}, DirectDelivery())
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	rec := metricRec("m1",
		types.String("session_id", "s-1"),
		types.String("gohan.session_id", "s-1"),
		types.String("run_id", "r-1"),
		types.String("gohan.run_id", "r-1"),
		types.String("subject", "x"),
		types.String("approver", "y"),
		types.String("flow", "chat"),
	)
	if err := p.Ingest(ctx, dest.name, []types.ExportRecord{rec}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	got := dest.records()
	if len(got) != 1 {
		t.Fatalf("admitted %d records, want 1", len(got))
	}
	for _, a := range got[0].Metric.Dimensions {
		if forbiddenLabels[a.Key] || forbiddenLabels[normalizeLabel(a.Key)] {
			t.Fatalf("identity dimension %q reached the destination", a.Key)
		}
	}
	if len(got[0].Metric.Dimensions) != 1 || got[0].Metric.Dimensions[0].Key != "flow" {
		t.Fatalf("dimensions = %v, want only flow", got[0].Metric.Dimensions)
	}
}

func TestExportMalformedRefusals(t *testing.T) {
	ctx := t.Context()
	dest := newCapDest("mem")
	p, err := NewPipeline([]types.ExportDestination{dest})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	cases := []struct {
		name string
		rec  types.ExportRecord
		want string
	}{
		{"empty id", func() types.ExportRecord { r := spanRec(""); return r }(), "empty id"},
		{"wrong schema", func() types.ExportRecord {
			r := spanRec("s1")
			r.SchemaVersion = 99
			return r
		}(), "schema version"},
		{"span without payload", func() types.ExportRecord {
			r := spanRec("s1")
			r.Span = nil
			return r
		}(), "span signal without span payload"},
		{"metric mismatch", func() types.ExportRecord {
			r := spanRec("s1")
			r.Signal = types.SignalMetric
			return r
		}(), "metric signal without metric payload"},
		{"unknown signal", func() types.ExportRecord {
			r := spanRec("s1")
			r.Signal = "trace"
			return r
		}(), "unknown signal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Ingest(ctx, dest.name, []types.ExportRecord{tc.rec})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Ingest err = %v, want it to name %q", err, tc.want)
			}
			if len(dest.records()) != 0 {
				t.Fatal("malformed record reached the destination")
			}
		})
	}
}

func TestExportUnknownDestination(t *testing.T) {
	ctx := t.Context()
	dest := newCapDest("mem")
	p, err := NewPipeline([]types.ExportDestination{dest})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	err = p.Ingest(ctx, "other", []types.ExportRecord{spanRec("s1")})
	if !errors.Is(err, types.ErrUnknownDestination) {
		t.Fatalf("Ingest err = %v, want ErrUnknownDestination", err)
	}
}

func TestExportPostShutdownRefusal(t *testing.T) {
	ctx := t.Context()
	dest := newCapDest("mem")
	p, err := NewPipeline([]types.ExportDestination{dest})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if _, err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	err = p.Ingest(ctx, dest.name, []types.ExportRecord{spanRec("s1")})
	if !errors.Is(err, types.ErrExportShutdown) {
		t.Fatalf("Ingest err = %v, want ErrExportShutdown", err)
	}
}

func TestExportQueueBound(t *testing.T) {
	ctx := t.Context()
	dest := newCapDest("blocked-never-drains")
	dest.caps.Replay = types.ReplayNone
	p, err := NewPipeline([]types.ExportDestination{dest}, WithExportBounds(types.ExportLimits{MaxQueueDepth: 2}))
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	for i := range 2 {
		if err := p.Ingest(ctx, dest.name, []types.ExportRecord{spanRec("q" + string(rune('a'+i)))}); err != nil {
			t.Fatalf("Ingest %d: %v", i, err)
		}
	}
	err = p.Ingest(ctx, dest.name, []types.ExportRecord{spanRec("qc")})
	if !errors.Is(err, types.ErrExportQueueFull) {
		t.Fatalf("Ingest err = %v, want ErrExportQueueFull", err)
	}
	if _, err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

type failRedactor struct{}

func (failRedactor) Redact(ctx context.Context, blocks []types.Block, p types.RedactionPolicy) ([]types.Block, types.RedactionMap, error) {
	return nil, types.RedactionMap{}, errors.New("redactor down")
}

func (failRedactor) Rehydrate(ctx context.Context, blocks []types.Block, m types.RedactionMap) ([]types.Block, error) {
	return blocks, nil
}

type capturingDiags struct {
	mu      sync.Mutex
	reports []types.ExportDiagnostic
}

func (c *capturingDiags) Report(ctx context.Context, d types.ExportDiagnostic) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = append(c.reports, d)
}

func (c *capturingDiags) ofKind(k types.DiagnosticKind) []types.ExportDiagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []types.ExportDiagnostic
	for _, r := range c.reports {
		if r.Kind == k {
			out = append(out, r)
		}
	}
	return out
}

func TestExportRedactionFailsClosed(t *testing.T) {
	ctx := t.Context()
	dest := newCapDest("mem")
	diags := &capturingDiags{}
	p, err := NewPipeline([]types.ExportDestination{dest},
		WithRedactor(failRedactor{}, types.RedactionPolicy{}),
		WithExportDiagnostics(diags),
		DirectDelivery(),
	)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	rec := spanRec("s1",
		types.String("user.email", "alice@example.com"),
		types.Bool("plain", true),
	)
	if err := p.Ingest(ctx, dest.name, []types.ExportRecord{rec}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	got := dest.records()
	if len(got) != 1 {
		t.Fatalf("admitted %d records, want 1", len(got))
	}
	for _, a := range got[0].Span.Attrs {
		if strings.Contains(fmtAttr(a), "alice@example.com") {
			t.Fatalf("raw value survived a failed redaction: %v", got[0].Span.Attrs)
		}
	}
	if _, ok := attrByKey(got, "user.email"); ok {
		t.Fatal("unredactable attr was admitted")
	}
	if _, ok := attrByKey(got, "plain"); !ok {
		t.Fatal("non-string attr was dropped")
	}
	rejected := diags.ofKind(types.DiagAdmissionRejected)
	if len(rejected) != 1 || rejected[0].Name != "user.email" {
		t.Fatalf("DiagAdmissionRejected = %+v, want one naming user.email", rejected)
	}
	if _, err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

type retryDest struct {
	capDest
}

func newRetryDest(name string) *retryDest {
	return &retryDest{capDest: *newCapDest(name)}
}

func (d *retryDest) Export(ctx context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	out, err := d.capDest.Export(ctx, recs)
	for i := range out {
		out[i].Status = types.OutcomeRetryable
	}
	return out, err
}

func TestExportBacklogCountsTowardQueueBound(t *testing.T) {
	ctx := context.Background()
	dest := newRetryDest("retrying")
	p, err := NewPipeline([]types.ExportDestination{dest}, WithExportBounds(types.ExportLimits{MaxQueueDepth: 2}))
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	for _, id := range []string{"b1", "b2"} {
		if err := p.Ingest(ctx, dest.name, []types.ExportRecord{spanRec(id)}); err != nil {
			t.Fatalf("Ingest %s: %v", id, err)
		}
	}
	if _, err := p.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	err = p.Ingest(ctx, dest.name, []types.ExportRecord{spanRec("b3")})
	if !errors.Is(err, types.ErrExportQueueFull) {
		t.Fatalf("Ingest err = %v, want ErrExportQueueFull while backlog holds the bound", err)
	}
	if _, err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestExportFlushesLeakNoWatchers(t *testing.T) {
	ctx := context.Background()
	dest := newCapDest("mem")
	p, err := NewPipeline([]types.ExportDestination{dest})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := p.Ingest(ctx, dest.name, []types.ExportRecord{spanRec("s1")}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	base := runtime.NumGoroutine()
	for range 3 {
		if _, err := p.Flush(ctx); err != nil {
			t.Fatalf("Flush: %v", err)
		}
		if _, err := p.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > base {
		t.Fatalf("goroutines after cycles = %d, want back to baseline %d", got, base)
	}
}

func TestExportRenamedIdentityDimensionsDropped(t *testing.T) {
	ctx := t.Context()
	dest := newCapDest("mem")
	p, err := NewPipeline([]types.ExportDestination{dest}, DirectDelivery())
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	rec := metricRec("m2",
		types.String("gen_ai.conversation.id", "conv-1"),
		types.String("langfuse.session.id", "sess-1"),
		types.String("flow", "chat"),
	)
	if err := p.Ingest(ctx, dest.name, []types.ExportRecord{rec}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	got := dest.records()
	if len(got) != 1 {
		t.Fatalf("admitted %d records, want 1", len(got))
	}
	dims := got[0].Metric.Dimensions
	for _, a := range dims {
		if a.Key == "gen_ai.conversation.id" || a.Key == "langfuse.session.id" {
			t.Fatalf("renamed identity dimension %q reached the destination", a.Key)
		}
	}
	if len(dims) != 1 || dims[0].Key != "flow" {
		t.Fatalf("dimensions = %v, want only flow", dims)
	}
	if _, err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
