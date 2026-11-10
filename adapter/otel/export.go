package otel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"uuid"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/victorzhuk/gohan/core/types"
)

// Option adjusts NewExportPipeline.
type Option func(*exportConfig)

type exportConfig struct {
	diag        types.Diagnostics
	bounds      types.ExportLimits
	signals     map[types.ExportSignalKind]bool
	instruments map[string]types.Instrument
	conflicts   []string
	badBounds   []string
	resource    string
}

func defaultExportConfig() exportConfig {
	return exportConfig{
		diag:     types.BoundedDiagnostics(warnDiagnostics{logger: slog.Default()}),
		signals:  map[types.ExportSignalKind]bool{types.SignalSpan: true, types.SignalMetric: true},
		resource: "gohan",
	}
}

// WithDiagnostics sets the diagnostics sink.
func WithDiagnostics(d types.Diagnostics) Option {
	return func(c *exportConfig) { c.diag = d }
}

// WithExportBounds sets the admission bounds.
func WithExportBounds(b types.ExportLimits) Option {
	return func(c *exportConfig) { c.bounds = b }
}

// WithSignals narrows the produced signal set.
func WithSignals(sigs ...types.ExportSignalKind) Option {
	return func(c *exportConfig) {
		c.signals = make(map[types.ExportSignalKind]bool, len(sigs))
		for _, s := range sigs {
			c.signals[s] = true
		}
	}
}

// WithInstruments declares instrument descriptors for lazily created
// instruments. Identical duplicates are idempotent; conflicting ones fail
// construction with types.ErrInstrumentConflict.
func WithInstruments(insts ...types.Instrument) Option {
	return func(c *exportConfig) {
		seen := c.instruments
		if seen == nil {
			seen = map[string]types.Instrument{}
			c.instruments = seen
		}
		for _, inst := range insts {
			if prev, ok := seen[inst.Name]; ok {
				if !sameDescriptor(prev, inst) {
					c.conflicts = append(c.conflicts, inst.Name)
				}
				continue
			}
			for i := 1; i < len(inst.Boundaries); i++ {
				if inst.Boundaries[i] <= inst.Boundaries[i-1] {
					c.badBounds = append(c.badBounds, inst.Name)
					break
				}
			}
			seen[inst.Name] = inst
		}
	}
}

// WithResourceName sets the producing resource's name.
func WithResourceName(name string) Option {
	return func(c *exportConfig) { c.resource = name }
}

func instrumentLimit(l types.ExportLimits) int {
	if l.MaxInstruments <= 0 {
		return types.DefaultMaxInstruments
	}
	return l.MaxInstruments
}

// sameDescriptor reports byte-equal instrument declarations; boundaries are
// compared element-wise, order included.
func sameDescriptor(a, b types.Instrument) bool {
	if a.Name != b.Name || a.Kind != b.Kind || a.Unit != b.Unit || a.Description != b.Description {
		return false
	}
	if len(a.Boundaries) != len(b.Boundaries) {
		return false
	}
	for i := range a.Boundaries {
		if a.Boundaries[i] != b.Boundaries[i] {
			return false
		}
	}
	return true
}

// claimName admits one distinct instrument name under the limit. An already
// seen name always passes.
func claimName(seen *sync.Map, count *atomic.Int64, limit int, name string) bool {
	if _, ok := seen.Load(name); ok {
		return true
	}
	if int(count.Load()) >= limit {
		return false
	}
	if _, loaded := seen.LoadOrStore(name, true); loaded {
		return true
	}
	count.Add(1)
	return true
}

// cycleDiagnostics closes the bounded-diagnostics cycle at the owning
// pipeline's Flush or Shutdown; absent the assertion, diagnostics have no
// cycle to close.
type cycleDiagnostics interface {
	Flush(ctx context.Context)
	Shutdown(ctx context.Context)
}

func endCycle(ctx context.Context, d types.Diagnostics, shutdown bool) {
	c, ok := d.(cycleDiagnostics)
	if !ok {
		return
	}
	if shutdown {
		c.Shutdown(ctx)
		return
	}
	c.Flush(ctx)
}

// ExportPipeline owns its tracer and meter providers and pushes translated
// records into a record sink. It implements types.Telemetry,
// types.GaugeTelemetry and types.TelemetryLifecycle.
type ExportPipeline struct {
	sink  types.RecordSink
	dests []types.ExportDestination
	cfg   exportConfig

	tracer trace.Tracer
	meter  metric.Meter

	tp *sdktrace.TracerProvider
	mp *sdkmetric.MeterProvider

	claimed   sync.Map
	instCount atomic.Int64
	counters  sync.Map
	hists     sync.Map
	gauges    sync.Map

	mu       sync.Mutex
	shutdown bool
}

