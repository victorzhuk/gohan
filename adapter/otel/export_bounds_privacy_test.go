package otel_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/adapter/otel"
	"github.com/victorzhuk/gohan/core/types"
)

// regSink is the minimal RecordSink fake: it stores ingested records per
// destination and resolves every flush and shutdown as complete.
type regSink struct {
	mu    sync.Mutex
	dests map[string]types.ExportDestination
	recs  map[string][]types.ExportRecord
}

func (s *regSink) Ingest(_ context.Context, dest string, recs []types.ExportRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dests[dest]; !ok {
		return fmt.Errorf("ingest %q: %w", dest, types.ErrUnknownDestination)
	}
	s.recs[dest] = append(s.recs[dest], recs...)
	return nil
}

func (s *regSink) Flush(context.Context) (types.UnresolvedWork, error) {
	return types.UnresolvedWork{}, nil
}

func (s *regSink) Shutdown(context.Context) (types.UnresolvedWork, error) {
	return types.UnresolvedWork{}, nil
}

func (s *regSink) names(dest string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.recs[dest] {
		if r.Metric != nil {
			out = append(out, r.Metric.Instrument.Name)
		}
	}
	return out
}

func TestExportInstrumentRegistration(t *testing.T) {
	t.Run("telemetry.export-bounds-privacy", func(t *testing.T) {
		ctx := t.Context()
		diags := &fidDiags{}
		dest := newFidDest("reg", fidCaps())
		sink := &regSink{
			dests: map[string]types.ExportDestination{dest.Name(): dest},
			recs:  map[string][]types.ExportRecord{},
		}
		pipe, err := otel.NewExportPipeline(sink, []types.ExportDestination{dest}, otel.WithDiagnostics(types.BoundedDiagnostics(diags)))
		if err != nil {
			t.Fatalf("NewExportPipeline: %v", err)
		}
		t.Cleanup(func() { _, _ = pipe.Shutdown(context.Background()) })

		rejected := fmt.Sprintf("reg.counter.%03d", types.DefaultMaxInstruments)
		for i := 0; i < types.DefaultMaxInstruments; i++ {
			pipe.Count(ctx, fmt.Sprintf("reg.counter.%03d", i), 1)
		}
		for i := 0; i < 3; i++ {
			pipe.Count(ctx, rejected, 1)
		}

		w, err := pipe.Flush(ctx)
		if err != nil {
			t.Fatalf("Flush: %v", err)
		}
		if w.Records != 0 || len(w.ByDestination) != 0 {
			t.Fatalf("Flush left unresolved work %+v, want none", w)
		}

		seen := make(map[string]int)
		for _, name := range sink.names(dest.Name()) {
			seen[name]++
			if name == rejected {
				t.Fatalf("rejected instrument %q exported a metric record", rejected)
			}
		}
		if seen[fmt.Sprintf("reg.counter.%03d", types.DefaultMaxInstruments-1)] != 1 {
			t.Fatalf("bounded predecessor instrument missing from export: %d records", seen[fmt.Sprintf("reg.counter.%03d", types.DefaultMaxInstruments-1)])
		}

		reports := diags.ofKind(types.DiagInstrumentRejected)
		var named []types.ExportDiagnostic
		for _, d := range reports {
			if d.Name == rejected {
				named = append(named, d)
			}
		}
		if len(named) != 2 {
			t.Fatalf("%d diagnostics for %q, want exactly the first-rejection report and the cycle-end report", len(named), rejected)
		}
		if !errors.Is(named[0].Err, types.ErrInstrumentLimit) {
			t.Fatalf("rejection err %v, want wrapping ErrInstrumentLimit", named[0].Err)
		}
		if named[1].Dropped != 3 {
			t.Fatalf("cycle-end report Dropped=%d, want 3 rejections in the cycle", named[1].Dropped)
		}
	})
}
