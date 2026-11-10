package otel_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/victorzhuk/gohan/adapter/otel"
	"github.com/victorzhuk/gohan/core/types"
)

// blockingDestination parks every Export call on ctx cancellation.
type blockingDestination struct {
	name string
}

func newBlockingDestination(name string) *blockingDestination {
	return &blockingDestination{name: name}
}

func (d *blockingDestination) Name() string { return d.name }

func (d *blockingDestination) Capabilities() types.DestinationCapabilities {
	return types.DestinationCapabilities{
		Signals:     []types.ExportSignalKind{types.SignalSpan, types.SignalMetric},
		Replay:      types.ReplayDedupByID,
		Temporality: types.TemporalityCumulative,
	}
}

func (d *blockingDestination) Export(ctx context.Context, _ []types.ExportRecord) ([]types.RecordOutcome, error) {
	<-ctx.Done()
	return nil, fmt.Errorf("export to %q: %w", d.name, ctx.Err())
}

// ackDestination acknowledges every record and honors ctx.
type ackDestination struct {
	name string
}

func (d *ackDestination) Name() string { return d.name }

func (d *ackDestination) Capabilities() types.DestinationCapabilities {
	return types.DestinationCapabilities{
		Signals:     []types.ExportSignalKind{types.SignalSpan, types.SignalMetric},
		Replay:      types.ReplayDedupByID,
		Temporality: types.TemporalityCumulative,
	}
}

func (d *ackDestination) Export(ctx context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("export to %q: %w", d.name, err)
	}
	out := make([]types.RecordOutcome, len(recs))
	for i, r := range recs {
		out[i] = types.RecordOutcome{ID: r.ID, Status: types.OutcomeAccepted}
	}
	return out, nil
}

// memorySink holds records per destination until a Flush or Shutdown drains
// them through each destination's Export.
type memorySink struct {
	mu       sync.Mutex
	dests    map[string]types.ExportDestination
	queues   map[string][]types.ExportRecord
	shutdown bool
}

func newMemorySink(dests ...types.ExportDestination) *memorySink {
	s := &memorySink{
		dests:  make(map[string]types.ExportDestination, len(dests)),
		queues: make(map[string][]types.ExportRecord, len(dests)),
	}
	for _, d := range dests {
		s.dests[d.Name()] = d
	}
	return s
}

func (s *memorySink) Ingest(_ context.Context, dest string, recs []types.ExportRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return fmt.Errorf("ingest %q: %w", dest, types.ErrExportShutdown)
	}
	if _, ok := s.dests[dest]; !ok {
		return fmt.Errorf("ingest %q: %w", dest, types.ErrUnknownDestination)
	}
	s.queues[dest] = append(s.queues[dest], recs...)
	return nil
}

func (s *memorySink) Flush(ctx context.Context) (types.UnresolvedWork, error) {
	return s.drain(ctx)
}

func (s *memorySink) Shutdown(ctx context.Context) (types.UnresolvedWork, error) {
	s.mu.Lock()
	s.shutdown = true
	s.mu.Unlock()
	return s.drain(ctx)
}

func (s *memorySink) drain(ctx context.Context) (types.UnresolvedWork, error) {
	var errs []error
	w := types.UnresolvedWork{ByDestination: map[string]int64{}}
	for name, dest := range s.dests {
		s.mu.Lock()
		q := s.queues[name]
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
		}
		remaining := int64(0)
		for _, r := range q {
			if !acked[r.ID] {
				remaining++
			}
		}
		if remaining > 0 {
			w.Records += remaining
			w.ByDestination[name] += remaining
		}
	}
	return w, errors.Join(errs...)
}

func exportRecord(id string) types.ExportRecord {
	return types.ExportRecord{
		SchemaVersion: types.CurrentExportSchemaVersion,
		ID:            types.RecordID(id),
		Resource:      types.ResourceIdentity{Name: "gohan"},
		Signal:        types.SignalSpan,
		Span:          &types.SpanRecord{Name: "chat", Outcome: types.SpanOK},
	}
}