var (
	_ types.Telemetry          = (*ExportPipeline)(nil)
	_ types.GaugeTelemetry     = (*ExportPipeline)(nil)
	_ types.TelemetryLifecycle = (*ExportPipeline)(nil)
)

// NewExportPipeline builds an owned pipeline pushing translated records into
// sink for the named destinations. Configuration is validated before
// admission, exactly like telemetry.NewPipeline.
func NewExportPipeline(sink types.RecordSink, dests []types.ExportDestination, opts ...Option) (*ExportPipeline, error) {
	if sink == nil {
		return nil, errors.New("gohan/otel: NewExportPipeline needs a record sink")
	}
	cfg := defaultExportConfig()
	for _, o := range opts {
		o(&cfg)
	}
	names := make(map[string]bool, len(dests))
	var spanDests, metricDests []types.ExportDestination
	for _, d := range dests {
		if d == nil {
			return nil, errors.New("gohan/otel: nil export destination")
		}
		name := d.Name()
		if names[name] {
			return nil, fmt.Errorf("gohan/otel: duplicate destination %q", name)
		}
		names[name] = true
		caps := d.Capabilities()
		for sig := range cfg.signals {
			if !hasSignal(caps.Signals, sig) {
				return nil, fmt.Errorf("gohan/otel: destination %q: %w", name, types.ErrExportUnsupportedSignal)
			}
		}
		if cfg.signals[types.SignalSpan] && hasSignal(caps.Signals, types.SignalSpan) {
			spanDests = append(spanDests, d)
		}
		if cfg.signals[types.SignalMetric] && hasSignal(caps.Signals, types.SignalMetric) {
			metricDests = append(metricDests, d)
		}
	}
	if len(cfg.badBounds) > 0 {
		return nil, fmt.Errorf("gohan/otel: instrument %q: non-monotonic boundaries", cfg.badBounds[0])
	}
	if len(cfg.conflicts) > 0 {
		return nil, fmt.Errorf("gohan/otel: instrument %q: %w", cfg.conflicts[0], types.ErrInstrumentConflict)
	}
	if len(cfg.instruments) > instrumentLimit(cfg.bounds) {
		return nil, fmt.Errorf("gohan/otel: %w: %d declared instruments", types.ErrInstrumentLimit, len(cfg.instruments))
	}

	p := &ExportPipeline{sink: sink, dests: dests, cfg: cfg}
	res := resource.NewSchemaless(attribute.String("service.name", cfg.resource))
	if len(spanDests) > 0 {
		p.tp = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(&spanExporter{pipe: p, dests: spanDests}),
		)
		p.tracer = p.tp.Tracer("github.com/victorzhuk/gohan")
	}
	if len(metricDests) > 0 {
		mpOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
		for _, d := range metricDests {
			mpOpts = append(mpOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(&metricExporter{pipe: p, dest: d})))
		}
		p.mp = sdkmetric.NewMeterProvider(mpOpts...)
		p.meter = p.mp.Meter("github.com/victorzhuk/gohan")
	}
	return p, nil
}

func hasSignal(sigs []types.ExportSignalKind, sig types.ExportSignalKind) bool {
	for _, s := range sigs {
		if s == sig {
			return true
		}
	}
	return false
}

// StartSpan opens a span on the owned provider.
func (p *ExportPipeline) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	if p.tracer == nil {
		return ctx, func(...types.Attr) {}
	}
	ctx, span := p.tracer.Start(ctx, name, trace.WithAttributes(mapAttrs(attrs)...))
	return ctx, func(late ...types.Attr) {
		span.SetAttributes(mapAttrs(late)...)
		span.End()
	}
}

// Count adds n to the counter named name.
func (p *ExportPipeline) Count(ctx context.Context, name string, n int64, attrs ...types.Attr) {
	c, ok := p.counter(name)
	if !ok {
		return
	}
	c.Add(ctx, n, metric.WithAttributes(mapAttrs(attrs)...))
}

// Record observes v on the histogram named name.
func (p *ExportPipeline) Record(ctx context.Context, name string, v float64, attrs ...types.Attr) {
	h, ok := p.histogram(name)
	if !ok {
		return
	}
	h.Record(ctx, v, metric.WithAttributes(mapAttrs(attrs)...))
}

