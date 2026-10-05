package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

// Chain runs the chain conformance suite: the governed tool chain composes
// as chains.RunToolChain steps - guard outermost, gate inside it, the tool
// innermost - and an error raised inside a step reaches the caller named.
func Chain(t *testing.T, newHarness func(Scenario) Harness) {
	t.Helper()
	t.Run("guard runs before gate", func(t *testing.T) {
		runChainOrdering(testingTB{t}, newHarness(ScenarioToolRoundTrip))
	})
	t.Run("step error propagates", func(t *testing.T) {
		runChainStepError(testingTB{t}, newHarness(ScenarioToolError))
	})
}

func runChainOrdering(tb conformanceTB, h Harness) {
	tb.Helper()
	if _, err := h.Run(context.Background(), userTurn()); err != nil {
		tb.Fatalf("Run error = %v", err)
	}
	obs := h.Observed()
	guard, gate := stepIndex(obs, StepGuard), stepIndex(obs, StepGate)
	if guard < 0 || gate < 0 {
		tb.Fatalf("steps %v: governed steps missing", obs.Steps)
	}
	if guard > gate {
		tb.Errorf("steps %v: gate ran before guard", obs.Steps)
	}
	if len(obs.ToolCalls) == 0 {
		tb.Errorf("tool calls %v: no tool ran inside the chain", obs.ToolCalls)
	}
}

func runChainStepError(tb conformanceTB, h Harness) {
	tb.Helper()
	_, err := h.Run(context.Background(), userTurn())
	if err == nil {
		tb.Fatalf("Run error = nil, want a step error")
	}
	se, ok := errors.AsType[*types.StepError](err)
	if !ok {
		tb.Fatalf("error %v, want *types.StepError", err)
		return
	}
	if se.Step != StepGate {
		tb.Errorf("step = %q, want %q", se.Step, StepGate)
	}
}

func stepIndex(obs Observations, step string) int {
	for i, s := range obs.Steps {
		if s == step {
			return i
		}
	}
	return -1
}

// governedChain composes the canonical governed order over one tool call.
func governedChain(obs *Observations, inner types.Tool) chains.ToolChain {
	return chains.ToolChain{
		{
			Name: StepGuard,
			Kind: chains.KindGuard,
			Use: func(next types.ToolFunc) types.ToolFunc {
				return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
					obs.Steps = append(obs.Steps, StepGuard)
					return next(ctx, call)
				}
			},
		},
		{
			Name: StepGate,
			Kind: chains.KindGate,
			Use: func(next types.ToolFunc) types.ToolFunc {
				return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
					obs.Steps = append(obs.Steps, StepGate)
					return next(ctx, call)
				}
			},
		},
	}
}
