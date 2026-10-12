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
	// ScenarioParallelCalls carries two tool calls in one model message,
	// then answers.
	ScenarioParallelCalls Scenario = "parallel calls"
	// ScenarioMaxTurns calls a tool every turn; the driver stops the run
	// at its MaxTurns bound with Done(StopLimit).
	ScenarioMaxTurns Scenario = "max turns"
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
	t.Run("runtime.parallel-calls-ordering", func(t *testing.T) {
		runRuntimeParallelCallsOrdering(testingTB{t}, newHarness(ScenarioParallelCalls))
	})
	t.Run("runtime.max-turns", func(t *testing.T) {
		runRuntimeMaxTurns(testingTB{t}, newHarness(ScenarioMaxTurns))
	})
	t.Run("runtime.cancellation", func(t *testing.T) {
		runRuntimeCancellation(testingTB{t}, newHarness(ScenarioPlainAnswer))
	})
	t.Run("runtime.no-goroutine-leak", func(t *testing.T) {
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

// runRuntimeParallelCallsOrdering proves the results of one multi-call
// model message reach the next model call in call order.
func runRuntimeParallelCallsOrdering(tb conformanceTB, h Harness) {
	tb.Helper()
	evs, err := h.Run(context.Background(), userTurn())
	if err != nil {
		tb.Fatalf("Run error = %v, want a completed run", err)
	}
	assertRunDone(tb, evs)
	obs := h.Observed()
	if len(obs.ToolCalls) != 2 {
		tb.Fatalf("tool calls %v, want two in call order", obs.ToolCalls)
	}
	if obs.ToolCalls[0] == obs.ToolCalls[1] {
		tb.Errorf("tool calls %v, want the declared call order", obs.ToolCalls)
	}
}

// runRuntimeMaxTurns proves the driver ends the run with Done(StopLimit)
// after exactly three model calls when the model calls a tool every turn.
func runRuntimeMaxTurns(tb conformanceTB, h Harness) {
	tb.Helper()
	evs, err := h.Run(context.Background(), userTurn())
	if err != nil {
		tb.Fatalf("Run error = %v, want a limit-ended run", err)
	}
	if len(evs) == 0 {
		tb.Fatalf("events = [], want a terminal Done")
	}
	done, ok := evs[len(evs)-1].(types.Done)
	if !ok {
		tb.Fatalf("last event %T, want types.Done", evs[len(evs)-1])
	}
	if done.Reason != types.StopLimit {
		tb.Errorf("Done reason = %q, want %q", done.Reason, types.StopLimit)
	}
	calls, counted := h.(interface{ ModelCalls() int })
	if !counted {
		tb.Fatalf("harness %T does not report its model calls", h)
	}
	if got := calls.ModelCalls(); got != 3 {
		tb.Errorf("model calls = %d, want 3", got)
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