// Gauge observes v on the float gauge named name.
func (p *ExportPipeline) Gauge(ctx context.Context, name string, v float64, attrs ...types.Attr) {
	g, ok := p.gauge(name)
	if !ok {
		return
	}
	g.Record(ctx, v, metric.WithAttributes(mapAttrs(attrs)...))
}

func (p *ExportPipeline) counter(name string) (metric.Int64Counter, bool) {
	if c, ok := p.counters.Load(name); ok {
		return c.(metric.Int64Counter), true
	}
	unit, desc, _, ok := p.claim(name)
	if !ok {
		return nil, false
	}
	var opts []metric.Int64CounterOption
	if unit != "" {
		opts = append(opts, metric.WithUnit(unit))
	}
	if desc != "" {
		opts = append(opts, metric.WithDescription(desc))
	}
	c, err := p.meter.Int64Counter(name, opts...)
	if err != nil {
		p.reportRejected(name, err)
		return nil, false
	}
	actual, _ := p.counters.LoadOrStore(name, c)
	return actual.(metric.Int64Counter), true
}

func (p *ExportPipeline) histogram(name string) (metric.Float64Histogram, bool) {
	if h, ok := p.hists.Load(name); ok {
		return h.(metric.Float64Histogram), true
	}
	unit, desc, boundaries, ok := p.claim(name)
	if !ok {
		return nil, false
	}
	var opts []metric.Float64HistogramOption
	if unit != "" {
		opts = append(opts, metric.WithUnit(unit))
	}
	if desc != "" {
		opts = append(opts, metric.WithDescription(desc))
	}
	if len(boundaries) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(boundaries...))
	}
	h, err := p.meter.Float64Histogram(name, opts...)
	if err != nil {
		p.reportRejected(name, err)
		return nil, false
	}
	actual, _ := p.hists.LoadOrStore(name, h)
	return actual.(metric.Float64Histogram), true
}

func (p *ExportPipeline) gauge(name string) (metric.Float64Gauge, bool) {
	if g, ok := p.gauges.Load(name); ok {
		return g.(metric.Float64Gauge), true
	}
	unit, desc, _, ok := p.claim(name)
	if !ok {
		return nil, false
	}
	var opts []metric.Float64GaugeOption
	if unit != "" {
		opts = append(opts, metric.WithUnit(unit))
	}
	if desc != "" {
		opts = append(opts, metric.WithDescription(desc))
	}
	g, err := p.meter.Float64Gauge(name, opts...)
	if err != nil {
		p.reportRejected(name, err)
		return nil, false
	}
	actual, _ := p.gauges.LoadOrStore(name, g)
	return actual.(metric.Float64Gauge), true
}

// claim admits one distinct instrument name under the limit and returns the
// declared unit, description and histogram boundaries for the instrument.
func (p *ExportPipeline) claim(name string) (unit, desc string, boundaries []float64, ok bool) {
	if p.meter == nil {
		return "", "", nil, false
	}
	if !claimName(&p.claimed, &p.instCount, instrumentLimit(p.cfg.bounds), name) {
		p.reportRejected(name, types.ErrInstrumentLimit)
		return "", "", nil, false
	}
	inst, declared := p.cfg.instruments[name]
	if !declared {
		return "", "", nil, true
	}
	return inst.Unit, inst.Description, inst.Boundaries, true
}

func (p *ExportPipeline) report(ctx context.Context, d types.ExportDiagnostic) {
	if p.cfg.diag != nil {
		p.cfg.diag.Report(ctx, d)
	}
}

func (p *ExportPipeline) reportRejected(name string, err error) {
	p.report(context.Background(), types.ExportDiagnostic{Kind: types.DiagInstrumentRejected, Name: name, Err: err})
}

