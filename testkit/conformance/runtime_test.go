package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// errToolDenied is the failure the gate step's tool raises in the
// step-error scenario; RunToolChain recovers it as a named StepError.
var errToolDenied = errors.New("tool denied by gate")

// foreignToolInfo mirrors the part of eino's schema.ToolInfo the adapter
// needs. The repo's dependency rule refuses eino itself, so the imported
// seam is pinned by hand.
type foreignToolInfo struct {
	Name string
	Desc string
}

// foreignOption mirrors the variadic option slot in eino's InvokableRun.
type foreignOption struct{}

// foreignEcho implements eino's InvokableTool interface shape: Info plus one
// JSON-in, string-out invoke. A real eino tool would arrive through the
// eino import; the shape it must satisfy is identical.
type foreignEcho struct{ called int }

func (f *foreignEcho) Info() *foreignToolInfo {
	return &foreignToolInfo{Name: "echo", Desc: "echoes its input"}
}

func (f *foreignEcho) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...foreignOption) (string, error) {
	f.called++
	return argumentsInJSON, nil
}

// foreignToolAdapter adapts the eino-shaped tool to types.Tool so the full
// governed chain runs over it.
type foreignToolAdapter struct{ inner *foreignEcho }

func (a foreignToolAdapter) Spec() types.ToolSpec {
	info := a.inner.Info()
	return types.ToolSpec{
		Name:        info.Name,
		Description: info.Desc,
		Schema:      json.RawMessage(`{"type":"object"}`),
	}
}

func (a foreignToolAdapter) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	out, err := a.inner.InvokableRun(ctx, string(args))
	if err != nil {
		return types.ToolResult{}, err
	}
	return types.ToolResult{Content: []types.Block{types.Text{Text: out}}}, nil
}

// panicTool fails inside the chain so RunToolChain's step recovery is on
// the exercised path.
type panicTool struct{}

func (panicTool) Spec() types.ToolSpec { return types.ToolSpec{Name: "boom"} }

func (panicTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	panic(errToolDenied)
}

// nativeTool maps a scenario onto its governed tool: the foreign tool for
// the round trip, the panicking one for the step-error case.
func nativeTool(sc Scenario) types.Tool {
	switch sc {
	case ScenarioToolRoundTrip:
		return foreignToolAdapter{inner: &foreignEcho{}}
	case ScenarioToolError:
		return panicTool{}
	}
	return nil
}

func newForeignHarness(echo *foreignEcho) Harness {
	return &nativeRuntimeHarness{
		scenario: ScenarioToolRoundTrip,
		model:    gohantest.NewScriptedModel(confProfile, nativeTurns(ScenarioToolRoundTrip)...),
		tool:     foreignToolAdapter{inner: echo},
	}
}

func nativeTurns(sc Scenario) []gohantest.Turn {
	if sc == ScenarioPlainAnswer {
		return []gohantest.Turn{gohantest.Text("hi")}
	}
	return []gohantest.Turn{
		gohantest.ToolCall("echo", map[string]string{"say": "hi"}),
		gohantest.Text("done"),
	}
}

// leakingHarness spawns a goroutine that outlives its run until released.
type leakingHarness struct {
	release chan struct{}
	started chan struct{}
	done    chan struct{}
}

func (h *leakingHarness) Run(context.Context, []types.Message) ([]types.Event, error) {
	go func() {
		defer close(h.done)
		close(h.started)
		<-h.release
	}()
	<-h.started
	return nil, nil
}

func (h *leakingHarness) Observed() Observations { return Observations{} }

// TestRuntimeAndChainConformance runs the Runtime and Chain suites against
// the shipped native runtime, including the foreign-tool scenario and the
// negative leak and cancellation cases.
func TestRuntimeAndChainConformance(t *testing.T) {
	newNative := newNativeRuntimeHarness

	t.Run("runtime.foreign-tool-under-native", func(t *testing.T) {
		capture := &captureTB{}
		suiteEcho := &foreignEcho{}
		runRuntimeToolRoundTrip(capture, newForeignHarness(suiteEcho))
		chainEcho := &foreignEcho{}
		runChainOrdering(capture, newForeignHarness(chainEcho))
		if capture.Failed() {
			t.Fatal("runtime.foreign-tool-under-native: suite failed for the foreign tool")
		}
		if suiteEcho.called != 1 || chainEcho.called != 1 {
			t.Fatalf("foreign InvokableRun calls = %d and %d, want 1 each", suiteEcho.called, chainEcho.called)
		}
	})

	t.Run("runtime suite passes for the shipped native runtime", func(t *testing.T) {
		Runtime(t, newNative)
	})

	t.Run("chain suite passes for the shipped native chain", func(t *testing.T) {
		Chain(t, newNative)
	})

	t.Run("leak check passes a clean run", func(t *testing.T) {
		capture := &captureTB{}
		runRuntimeLeakCheck(capture, newNative)
		if capture.Failed() {
			t.Fatal("leak check failed a clean run")
		}
	})

	t.Run("leak check fails a leaking run", func(t *testing.T) {
		release := make(chan struct{})
		started := make(chan struct{})
		done := make(chan struct{})
		capture := &captureTB{}
		runRuntimeLeakCheck(capture, func(Scenario) Harness {
			return &leakingHarness{release: release, started: started, done: done}
		})
		if !capture.Failed() {
			close(release)
			<-done
			t.Fatal("leak check passed a leaking run")
		}
		close(release)
		<-done
	})

	t.Run("cancelled run stops", func(t *testing.T) {
		h := newNative(ScenarioPlainAnswer)
		ctx, cancel := gohantest.CancelledContext()
		defer cancel()
		evs, err := h.Run(ctx, userTurn())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
		if len(evs) != 0 {
			t.Errorf("events = %v, want none", evs)
		}
		obs := h.Observed()
		if !sawStep(obs, StepGuard) || sawStep(obs, StepGate) || len(obs.ToolCalls) > 0 {
			t.Errorf("observed %v after cancellation, want guard only", obs)
		}
	})
}
