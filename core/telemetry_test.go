package gohan

import (
	"context"
	"encoding/json/jsontext"
	"testing"

	"github.com/victorzhuk/gohan/core/guards"
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
	return context.WithValue(ctx, telCtxKey{}, rec), func(final ...types.Attr) {
		rec.ended = true
		rec.attrs = append(rec.attrs, final...)
	}
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

// telRuntime drives the real governed turn loop: two model calls and one
// tool call per step, opening no spans itself.
type telRuntime struct {
	turns [][]types.ModelChunk
	tool  *echoTool
}

func (r *telRuntime) Name() string                         { return "tel" }
func (r *telRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

func (r *telRuntime) Start(_ context.Context, _ runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *telRuntime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	m := &scriptTurns{turns: r.turns}
	cfg := turnConfig{
		model:    m.model(),
		assemble: turnAssemble(nil),
		tools:    turnToolset(r.tool),
		maxTurns: 4,
	}
	_, err := driveTurnCollect(func(y func(types.Event, error) bool) { driveTurns(ctx, cfg, y) })
	if err != nil {
		return runtime.State{}, nil, runtime.DoneStatus, err
	}
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1}, nil, runtime.DoneStatus, nil
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
	ctx := runInfoCtx()
	if lc.telemetry != nil {
		ctx = withTelemetry(ctx, lc.telemetry)
	}
	for _, err := range DriveLifecycle(ctx, lc, rt, runtime.AgentRun{}) {
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
	}
}

type telGuard struct{}

func (telGuard) Decide(context.Context, guards.GuardInput) (types.Decision[guards.GuardVerdict], error) {
	return types.Decision[guards.GuardVerdict]{Value: guards.GuardVerdict{Action: guards.Pass}}, nil
}

func TestTelemetryGuardAndToolMetrics(t *testing.T) {
	t.Run("guard consultation emits a span", func(t *testing.T) {
		tel := &fakeTelemetry{}
		ctx := withTelemetry(runInfoCtx(), tel)
		if _, err := ConsultGuard(ctx, telGuard{}, guards.GuardInput{Stage: types.StageInput}); err != nil {
			t.Fatalf("ConsultGuard: %v", err)
		}
		guard := tel.span(t, SpanGuard)
		if len(tel.spans) != 1 {
			t.Fatalf("spans %v, want only the guard span", tel.spanNames())
		}
		if got := tel.attr(t, guard, types.KeyGuardStage).Value; got != "input" {
			t.Fatalf("stage = %v, want input", got)
		}
		if got := tel.attr(t, guard, types.KeyGuardVerdict).Value; got != "pass" {
			t.Fatalf("verdict = %v, want pass", got)
		}
	})

	t.Run("tool boundary counters count once", func(t *testing.T) {
		tel := &fakeTelemetry{}
		ctx := withTelemetry(runInfoCtx(), tel)
		set := turnToolset(&echoTool{})
		if _, err := CallTool(ctx, set, "nope", jsontext.Value(`{}`)); err != nil {
			t.Fatalf("unknown tool: %v", err)
		}
		if _, err := CallTool(ctx, set, "echo", jsontext.Value(`{"a":1,"a":2}`)); err != nil {
			t.Fatalf("invalid args: %v", err)
		}
		want := map[string]int64{MetricToolUnknown: 1, MetricToolInvalidArgs: 1}
		if len(tel.counts) != len(want) {
			t.Fatalf("counts %v, want %v", tel.counts, want)
		}
		for _, c := range tel.counts {
			if want[c.name] != c.n {
				t.Fatalf("count %s = %d, want %d", c.name, c.n, want[c.name])
			}
			rec := &spanRec{name: c.name, attrs: c.attrs}
			wantName := "nope"
			if c.name == MetricToolInvalidArgs {
				wantName = "echo"
			}
			if tel.attr(t, rec, types.KeyToolName).Value != wantName {
				t.Fatalf("%s tool name = %v, want %s", c.name, tel.attr(t, rec, types.KeyToolName).Value, wantName)
			}
			if c.name == MetricToolInvalidArgs {
				if got := tel.attr(t, rec, types.KeySuspendReason); got.Value != "duplicate_key" {
					t.Fatalf("reason = %v, want duplicate_key", got.Value)
				}
			}
		}
	})
}

func TestTelemetrySpanTree(t *testing.T) {
	t.Run("telemetry.same-tree-on-all-backends", func(t *testing.T) {
		tel := &fakeTelemetry{}
		lc := NewLifecycle(
			WithLifecycleRuns(&lcRuns{rec: &order{}}, stores.Lease{RunID: "r-1"}),
			WithLifecycleTelemetry(tel),
		)
		telDrive(t, lc, &telRuntime{turns: [][]types.ModelChunk{
			{toolCall("c1", "echo"), {Finish: types.FinishToolUse}},
			{turnTextChunk("done")},
		}, tool: &echoTool{}})
		if len(tel.spans) != 4 {
			t.Fatalf("spans %v, want run, 2 chat and 1 tool", tel.spanNames())
		}
		chats := 0
		for _, s := range tel.spans {
			switch s.name {
			case SpanChat:
				chats++
			case SpanRun, SpanTool:
			default:
				t.Fatalf("unexpected span %q", s.name)
			}
		}
		if chats != 2 {
			t.Fatalf("chat spans %d, want 2", chats)
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
		telDrive(t, lc, &telRuntime{turns: [][]types.ModelChunk{
			{turnTextChunk("hi")},
		}, tool: &echoTool{}})
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