// Flush force-flushes the owned providers and the sink.
func (p *ExportPipeline) Flush(ctx context.Context) (types.UnresolvedWork, error) {
	var errs []error
	if p.tp != nil {
		if err := p.tp.ForceFlush(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if p.mp != nil {
		if err := p.mp.ForceFlush(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	w, err := p.sink.Flush(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	endCycle(ctx, p.cfg.diag, false)
	return w, errors.Join(errs...)
}

// Shutdown closes the owned providers and the sink within ctx. It is
// idempotent and never touches borrowed resources.
func (p *ExportPipeline) Shutdown(ctx context.Context) (types.UnresolvedWork, error) {
	p.mu.Lock()
	if p.shutdown {
		p.mu.Unlock()
		return types.UnresolvedWork{}, nil
	}
	p.shutdown = true
	p.mu.Unlock()

	var errs []error
	if p.tp != nil {
		if err := p.tp.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if p.mp != nil {
		if err := p.mp.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	w, err := p.sink.Shutdown(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	endCycle(ctx, p.cfg.diag, true)
	return w, errors.Join(errs...)
}

type spanExporter struct {
	pipe  *ExportPipeline
	dests []types.ExportDestination
}

func (e *spanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	recs := make([]types.ExportRecord, 0, len(spans))
	for _, s := range spans {
		recs = append(recs, e.pipe.spanRecord(s))
	}
	for _, d := range e.dests {
		if err := e.pipe.sink.Ingest(ctx, d.Name(), recs); err != nil {
			e.pipe.report(ctx, types.ExportDiagnostic{Kind: types.DiagDeliveryDropped, Name: d.Name(), Err: err, Dropped: int64(len(recs))})
		}
	}
	return nil
}

func (e *spanExporter) Shutdown(context.Context) error { return nil }

func (p *ExportPipeline) spanRecord(s sdktrace.ReadOnlySpan) types.ExportRecord {
	sc := s.SpanContext()
	parent := s.Parent()
	outcome := types.SpanUnset
	switch s.Status().Code {
	case codes.Ok:
		outcome = types.SpanOK
	case codes.Error:
		outcome = types.SpanError
	}
	rec := types.ExportRecord{
		SchemaVersion: types.CurrentExportSchemaVersion,
		ID:            types.RecordID(uuid.NewV7().String()),
		Resource:      resourceIdentity(s.Resource(), p.cfg.resource),
		Signal:        types.SignalSpan,
		Span: &types.SpanRecord{
			TraceID: sc.TraceID().String(),
			SpanID:  sc.SpanID().String(),
			Name:    s.Name(),
			Start:   s.StartTime(),
			End:     s.EndTime(),
			Attrs:   kvAttrs(s.Attributes()),
			Outcome: outcome,
			Source: types.Source{
				Name:      s.InstrumentationScope().Name,
				Version:   s.InstrumentationScope().Version,
				SchemaURL: s.InstrumentationScope().SchemaURL,
			},
		},
	}
	if parent.IsValid() {
		rec.Span.ParentID = parent.SpanID().String()
	}
	return rec
}

type metricExporter struct {
	pipe *ExportPipeline
	dest types.ExportDestination
}

func (e *metricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	if e.dest.Capabilities().Temporality == types.TemporalityDelta {
		return metricdata.DeltaTemporality
	}
	return metricdata.CumulativeTemporality
}

func (e *metricExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

func (e *metricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	recs := e.pipe.metricRecords(ctx, rm)
	if len(recs) == 0 {
		return nil
	}
	if err := e.pipe.sink.Ingest(ctx, e.dest.Name(), recs); err != nil {
		e.pipe.report(ctx, types.ExportDiagnostic{Kind: types.DiagDeliveryDropped, Name: e.dest.Name(), Err: err, Dropped: int64(len(recs))})
	}
	return nil
}

func (e *metricExporter) ForceFlush(context.Context) error { return nil }
func (e *metricExporter) Shutdown(context.Context) error   { return nil }

func (p *ExportPipeline) metricRecords(ctx context.Context, rm *metricdata.ResourceMetrics) []types.ExportRecord {
	ident := resourceIdentity(rm.Resource, p.cfg.resource)
	var recs []types.ExportRecord
	for _, sm := range rm.ScopeMetrics {
		src := types.Source{Name: sm.Scope.Name, Version: sm.Scope.Version, SchemaURL: sm.Scope.SchemaURL}
		for i := range sm.Metrics {
			m := &sm.Metrics[i]
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range data.DataPoints {
					v := dp.Value
					recs = append(recs, p.metricRecord(ident, src, m, types.InstrumentCounter, wireTemporality(data.Temporality), dp.StartTime, dp.Time, dp.Attributes, types.MetricValue{Int: &v}))
				}
			case metricdata.Sum[float64]:
				for _, dp := range data.DataPoints {
					v := dp.Value
					recs = append(recs, p.metricRecord(ident, src, m, types.InstrumentCounter, wireTemporality(data.Temporality), dp.StartTime, dp.Time, dp.Attributes, types.MetricValue{Float: &v}))
				}
			case metricdata.Gauge[int64]:
				for _, dp := range data.DataPoints {
					v := dp.Value
					recs = append(recs, p.metricRecord(ident, src, m, types.InstrumentGauge, types.TemporalityCumulative, dp.StartTime, dp.Time, dp.Attributes, types.MetricValue{Int: &v}))
				}
			case metricdata.Gauge[float64]:
				for _, dp := range data.DataPoints {
					v := dp.Value
					recs = append(recs, p.metricRecord(ident, src, m, types.InstrumentGauge, types.TemporalityCumulative, dp.StartTime, dp.Time, dp.Attributes, types.MetricValue{Float: &v}))
				}
			case metricdata.Histogram[int64]:
				for _, dp := range data.DataPoints {
					recs = append(recs, p.metricRecord(ident, src, m, types.InstrumentHistogram, wireTemporality(data.Temporality), dp.StartTime, dp.Time, dp.Attributes, types.MetricValue{Histogram: histogramValue(dp)}))
				}
			case metricdata.Histogram[float64]:
				for _, dp := range data.DataPoints {
					recs = append(recs, p.metricRecord(ident, src, m, types.InstrumentHistogram, wireTemporality(data.Temporality), dp.StartTime, dp.Time, dp.Attributes, types.MetricValue{Histogram: histogramValue(dp)}))
				}
			default:
				p.report(ctx, types.ExportDiagnostic{
					Kind: types.DiagInstrumentRejected,
					Name: m.Name,
					Err:  fmt.Errorf("unsupported aggregation %T", m.Data),
				})
			}
		}
	}
	return recs
}

func (p *ExportPipeline) metricRecord(
	ident types.ResourceIdentity,
	src types.Source,
	m *metricdata.Metrics,
	kind types.InstrumentKind,
	temp types.Temporality,
	start, at time.Time,
	set attribute.Set,
	value types.MetricValue,
) types.ExportRecord {
	return types.ExportRecord{
		SchemaVersion: types.CurrentExportSchemaVersion,
		ID:            types.RecordID(uuid.NewV7().String()),
		Resource:      ident,
		Signal:        types.SignalMetric,
		Metric: &types.MetricRecord{
			Instrument:  types.Instrument{Name: m.Name, Kind: kind, Unit: m.Unit, Description: m.Description},
			Source:      src,
			Time:        at,
			Start:       start,
			Temporality: temp,
			Dimensions:  setAttrs(set),
			Value:       value,
		},
	}
}

func wireTemporality(t metricdata.Temporality) types.Temporality {
	if t == metricdata.DeltaTemporality {
		return types.TemporalityDelta
	}
	return types.TemporalityCumulative
}

func histogramValue[N int64 | float64](dp metricdata.HistogramDataPoint[N]) *types.HistogramValue {
	hv := &types.HistogramValue{
		Count:        dp.Count,
		Bounds:       dp.Bounds,
		BucketCounts: dp.BucketCounts,
		Sum:          float64(dp.Sum),
	}
	if v, ok := dp.Min.Value(); ok {
		fv := float64(v)
		hv.Min = &fv
	}
	if v, ok := dp.Max.Value(); ok {
		fv := float64(v)
		hv.Max = &fv
	}
	return hv
}

func resourceIdentity(res *resource.Resource, fallback string) types.ResourceIdentity {
	ident := types.ResourceIdentity{Name: fallback}
	if res == nil {
		return ident
	}
	for _, kv := range res.Attributes() {
		if kv.Key == "service.name" {
			ident.Name = kv.Value.AsString()
			continue
		}
		ident.Attrs = append(ident.Attrs, attrFromKV(kv))
	}
	return ident
}

func attrFromKV(kv attribute.KeyValue) types.Attr {
	a := types.Attr{Key: string(kv.Key)}
	switch kv.Value.Type() {
	case attribute.BOOL:
		a.Value = kv.Value.AsBool()
	case attribute.INT64:
		a.Value = kv.Value.AsInt64()
	case attribute.FLOAT64:
		a.Value = kv.Value.AsFloat64()
	default:
		a.Value = kv.Value.AsString()
	}
	return a
}

func kvAttrs(kvs []attribute.KeyValue) []types.Attr {
	if len(kvs) == 0 {
		return nil
	}
	attrs := make([]types.Attr, 0, len(kvs))
	for _, kv := range kvs {
		attrs = append(attrs, attrFromKV(kv))
	}
	return attrs
}

func setAttrs(set attribute.Set) []types.Attr {
	return kvAttrs(set.ToSlice())
}
