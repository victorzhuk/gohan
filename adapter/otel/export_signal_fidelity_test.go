package otel_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/victorzhuk/gohan/adapter/otel"
	"github.com/victorzhuk/gohan/core/types"
)

// fidDiags captures export diagnostics reported by a pipeline or by the
// delivery seam the sink fixture simulates.
type fidDiags struct {
	mu  sync.Mutex
	got []types.ExportDiagnostic
}

func (d *fidDiags) Report(_ context.Context, diag types.ExportDiagnostic) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.got = append(d.got, diag)
}

func (d *fidDiags) ofKind(k types.DiagnosticKind) []types.ExportDiagnostic {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []types.ExportDiagnostic
	for _, diag := range d.got {
		if diag.Kind == k {
			out = append(out, diag)
		}
	}
	return out
}

// fidDest is an in-memory ExportDestination that captures every batch it
// receives. An optional script overrides the returned outcomes.
type fidDest struct {
	name   string
	caps   types.DestinationCapabilities
	mu     sync.Mutex
	got    [][]types.ExportRecord
	script func(recs []types.ExportRecord) []types.RecordOutcome
}

func newFidDest(name string, caps types.DestinationCapabilities) *fidDest {
	return &fidDest{name: name, caps: caps}
}

func (d *fidDest) Name() string { return d.name }

func (d *fidDest) Capabilities() types.DestinationCapabilities { return d.caps }

func (d *fidDest) Export(_ context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.got = append(d.got, recs)
	if d.script != nil {
		return d.script(recs), nil
	}
	out := make([]types.RecordOutcome, len(recs))
	for i, r := range recs {
		out[i] = types.RecordOutcome{ID: r.ID, Status: types.OutcomeAccepted}
	}
	return out, nil
}

func (d *fidDest) batches() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.got)
}

func (d *fidDest) records() []types.ExportRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []types.ExportRecord
	for _, batch := range d.got {
		out = append(out, batch...)
	}
	return out
}

// fidSink is the delivery seam: it queues translated records per destination
// and drains them through each destination's Export at Flush or Shutdown,
// applying the shared outcome vocabulary. Acknowledged records advance;
// everything else stays queued with its ID unchanged.
type fidSink struct {
	diags  types.Diagnostics
	mu     sync.Mutex
	dests  map[string]types.ExportDestination
	queues map[string][]types.ExportRecord
}

func newFidSink(diags types.Diagnostics, dests ...types.ExportDestination) *fidSink {
	s := &fidSink{
		diags:  diags,
		dests:  make(map[string]types.ExportDestination, len(dests)),
		queues: make(map[string][]types.ExportRecord, len(dests)),
	}
	for _, d := range dests {
		s.dests[d.Name()] = d
	}
	return s
}

func (s *fidSink) Ingest(_ context.Context, dest string, recs []types.ExportRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dests[dest]; !ok {
		return fmt.Errorf("ingest %q: %w", dest, types.ErrUnknownDestination)
	}
	s.queues[dest] = append(s.queues[dest], recs...)
	return nil
}

func (s *fidSink) Flush(ctx context.Context) (types.UnresolvedWork, error) {
	return s.deliver(ctx)
}

func (s *fidSink) Shutdown(ctx context.Context) (types.UnresolvedWork, error) {
	return s.deliver(ctx)
}