func hasMetric(rm metricdata.ResourceMetrics, name string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return true
			}
		}
	}
	return false
}

func settleGoroutines(baseline int) int {
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline+1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	return runtime.NumGoroutine()
}

func TestExportLifecycle(t *testing.T) {
	t.Run("telemetry.export-lifecycle", func(t *testing.T) {
		blocked := newBlockingDestination("blocked")
		sink := newMemorySink(blocked)
		baseline := runtime.NumGoroutine()

		ep, err := otel.NewExportPipeline(sink, []types.ExportDestination{blocked})
		if err != nil {
			t.Fatalf("new export pipeline: %v", err)
		}

		const n = 3
		recs := make([]types.ExportRecord, n)
		for i := range recs {
			recs[i] = exportRecord(fmt.Sprintf("r%d", i))
		}
		if err := sink.Ingest(t.Context(), "blocked", recs); err != nil {
			t.Fatalf("ingest: %v", err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		start := time.Now()
		w, err := ep.Shutdown(ctx)
		cancel()
		if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
			t.Fatalf("shutdown took %v, want under deadline+100ms grace", elapsed)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown err %v, want context.DeadlineExceeded", err)
		}
		if w.Records != n || w.ByDestination["blocked"] != n {
			t.Fatalf("unresolved %+v, want %d records on blocked", w, n)
		}

		if got := settleGoroutines(baseline); got > baseline {
			t.Fatalf("goroutines %d exceed baseline %d after shutdown", got, baseline)
		}

		w2, err := ep.Shutdown(t.Context())
		if err != nil {
			t.Fatalf("second shutdown err %v, want nil", err)
		}
		if w2.Records != 0 {
			t.Fatalf("second shutdown unresolved %+v, want zero", w2)
		}
		if err := sink.Ingest(t.Context(), "blocked", []types.ExportRecord{exportRecord("late")}); !errors.Is(err, types.ErrExportShutdown) {
			t.Fatalf("ingest after shutdown err %v, want ErrExportShutdown", err)
		}

		tel, sr, mr := newTelemetry(t)
		var port types.Telemetry = tel
		if _, ok := port.(types.TelemetryLifecycle); ok {
			t.Fatal("borrowed *otel.Telemetry implements TelemetryLifecycle")
		}
		_, end := tel.StartSpan(context.Background(), "after.closure")
		end()
		if findSpan(sr.Ended(), "after.closure") == nil {
			t.Fatal("borrowed provider closed: span after closure attempt not recorded")
		}
		tel.Count(context.Background(), MetricRunRecovered, 1)
		var rm metricdata.ResourceMetrics
		if err := mr.Collect(context.Background(), &rm); err != nil {
			t.Fatalf("collect on borrowed meter: %v", err)
		}
		if !hasMetric(rm, MetricRunRecovered) {
			t.Fatal("borrowed meter closed: metric not collected after closure attempt")
		}

		healthy := &ackDestination{name: "healthy"}
		sink2 := newMemorySink(healthy)
		ep2, err := otel.NewExportPipeline(sink2, []types.ExportDestination{healthy})
		if err != nil {
			t.Fatalf("second export pipeline: %v", err)
		}
		flushRecs := []types.ExportRecord{exportRecord("f1"), exportRecord("f2")}
		if err := sink2.Ingest(t.Context(), "healthy", flushRecs); err != nil {
			t.Fatalf("ingest: %v", err)
		}
		fctx, fcancel := context.WithCancel(t.Context())
		fcancel()
		w3, err := ep2.Flush(fctx)
		fcancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled flush err %v, want context.Canceled", err)
		}
		if w3.Records != 2 {
			t.Fatalf("cancelled flush unresolved %+v, want 2 records", w3)
		}
		if _, err := ep2.Shutdown(t.Context()); err != nil {
			t.Fatalf("shutdown after cancelled flush: %v", err)
		}
		if got := settleGoroutines(baseline); got > baseline {
			t.Fatalf("goroutines %d exceed baseline %d after cancelled flush", got, baseline)
		}
	})
}
