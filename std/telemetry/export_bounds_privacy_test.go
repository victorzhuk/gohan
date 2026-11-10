package telemetry_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	telemetry "github.com/victorzhuk/gohan/std/telemetry"
)

type bytesDest struct {
	name    string
	caps    types.DestinationCapabilities
	park    chan struct{}
	entered chan struct{}
	block   bool
	once    sync.Once

	mu    sync.Mutex
	calls int
	wire  [][]byte
}

func newBytesDest(name string) *bytesDest {
	return &bytesDest{
		name: name,
		caps: types.DestinationCapabilities{
			Signals:     []types.ExportSignalKind{types.SignalSpan, types.SignalMetric},
			Replay:      types.ReplayDedupByID,
			Temporality: types.TemporalityCumulative,
		},
	}
}

func newParkedDest(name string) *bytesDest {
	d := newBytesDest(name)
	d.caps.Replay = types.ReplayNone
	d.block = true
	d.park = make(chan struct{})
	d.entered = make(chan struct{})
	return d
}

func (d *bytesDest) Name() string                                { return d.name }
func (d *bytesDest) Capabilities() types.DestinationCapabilities { return d.caps }

func (d *bytesDest) Export(ctx context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	if d.block {
		d.once.Do(func() { close(d.entered) })
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("parked export: %w", ctx.Err())
		case <-d.park:
		}
	}
	out := make([][]byte, 0, len(recs))
	for _, rec := range recs {
		b, err := json.Marshal(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	d.mu.Lock()
	d.wire = append(d.wire, out...)
	d.mu.Unlock()
	res := make([]types.RecordOutcome, 0, len(recs))
	for _, rec := range recs {
		res = append(res, types.RecordOutcome{ID: rec.ID, Status: types.OutcomeAccepted})
	}
	return res, nil
}

func (d *bytesDest) serialized() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.wire))
	for _, b := range d.wire {
		out = append(out, string(b))
	}
	return out
}

func (d *bytesDest) serializedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.wire)
}

type captureDiag struct {
	mu    sync.Mutex
	repts []types.ExportDiagnostic
}

func (c *captureDiag) Report(ctx context.Context, d types.ExportDiagnostic) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.repts = append(c.repts, d)
}

func (c *captureDiag) snapshot() []types.ExportDiagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]types.ExportDiagnostic(nil), c.repts...)
}

type stubRedactor struct {
	mu     sync.Mutex
	called bool
}

func (r *stubRedactor) Redact(ctx context.Context, blocks []types.Block, _ types.RedactionPolicy) ([]types.Block, types.RedactionMap, error) {
	r.mu.Lock()
	r.called = true
	r.mu.Unlock()
	if _, ok := blocks[0].(types.Text); !ok {
		return blocks, types.RedactionMap{}, nil
	}
	return []types.Block{types.Text{Text: "[REDACTED]"}}, types.RedactionMap{}, nil
}

func (r *stubRedactor) Rehydrate(ctx context.Context, blocks []types.Block, m types.RedactionMap) ([]types.Block, error) {
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
	one := int64(1)
	return types.ExportRecord{
		SchemaVersion: types.CurrentExportSchemaVersion,
		ID:            types.RecordID(id),
		Resource:      types.ResourceIdentity{Name: "gohan"},
		Signal:        types.SignalMetric,
		Metric: &types.MetricRecord{
			Instrument:  types.Instrument{Name: "gohan.model.ttft", Kind: types.InstrumentCounter},
			Temporality: types.TemporalityCumulative,
			Dimensions:  dims,
			Value:       types.MetricValue{Int: &one},
		},
	}
}

func wireHas(wire []string, needle string) bool {
	for _, w := range wire {
		if strings.Contains(w, needle) {
			return true
		}
	}
	return false
}

