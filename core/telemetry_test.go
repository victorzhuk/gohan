package gohan

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type telCtxKey struct{}

type spanRec struct {
	name   string
	parent string
	attrs  []types.Attr
	ended  bool
}

type countRec struct {
	name  string
	n     int64
	attrs []types.Attr
}

type fakeTelemetry struct {
	spans  []*spanRec
	counts []countRec
}

func (f *fakeTelemetry) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	rec := &spanRec{name: name, attrs: attrs}
	if p, ok := ctx.Value(telCtxKey{}).(*spanRec); ok {
		rec.parent = p.name
	}
	f.spans = append(f.spans, rec)
	return context.WithValue(ctx, telCtxKey{}, rec), func(...types.Attr) { rec.ended = true }
}

func (f *fakeTelemetry) Count(_ context.Context, name string, n int64, attrs ...types.Attr) {
	f.counts = append(f.counts, countRec{name: name, n: n, attrs: attrs})
}

func (f *fakeTelemetry) Record(context.Context, string, float64, ...types.Attr) {}

func (f *fakeTelemetry) span(t *testing.T, name string) *spanRec {
	t.Helper()
	for _, s := range f.spans {
		if s.name == name {
			return s
		}
	}
	t.Fatalf("no %q span in %v", name, f.spanNames())
	return nil
}

func (f *fakeTelemetry) spanNames() []string {
	names := make([]string, 0, len(f.spans))
	for _, s := range f.spans {
		names = append(names, s.name)
	}
	return names
}

func (f *fakeTelemetry) attr(t *testing.T, s *spanRec, key string) types.Attr {
	t.Helper()
	for _, a := range s.attrs {
		if a.Key == key {
			return a
		}
	}
	t.Fatalf("span %q has no %s attr", s.name, key)
	return types.Attr{}
}

// telRuntime opens one chat span in its first step and one execute_tool
// span in its second, from the ctx DriveLifecycle hands it.
type telRuntime struct {
	tel   *fakeTelemetry
	steps int
}

func (r *telRuntime) Name() string                         { return "tel" }
func (r *telRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

func (r *telRuntime) Start(_ context.Context, _ runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *telRuntime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	name := SpanChat
	status := runtime.Continue
	if r.steps > 0 {
		name = SpanTool
		status = runtime.DoneStatus
	}
	r.steps++
	_, end := r.tel.StartSpan(ctx, name)
	end()
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1}, nil, status, nil
}

func runInfoCtx() context.Context {
	return types.WithRunInfo(context.Background(), types.RunInfo{
		Flow:        "trip",
		SessionID:   "s-1",
		RunID:       "r-1",
		RootRunID:   "r-1",
		ParentRunID: "",
		Turn:        2,
	})
}

func telDrive(t *testing.T, lc *Lifecycle, rt runtime.Runtime) {
	t.Helper()
	for _, err := range DriveLifecycle(runInfoCtx(), lc, rt, runtime.AgentRun{}) {
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
	}
}

func TestTelemetrySpanTree(t *testing.T) {
	t.Run("telemetry.same-tree-on-all-backends", func(t *testing.T) {
		tel := &fakeTelemetry{}
		lc := NewLifecycle(
			WithLifecycleRuns(&lcRuns{rec: &order{}}, stores.Lease{RunID: "r-1"}),
			WithLifecycleTelemetry(tel),
		)
		telDrive(t, lc, &telRuntime{tel: tel})
		if len(tel.spans) != 3 {
			t.Fatalf("spans %v, want run, chat and tool", tel.spanNames())
		}
		run := tel.span(t, SpanRun)
		if !run.ended {
			t.Fatal("run span never ended")
		}
		if run.parent != "" {
			t.Fatalf("run span parent %q, want none", run.parent)
		}
		if len(tel.counts) != 0 {
			t.Fatalf("counts %v, want none on a plain drive", tel.counts)
		}
		for _, child := range []*spanRec{tel.span(t, SpanChat), tel.span(t, SpanTool)} {
			if child.parent != SpanRun {
				t.Fatalf("%q parent %q, want %q", child.name, child.parent, SpanRun)
			}
			if !child.ended {
				t.Fatalf("%q span never ended", child.name)
			}
		}
	})

	t.Run("run-span-carries-canonical-tree-keys", func(t *testing.T) {
		tel := &fakeTelemetry{}
		lc := NewLifecycle(
			WithLifecycleRuns(&lcRuns{rec: &order{}}, stores.Lease{RunID: "r-1"}),
			WithLifecycleTelemetry(tel),
		)
		telDrive(t, lc, &telRuntime{tel: tel})
		run := tel.span(t, SpanRun)
		for key, want := range map[string]string{
			types.KeyFlow:        "trip",
			types.KeySessionID:   "s-1",
			types.KeyRunID:       "r-1",
			types.KeyRootRunID:   "r-1",
			types.KeyParentRunID: "",
		} {
			if got := tel.attr(t, run, key); got.Value != want {
				t.Fatalf("%s = %v, want %v", key, got.Value, want)
			}
		}
		if got := tel.attr(t, run, types.KeyTurn); got.Value != int64(2) {
			t.Fatalf("%s = %v, want 2", types.KeyTurn, got.Value)
		}
	})

	t.Run("nil-telemetry-is-a-no-op", func(t *testing.T) {
		rec := &order{}
		lc := NewLifecycle(WithLifecycleRuns(&lcRuns{rec: rec}, stores.Lease{RunID: "r-1"}))
		telDrive(t, lc, &lcRuntime{script: []lcStep{{status: runtime.DoneStatus}}})
		run := stores.Run{Flow: "trip", SessionID: "s-1", RunID: "r-1"}
		countAbandoned(context.Background(), nil, run)
		countRecovered(context.Background(), nil, run)
		ctx, end := startRunSpan(context.Background(), nil)
		if ctx == nil {
			t.Fatal("nil telemetry returned nil ctx")
		}
		end()
	})

	t.Run("recovered-and-abandoned-count-from-recovery", func(t *testing.T) {
		tel := &fakeTelemetry{}
		run := stores.Run{Flow: "trip", SessionID: "s-1", RunID: "r-1"}
		countAbandoned(context.Background(), tel, run)
		countRecovered(context.Background(), tel, run)
		want := map[string]int64{MetricRunAbandoned: 1, MetricRunRecovered: 1}
		if len(tel.counts) != len(want) {
			t.Fatalf("counts %v, want %v", tel.counts, want)
		}
		for _, c := range tel.counts {
			if want[c.name] != c.n {
				t.Fatalf("count %s = %d, want %d", c.name, c.n, want[c.name])
			}
			if tel.attr(t, &spanRec{name: c.name, attrs: c.attrs}, types.KeyFlow).Value != "trip" {
				t.Fatalf("count %s lacks flow", c.name)
			}
		}
	})
}