func (s *fidSink) deliver(ctx context.Context) (types.UnresolvedWork, error) {
	w := types.UnresolvedWork{ByDestination: map[string]int64{}}
	var errs []error
	for name, dest := range s.dests {
		s.mu.Lock()
		q := s.queues[name]
		s.queues[name] = nil
		s.mu.Unlock()
		if len(q) == 0 {
			continue
		}
		outcomes, err := dest.Export(ctx, q)
		if err != nil {
			errs = append(errs, &types.ExportError{Destination: name, Err: err})
		}
		acked := make(map[types.RecordID]bool, len(outcomes))
		for _, o := range outcomes {
			switch o.Status {
			case types.OutcomeAccepted, types.OutcomePermanent, types.OutcomeAmbiguous:
				acked[o.ID] = true
			}
			if o.Status == types.OutcomeAmbiguous && s.diags != nil {
				s.diags.Report(ctx, types.ExportDiagnostic{Kind: types.DiagDeliveryAmbiguous, Name: name})
			}
		}
		for _, r := range q {
			if acked[r.ID] {
				continue
			}
			s.mu.Lock()
			s.queues[name] = append(s.queues[name], r)
			s.mu.Unlock()
			w.Records++
			w.ByDestination[name]++
		}
	}
	return w, errors.Join(errs...)
}

func fidCaps() types.DestinationCapabilities {
	return types.DestinationCapabilities{
		Signals:     []types.ExportSignalKind{types.SignalSpan, types.SignalMetric},
		Replay:      types.ReplayDedupByID,
		Temporality: types.TemporalityCumulative,
	}
}

func fidPipeline(t *testing.T, sink types.RecordSink, dests []types.ExportDestination, opts ...otel.Option) *otel.ExportPipeline {
	t.Helper()
	pipe, err := otel.NewExportPipeline(sink, dests, opts...)
	if err != nil {
		t.Fatalf("NewExportPipeline: %v", err)
	}
	t.Cleanup(func() { _, _ = pipe.Shutdown(context.Background()) })
	return pipe
}

func fidSpanRecords(t *testing.T, d *fidDest) map[string]types.SpanRecord {
	t.Helper()
	out := map[string]types.SpanRecord{}
	for _, r := range d.records() {
		if r.Signal != types.SignalSpan || r.Span == nil {
			t.Fatalf("destination %q: record %s carries signal %q without span payload", d.Name(), r.ID, r.Signal)
		}
		if _, dup := out[r.Span.Name]; dup {
			t.Fatalf("destination %q: duplicate span record for %q", d.Name(), r.Span.Name)
		}
		out[r.Span.Name] = *r.Span
	}
	return out
}

func fidMetricRecords(t *testing.T, d *fidDest) map[string]types.MetricRecord {
	t.Helper()
	out := map[string]types.MetricRecord{}
	for _, r := range d.records() {
		if r.Signal != types.SignalMetric || r.Metric == nil {
			t.Fatalf("destination %q: record %s carries signal %q without metric payload", d.Name(), r.ID, r.Signal)
		}
		if _, dup := out[r.Metric.Instrument.Name]; dup {
			t.Fatalf("destination %q: duplicate metric record for %q in one cycle", d.Name(), r.Metric.Instrument.Name)
		}
		out[r.Metric.Instrument.Name] = *r.Metric
	}
	return out
}

func fidInWindow(t *testing.T, at, lo, hi time.Time, label string) {
	t.Helper()
	if at.Before(lo) || at.After(hi) {
		t.Fatalf("%s timestamp %v outside emission window [%v, %v]", label, at, lo, hi)
	}
}

func fidRoundTrip(t *testing.T, rec types.ExportRecord) types.ExportRecord {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal export record: %v", err)
	}
	var back types.ExportRecord
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal export record: %v", err)
	}
	// JSON has no monotonic clock reading; strip it before comparing.
	sent, got := rec, back
	if sent.Span != nil {
		sent.Span.Start, sent.Span.End = sent.Span.Start.Round(0), sent.Span.End.Round(0)
		got.Span.Start, got.Span.End = got.Span.Start.Round(0), got.Span.End.Round(0)
	}
	if sent.Metric != nil {
		sent.Metric.Time, sent.Metric.Start = sent.Metric.Time.Round(0), sent.Metric.Start.Round(0)
		got.Metric.Time, got.Metric.Start = got.Metric.Time.Round(0), got.Metric.Start.Round(0)
	}
	if !reflect.DeepEqual(sent, got) {
		t.Fatalf("serialized form lost data:\n sent: %#v\n got:  %#v", sent, got)
	}
	return back
}