func TestExportBoundsPrivacy(t *testing.T) {
	t.Run("telemetry.export-bounds-privacy", func(t *testing.T) {
		t.Run("content dropped before serialization", func(t *testing.T) {
			ctx := t.Context()
			dest := newBytesDest("mem")
			p, err := telemetry.NewPipeline([]types.ExportDestination{dest})
			if err != nil {
				t.Fatalf("NewPipeline: %v", err)
			}
			defer func() { _, _ = p.Shutdown(ctx) }()
			rec := spanRec("s1", types.String("gohan.content.input", "hunter2-secret"))
			if err := p.Ingest(ctx, dest.name, []types.ExportRecord{rec}); err != nil {
				t.Fatalf("Ingest: %v", err)
			}
			if _, err := p.Flush(ctx); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			wire := dest.serialized()
			if len(wire) == 0 {
				t.Fatal("record never serialized")
			}
			if wireHas(wire, "hunter2-secret") {
				t.Fatal("secret substring leaked into serialized output")
			}
			if wireHas(wire, "gohan.content.input") {
				t.Fatal("content key survived the ContentNone default")
			}
		})

		t.Run("redaction before serialization", func(t *testing.T) {
			ctx := t.Context()
			dest := newBytesDest("mem")
			red := &stubRedactor{}
			p, err := telemetry.NewPipeline([]types.ExportDestination{dest},
				telemetry.WithRedactor(red, types.RedactionPolicy{}),
			)
			if err != nil {
				t.Fatalf("NewPipeline: %v", err)
			}
			defer func() { _, _ = p.Shutdown(ctx) }()
			rec := spanRec("s2", types.String("user.email", "alice@example.com"))
			if err := p.Ingest(ctx, dest.name, []types.ExportRecord{rec}); err != nil {
				t.Fatalf("Ingest: %v", err)
			}
			if _, err := p.Flush(ctx); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			wire := dest.serialized()
			if dest.serializedCount() == 0 {
				t.Fatal("record was never admitted")
			}
			if wireHas(wire, "alice@example.com") {
				t.Fatal("raw PII email reached serialized output")
			}
			red.mu.Lock()
			called := red.called
			red.mu.Unlock()
			if !called {
				t.Fatal("redactor was never invoked")
			}
		})

		t.Run("forbidden identity dimensions dropped", func(t *testing.T) {
			ctx := t.Context()
			dest := newBytesDest("mem")
			p, err := telemetry.NewPipeline([]types.ExportDestination{dest})
			if err != nil {
				t.Fatalf("NewPipeline: %v", err)
			}
			defer func() { _, _ = p.Shutdown(ctx) }()
			rec := metricRec("m1",
				types.String("session_id", "sess-42"),
				types.String("gohan.session_id", "sess-42"),
				types.String("flow", "chat"),
			)
			if err := p.Ingest(ctx, dest.name, []types.ExportRecord{rec}); err != nil {
				t.Fatalf("Ingest: %v", err)
			}
			if _, err := p.Flush(ctx); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			wire := dest.serialized()
			if len(wire) == 0 {
				t.Fatal("record never serialized")
			}
			if wireHas(wire, "sess-42") || wireHas(wire, "session_id") {
				t.Fatal("identity dimension leaked into serialized output")
			}
			if !wireHas(wire, "flow") {
				t.Fatal("legitimate dimension missing from serialized output")
			}
		})

		t.Run("dimension value limit is atomic", func(t *testing.T) {
			ctx := t.Context()
			dest := newBytesDest("mem")
			diag := &captureDiag{}
			p, err := telemetry.NewPipeline([]types.ExportDestination{dest},
				telemetry.WithExportBounds(types.ExportLimits{MaxDimensionValues: 2}),
				telemetry.WithExportDiagnostics(types.BoundedDiagnostics(diag)),
			)
			if err != nil {
				t.Fatalf("NewPipeline: %v", err)
			}
			defer func() { _, _ = p.Shutdown(ctx) }()
			rec := metricRec("m2",
				types.String("region", "eu"),
				types.String("region", "us"),
				types.String("region", "ap"),
			)
			err = p.Ingest(ctx, dest.name, []types.ExportRecord{rec})
			if !errors.Is(err, types.ErrDimensionLimit) {
				t.Fatalf("Ingest err = %v, want ErrDimensionLimit", err)
			}
			if dest.serializedCount() != 0 {
				t.Fatal("refused record partially admitted")
			}
			for _, d := range diag.snapshot() {
				if d.Kind != types.DiagAdmissionRejected || d.Err == nil || !strings.Contains(d.Err.Error(), "region") {
					continue
				}
				return
			}
			t.Fatalf("diagnostics = %v, want DiagAdmissionRejected naming the key", diag.snapshot())
		})

		t.Run("dimension value limit is incremental across ingests", func(t *testing.T) {
			ctx := t.Context()
			dest := newBytesDest("mem")
			diag := &captureDiag{}
			p, err := telemetry.NewPipeline([]types.ExportDestination{dest},
				telemetry.WithExportBounds(types.ExportLimits{MaxDimensionValues: 2}),
				telemetry.WithExportDiagnostics(types.BoundedDiagnostics(diag)),
			)
			if err != nil {
				t.Fatalf("NewPipeline: %v", err)
			}
			defer func() { _, _ = p.Shutdown(ctx) }()
			if err := p.Ingest(ctx, dest.name, []types.ExportRecord{metricRec("m5", types.String("region", "eu"))}); err != nil {
				t.Fatalf("Ingest first: %v", err)
			}
			if err := p.Ingest(ctx, dest.name, []types.ExportRecord{metricRec("m6", types.String("region", "us"))}); err != nil {
				t.Fatalf("Ingest second: %v", err)
			}
			err = p.Ingest(ctx, dest.name, []types.ExportRecord{metricRec("m7", types.String("region", "ap"))})
			if !errors.Is(err, types.ErrDimensionLimit) {
				t.Fatalf("Ingest err = %v, want ErrDimensionLimit", err)
			}
			if _, err := p.Flush(ctx); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			if n := dest.serializedCount(); n != 2 {
				t.Fatalf("serialized %d records, want exactly the 2 admitted", n)
			}
			if wireHas(dest.serialized(), "m7") {
				t.Fatal("refused record was serialized")
			}
			var saw bool
			for _, d := range diag.snapshot() {
				if d.Kind == types.DiagAdmissionRejected && d.Err != nil && strings.Contains(d.Err.Error(), "region") {
					saw = true
				}
			}
			if !saw {
				t.Fatalf("diagnostics = %v, want DiagAdmissionRejected naming the key", diag.snapshot())
			}
		})

		t.Run("series limit refuses", func(t *testing.T) {
			ctx := t.Context()
			dest := newBytesDest("mem")
			diag := &captureDiag{}
			p, err := telemetry.NewPipeline([]types.ExportDestination{dest},
				telemetry.WithExportBounds(types.ExportLimits{MaxSeries: 1}),
				telemetry.WithExportDiagnostics(types.BoundedDiagnostics(diag)),
			)
			if err != nil {
				t.Fatalf("NewPipeline: %v", err)
			}
			defer func() { _, _ = p.Shutdown(ctx) }()
			first := metricRec("m3", types.String("flow", "chat"))
			if err := p.Ingest(ctx, dest.name, []types.ExportRecord{first}); err != nil {
				t.Fatalf("Ingest first: %v", err)
			}
			second := metricRec("m4", types.String("flow", "search"))
			err = p.Ingest(ctx, dest.name, []types.ExportRecord{second})
			if !errors.Is(err, types.ErrSeriesLimit) {
				t.Fatalf("Ingest err = %v, want ErrSeriesLimit", err)
			}
			var saw bool
			for _, d := range diag.snapshot() {
				if d.Kind == types.DiagAdmissionRejected {
					saw = true
				}
			}
			if !saw {
				t.Fatalf("diagnostics = %v, want DiagAdmissionRejected", diag.snapshot())
			}
		})

		t.Run("queue full on blocked destination", func(t *testing.T) {
			ctx := t.Context()
			dest := newParkedDest("parked")
			diag := &captureDiag{}
			p, err := telemetry.NewPipeline([]types.ExportDestination{dest},
				telemetry.WithExportBounds(types.ExportLimits{MaxQueueDepth: 2}),
				telemetry.WithExportDiagnostics(types.BoundedDiagnostics(diag)),
			)
			if err != nil {
				t.Fatalf("NewPipeline: %v", err)
			}
			defer func() { _, _ = p.Shutdown(ctx) }()
			for i := range 2 {
				if err := p.Ingest(ctx, dest.name, []types.ExportRecord{spanRec(fmt.Sprintf("q%d", i))}); err != nil {
					t.Fatalf("Ingest %d: %v", i, err)
				}
			}
			err = p.Ingest(ctx, dest.name, []types.ExportRecord{spanRec("q-dropped")})
			if !errors.Is(err, types.ErrExportQueueFull) {
				t.Fatalf("Ingest err = %v, want ErrExportQueueFull", err)
			}
			if wireHas(dest.serialized(), "q-dropped") {
				t.Fatal("dropped record was serialized")
			}
			var droppedDiag bool
			for _, d := range diag.snapshot() {
				if d.Kind == types.DiagDeliveryDropped {
					droppedDiag = true
				}
			}
			if !droppedDiag {
				t.Fatalf("diagnostics = %v, want DiagDeliveryDropped", diag.snapshot())
			}

			dctx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = p.Flush(dctx)
			}()
			<-dest.entered
			cancel()
			<-done
			if n := dest.serializedCount(); n != 0 {
				t.Fatalf("parked Export serialized %d records before honoring ctx", n)
			}
			close(dest.park)
		})

		t.Run("diagnostics budget bounded", func(t *testing.T) {
			ctx := t.Context()
			dest := newBytesDest("mem")
			diag := &captureDiag{}
			p, err := telemetry.NewPipeline([]types.ExportDestination{dest},
				telemetry.WithExportDiagnostics(types.BoundedDiagnostics(diag)),
			)
			if err != nil {
				t.Fatalf("NewPipeline: %v", err)
			}
			defer func() { _, _ = p.Shutdown(ctx) }()
			const flood = 100
			for i := range flood {
				rec := spanRec(fmt.Sprintf("f%d", i), types.String("gohan.content.input", "x"))
				if err := p.Ingest(ctx, dest.name, []types.ExportRecord{rec}); err != nil {
					t.Fatalf("Ingest %d: %v", i, err)
				}
			}
			key := func(d types.ExportDiagnostic) bool {
				return d.Kind == types.DiagAdmissionRejected && d.Name == "gohan.content.input"
			}
			mid := 0
			for _, d := range diag.snapshot() {
				if key(d) {
					mid++
				}
			}
			if mid > 1 {
				t.Fatalf("%d Reports before Flush, want at most 1", mid)
			}
			if n := dest.serializedCount(); n != 0 {
				t.Fatalf("%d records serialized before Flush, diagnostics must not trigger delivery", n)
			}
			if _, err := p.Flush(ctx); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			total := 0
			var final types.ExportDiagnostic
			for _, d := range diag.snapshot() {
				if key(d) {
					total++
					final = d
				}
			}
			if total > 2 {
				t.Fatalf("%d Reports across the cycle, want at most 2", total)
			}
			if final.Dropped != flood {
				t.Fatalf("final Report Dropped = %d, want %d", final.Dropped, flood)
			}
			if n := dest.serializedCount(); n != flood {
				t.Fatalf("%d records serialized after Flush, want exactly %d (diagnostic recursion would exceed it)", n, flood)
			}
		})
	})
}
