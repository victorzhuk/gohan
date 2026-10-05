package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// Harness is the seam a runtime backend plugs into: it drives one governed
// run to completion and reports what the governed seams observed, so a
// suite can prove governance ran rather than that a call succeeded.
type Harness interface {
	Run(ctx context.Context, input []types.Message) ([]types.Event, error)
	Observed() Observations
}

// Scenario selects what a harness drives for one suite run.
type Scenario string

const (
	// ScenarioPlainAnswer runs one model turn that answers with text.
	ScenarioPlainAnswer Scenario = "plain answer"
	// ScenarioToolRoundTrip calls a tool, then answers with text.
	ScenarioToolRoundTrip Scenario = "tool round trip"
	// ScenarioToolError fails inside the gate step.
	ScenarioToolError Scenario = "tool error"
)

// Canonical governed-step names a harness records in Observations.
const (
	StepGuard = "guard"
	StepGate  = "gate"
)

// Observations records the governed steps that executed, in order, and the
// tools that ran inside them.
type Observations struct {
	Steps     []string
	ToolCalls []string
}

// Runtime runs the runtime conformance suite: a run streams to a completed
// Done, honours cancellation, and leaves no goroutine behind.
func Runtime(t *testing.T, newHarness func(Scenario) Harness) {
	t.Helper()
	t.Run("runtime.plain-answer", func(t *testing.T) {
		runRuntimePlainAnswer(testingTB{t}, newHarness(ScenarioPlainAnswer))
	})
	t.Run("runtime.tool-round-trip", func(t *testing.T) {
		runRuntimeToolRoundTrip(testingTB{t}, newHarness(ScenarioToolRoundTrip))
	})
	t.Run("runtime.cancellation", func(t *testing.T) {
		runRuntimeCancellation(testingTB{t}, newHarness(ScenarioPlainAnswer))
	})
	t.Run("run leaves no goroutine behind", func(t *testing.T) {
		runRuntimeLeakCheck(testingTB{t}, newHarness)
	})
}

func runRuntimePlainAnswer(tb conformanceTB, h Harness) {
	tb.Helper()
	evs, err := h.Run(context.Background(), userTurn())
	if err != nil {
		tb.Fatalf("Run error = %v, want a completed run", err)
	}
	assertRunDone(tb, evs)
	if !sawStep(h.Observed(), StepGuard) {
		tb.Errorf("steps %v: guard did not run", h.Observed().Steps)
	}
}

func runRuntimeToolRoundTrip(tb conformanceTB, h Harness) {
	tb.Helper()
	evs, err := h.Run(context.Background(), userTurn())
	if err != nil {
		tb.Fatalf("Run error = %v, want a completed run", err)
	}
	assertRunDone(tb, evs)
	obs := h.Observed()
	if !sawStep(obs, StepGuard) || !sawStep(obs, StepGate) {
		tb.Errorf("steps %v: governed chain did not run", obs.Steps)
	}
	if len(obs.ToolCalls) == 0 {
		tb.Errorf("tool calls %v: no tool ran", obs.ToolCalls)
	}
}

func runRuntimeCancellation(tb conformanceTB, h Harness) {
	tb.Helper()
	ctx, cancel := gohantest.CancelledContext()
	defer cancel()
	evs, err := h.Run(ctx, userTurn())
	if !errors.Is(err, context.Canceled) {
		tb.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if len(evs) != 0 {
		tb.Errorf("events = %v, want none after cancellation", evs)
	}
	obs := h.Observed()
	if !sawStep(obs, StepGuard) {
		tb.Errorf("steps %v: guard did not observe the run", obs.Steps)
	}
	if sawStep(obs, StepGate) || len(obs.ToolCalls) > 0 {
		tb.Errorf("components ran after cancellation: steps %v tools %v", obs.Steps, obs.ToolCalls)
	}
}

// runRuntimeLeakCheck wraps a completed run in LeakCheck: the harness under
// test must not outlive its run.
func runRuntimeLeakCheck(tb conformanceTB, newHarness func(Scenario) Harness) {
	tb.Helper()
	gohantest.LeakCheck(leakTB{tb}, func() {
		h := newHarness(ScenarioToolRoundTrip)
		if _, err := h.Run(context.Background(), userTurn()); err != nil {
			tb.Fatalf("Run error = %v", err)
		}
	})
}

func assertRunDone(tb conformanceTB, evs []types.Event) {
	tb.Helper()
	if len(evs) == 0 {
		tb.Fatalf("events = [], want at least a Done")
	}
	done, ok := evs[len(evs)-1].(types.Done)
	if !ok {
		tb.Fatalf("last event %T, want types.Done", evs[len(evs)-1])
	}
	if done.Reason != types.StopCompleted {
		tb.Errorf("Done reason = %q, want %q", done.Reason, types.StopCompleted)
	}
}

func userTurn() []types.Message {
	return []types.Message{
		{ID: "in-1", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}},
	}
}

func sawStep(obs Observations, step string) bool {
	for _, s := range obs.Steps {
		if s == step {
			return true
		}
	}
	return false
}