func TestExportSignalFidelity(t *testing.T) {
	t.Run("telemetry.export-signal-fidelity", func(t *testing.T) {
		ctx := t.Context()

		t.Run("ancestry timestamps and outcome survive export", func(t *testing.T) {
			destA := newFidDest("fid-a", fidCaps())
			destB := newFidDest("fid-b", fidCaps())
			sink := newFidSink(&fidDiags{}, destA, destB)
			pipe := fidPipeline(t, sink, []types.ExportDestination{destA, destB}, otel.WithResourceName("fid-resource"))

			parentStart := time.Now()
			spanCtx, endParent := pipe.StartSpan(ctx, "fid.parent", types.Attr{Key: "stage", Value: "outer"})
			parentStarted := time.Now()
			childStart := time.Now()
			childCtx, endChild := pipe.StartSpan(spanCtx, "fid.child")
			childStarted := time.Now()
			if s := trace.SpanFromContext(childCtx); s.IsRecording() {
				s.SetStatus(codes.Error, "synthetic")
			}
			childEnd := time.Now()
			endChild(types.Attr{Key: "stage", Value: "inner"})
			childEnded := time.Now()
			parentEnd := time.Now()
			endParent()
			parentEnded := time.Now()

			if _, err := pipe.Flush(ctx); err != nil {
				t.Fatalf("Flush: %v", err)
			}

			for _, dest := range []*fidDest{destA, destB} {
				spans := fidSpanRecords(t, dest)
				if len(spans) != 2 {
					t.Fatalf("destination %q: %d span records, want 2", dest.Name(), len(spans))
				}
				parent, ok := spans["fid.parent"]
				if !ok {
					t.Fatalf("destination %q: parent span missing", dest.Name())
				}
				child, ok := spans["fid.child"]
				if !ok {
					t.Fatalf("destination %q: child span missing", dest.Name())
				}
				if parent.TraceID == "" || child.TraceID != parent.TraceID {
					t.Fatalf("destination %q: trace ids diverge: parent %q child %q", dest.Name(), parent.TraceID, child.TraceID)
				}
				if child.ParentID != parent.SpanID {
					t.Fatalf("destination %q: child parent_id %q, want parent span_id %q", dest.Name(), child.ParentID, parent.SpanID)
				}
				if parent.Outcome != types.SpanUnset {
					t.Fatalf("destination %q: parent outcome %q, want unset", dest.Name(), parent.Outcome)
				}
				if child.Outcome != types.SpanError {
					t.Fatalf("destination %q: child outcome %q, want error", dest.Name(), child.Outcome)
				}
				fidInWindow(t, parent.Start, parentStart, parentStarted, "parent start")
				fidInWindow(t, parent.End, parentEnd, parentEnded, "parent end")
				fidInWindow(t, child.Start, childStart, childStarted, "child start")
				fidInWindow(t, child.End, childEnd, childEnded, "child end")
				if len(child.Attrs) == 0 || child.Attrs[0].Key != "stage" || child.Attrs[0].Value != "inner" {
					t.Fatalf("destination %q: child attrs %v, want late attr stage=inner", dest.Name(), child.Attrs)
				}
			}

			src := fidSpanRecords(t, destA)["fid.parent"].Source
			const scopeName = "github.com/victorzhuk/gohan"
			if src.Name != scopeName {
				t.Fatalf("span record source name %q, want instrumentation scope %q", src.Name, scopeName)
			}
			if src.SchemaURL != "" || src.Version != "" {
				t.Fatalf("span record source carries schema_url %q version %q, want unset: pipeline scope declares neither", src.SchemaURL, src.Version)
			}

			recsA, recsB := destA.records(), destB.records()
			if len(recsA) != len(recsB) {
				t.Fatalf("destinations diverge: %d records vs %d records", len(recsA), len(recsB))
			}
			for i := range recsA {
				if recsA[i].ID != recsB[i].ID || !reflect.DeepEqual(recsA[i], recsB[i]) {
					t.Fatalf("destinations diverge at record %d: %#v vs %#v", i, recsA[i], recsB[i])
				}
				fidRoundTrip(t, recsA[i])
			}
		})

		t.Run("session id survives export on span record", func(t *testing.T) {
			dest := newFidDest("fid-sid", fidCaps())
			sink := newFidSink(&fidDiags{}, dest)
			pipe := fidPipeline(t, sink, []types.ExportDestination{dest})

			_, end := pipe.StartSpan(ctx, "fid.session",
				types.String(types.KeySessionID, "s-42"),
			)
			end(types.String(types.KeyToolOutcome, "allow"))
			if _, err := pipe.Flush(ctx); err != nil {
				t.Fatalf("Flush: %v", err)
			}

			span, ok := fidSpanRecords(t, dest)["fid.session"]
			if !ok {
				t.Fatalf("span record fid.session missing")
			}
			for _, want := range []types.Attr{
				{Key: types.KeySessionID, Value: "s-42"},
				{Key: types.KeyToolOutcome, Value: "allow"},
			} {
				found := false
				for _, a := range span.Attrs {
					if a.Key == want.Key {
						found = true
						if a.Value != want.Value {
							t.Fatalf("span record %s = %v, want %v", want.Key, a.Value, want.Value)
						}
					}
				}
				if !found {
					t.Fatalf("span record lost attribute %s; attrs %v", want.Key, span.Attrs)
				}
			}
		})

		t.Run("unended span produces no record", func(t *testing.T) {
			dest := newFidDest("fid-open", fidCaps())
			sink := newFidSink(&fidDiags{}, dest)
			pipe := fidPipeline(t, sink, []types.ExportDestination{dest})

			pipe.StartSpan(ctx, "fid.never-ended")
			if _, err := pipe.Flush(ctx); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			if recs := dest.records(); len(recs) != 0 {
				t.Fatalf("unended span produced %d records, want 0", len(recs))
			}
		})

		t.Run("declared instruments keep semantics", func(t *testing.T) {
			insts := []types.Instrument{
				{Name: "fid.counter", Kind: types.InstrumentCounter, Unit: "ms", Description: "synthetic counter"},
				{Name: "fid.histogram", Kind: types.InstrumentHistogram, Unit: "ms", Description: "synthetic histogram", Boundaries: []float64{5, 10, 25}},
				{Name: "fid.gauge", Kind: types.InstrumentGauge, Unit: "ms", Description: "synthetic gauge"},
			}
			destA := newFidDest("fid-ma", fidCaps())
			destB := newFidDest("fid-mb", fidCaps())
			sink := newFidSink(&fidDiags{}, destA, destB)
			pipe := fidPipeline(t, sink, []types.ExportDestination{destA, destB}, otel.WithInstruments(insts...))

			pipe.Count(ctx, "fid.counter", 7)
			pipe.Record(ctx, "fid.histogram", 8)
			pipe.Gauge(ctx, "fid.gauge", 3.5)
			if _, err := pipe.Flush(ctx); err != nil {
				t.Fatalf("Flush: %v", err)
			}

			for _, dest := range []*fidDest{destA, destB} {
				recs := dest.records()
				metrics := fidMetricRecords(t, dest)
				if len(recs) != 3 || len(metrics) != 3 {
					t.Fatalf("destination %q: %d records (%d metric), want one record per data point per instrument", dest.Name(), len(recs), len(metrics))
				}

				counter, ok := metrics["fid.counter"]
				if !ok {
					t.Fatalf("destination %q: counter record missing", dest.Name())
				}
				if counter.Instrument.Kind != types.InstrumentCounter || counter.Instrument.Unit != "ms" || counter.Instrument.Description != "synthetic counter" {
					t.Fatalf("destination %q: counter instrument %+v, want kind counter unit ms description preserved", dest.Name(), counter.Instrument)
				}
				if counter.Temporality != types.TemporalityCumulative {
					t.Fatalf("destination %q: counter temporality %q, want declared cumulative", dest.Name(), counter.Temporality)
				}
				if counter.Value.Int == nil || *counter.Value.Int != 7 || counter.Value.Float != nil || counter.Value.Histogram != nil {
					t.Fatalf("destination %q: counter value %+v, want int arm 7", dest.Name(), counter.Value)
				}

				hist, ok := metrics["fid.histogram"]
				if !ok {
					t.Fatalf("destination %q: histogram record missing", dest.Name())
				}
				if hist.Instrument.Kind != types.InstrumentHistogram || hist.Instrument.Unit != "ms" || hist.Instrument.Description != "synthetic histogram" {
					t.Fatalf("destination %q: histogram instrument %+v, want kind histogram unit ms description preserved", dest.Name(), hist.Instrument)
				}
				if hist.Temporality != types.TemporalityCumulative {
					t.Fatalf("destination %q: histogram temporality %q, want declared cumulative", dest.Name(), hist.Temporality)
				}
				hv := hist.Value.Histogram
				if hv == nil || hv.Count != 1 || hv.Sum != 8 {
					t.Fatalf("destination %q: histogram value %+v, want one observation summing 8", dest.Name(), hist.Value)
				}
				wantBounds := []float64{5, 10, 25}
				if !reflect.DeepEqual(hv.Bounds, wantBounds) {
					t.Fatalf("destination %q: histogram bounds %v, want declared %v with %d bucket counts, got %d", dest.Name(), hv.Bounds, wantBounds, len(wantBounds)+1, len(hv.BucketCounts))
				}
				if len(hv.BucketCounts) != len(hv.Bounds)+1 {
					t.Fatalf("destination %q: %d bucket counts for %d bounds, want %d", dest.Name(), len(hv.BucketCounts), len(hv.Bounds), len(hv.Bounds)+1)
				}

				gauge, ok := metrics["fid.gauge"]
				if !ok {
					t.Fatalf("destination %q: gauge record missing", dest.Name())
				}
				if gauge.Instrument.Kind != types.InstrumentGauge || gauge.Instrument.Unit != "ms" || gauge.Instrument.Description != "synthetic gauge" {
					t.Fatalf("destination %q: gauge instrument %+v, want kind gauge unit ms description preserved", dest.Name(), gauge.Instrument)
				}
				if gauge.Temporality != types.TemporalityCumulative {
					t.Fatalf("destination %q: gauge temporality %q, want cumulative", dest.Name(), gauge.Temporality)
				}
				if gauge.Value.Float == nil || *gauge.Value.Float != 3.5 || gauge.Value.Int != nil || gauge.Value.Histogram != nil {
					t.Fatalf("destination %q: gauge value %+v, want float arm 3.5", dest.Name(), gauge.Value)
				}
			}
		})

		t.Run("non-monotonic boundaries rejected at construction", func(t *testing.T) {
			dest := newFidDest("fid-bad-bounds", fidCaps())
			sink := newFidSink(&fidDiags{}, dest)
			_, err := otel.NewExportPipeline(sink, []types.ExportDestination{dest}, otel.WithInstruments(types.Instrument{
				Name:       "fid.bad.bounds",
				Kind:       types.InstrumentHistogram,
				Unit:       "ms",
				Boundaries: []float64{10, 5},
			}))
			if err == nil {
				t.Fatal("NewExportPipeline accepted non-monotonic boundaries, want construction error")
			}
			if !strings.Contains(err.Error(), "fid.bad.bounds") || !strings.Contains(err.Error(), "non-monotonic boundaries") {
				t.Fatalf("construction err %v, want instrument name with non-monotonic boundaries", err)
			}
			if recs := dest.records(); len(recs) != 0 {
				t.Fatalf("rejected configuration delivered %d records, want zero admission", len(recs))
			}
		})

		t.Run("conflicting instrument redeclaration rejected at construction", func(t *testing.T) {
			dest := newFidDest("fid-conflict", fidCaps())
			sink := newFidSink(&fidDiags{}, dest)
			base := types.Instrument{Name: "fid.dupe", Kind: types.InstrumentCounter, Unit: "ms", Description: "first"}
			if _, err := otel.NewExportPipeline(sink, []types.ExportDestination{dest},
				otel.WithInstruments(base),
				otel.WithInstruments(types.Instrument{Name: "fid.dupe", Kind: types.InstrumentCounter, Unit: "ms", Description: "other"}),
			); !errors.Is(err, types.ErrInstrumentConflict) {
				t.Fatalf("NewExportPipeline err %v, want wrapping types.ErrInstrumentConflict", err)
			}
			if recs := dest.records(); len(recs) != 0 {
				t.Fatalf("rejected configuration delivered %d records, want zero admission", len(recs))
			}

			if _, err := otel.NewExportPipeline(sink, []types.ExportDestination{dest},
				otel.WithInstruments(base),
				otel.WithInstruments(base),
			); err != nil {
				t.Fatalf("byte-identical redeclaration rejected: %v, want idempotent construction", err)
			}
		})

		t.Run("span-only destination rejected before admission", func(t *testing.T) {
			caps := fidCaps()
			caps.Signals = []types.ExportSignalKind{types.SignalSpan}
			dest := newFidDest("fid-span-only", caps)
			sink := newFidSink(&fidDiags{}, dest)

			if _, err := otel.NewExportPipeline(sink, []types.ExportDestination{dest}); !errors.Is(err, types.ErrExportUnsupportedSignal) {
				t.Fatalf("NewExportPipeline err %v, want wrapping ErrExportUnsupportedSignal", err)
			}
			if recs := dest.records(); len(recs) != 0 {
				t.Fatalf("rejected configuration delivered %d records, want zero admission", len(recs))
			}
		})

		t.Run("ambiguous outcome advances record and reports", func(t *testing.T) {
			dest := newFidDest("fid-amb", fidCaps())
			diags := &fidDiags{}
			sink := newFidSink(diags, dest)
			dest.script = func(recs []types.ExportRecord) []types.RecordOutcome {
				out := make([]types.RecordOutcome, len(recs))
				for i, r := range recs {
					out[i] = types.RecordOutcome{ID: r.ID, Status: types.OutcomeAccepted}
				}
				out[0] = types.RecordOutcome{ID: recs[0].ID, Status: types.OutcomeAmbiguous}
				return out
			}
			pipe := fidPipeline(t, sink, []types.ExportDestination{dest})

			_, end := pipe.StartSpan(ctx, "fid.ambiguous")
			end()
			w, err := pipe.Flush(ctx)
			if err != nil {
				t.Fatalf("Flush: %v", err)
			}
			if w.Records != 0 || len(w.ByDestination) != 0 {
				t.Fatalf("ambiguous record unresolved: %+v, want record advanced", w)
			}
			amb := diags.ofKind(types.DiagDeliveryAmbiguous)
			if len(amb) != 1 || amb[0].Name != dest.Name() {
				t.Fatalf("DiagDeliveryAmbiguous reports %+v, want exactly one naming %q", amb, dest.Name())
			}
			before := dest.batches()
			if _, err := pipe.Flush(ctx); err != nil {
				t.Fatalf("second Flush: %v", err)
			}
			if after := dest.batches(); after != before {
				t.Fatalf("advanced record re-sent: %d deliveries after drain, want %d", after, before)
			}
		})
	})
}
