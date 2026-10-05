package conformance

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// reversedHarness runs the gate step before the guard, the ordering
// non-conformance the Chain suite must catch.
type reversedHarness struct{ obs Observations }

func (h *reversedHarness) Run(ctx context.Context, input []types.Message) ([]types.Event, error) {
	h.obs.Steps = append(h.obs.Steps, StepGate, StepGuard)
	h.obs.ToolCalls = append(h.obs.ToolCalls, "echo")
	return []types.Event{types.Done{Reason: types.StopCompleted}}, nil
}

func (h *reversedHarness) Observed() Observations {
	return Observations{Steps: append([]string(nil), h.obs.Steps...), ToolCalls: append([]string(nil), h.obs.ToolCalls...)}
}

// swallowingHarness returns a bare error where the suite wants a named
// step error.
type swallowingHarness struct{}

func (swallowingHarness) Run(context.Context, []types.Message) ([]types.Event, error) {
	return nil, errToolDenied
}

func (swallowingHarness) Observed() Observations { return Observations{} }

// TestChainConformanceRejectsBrokenChains proves the Chain suite fails a
// harness that runs the gate outside the guard and one that swallows the
// step error's name.
func TestChainConformanceRejectsBrokenChains(t *testing.T) {
	t.Run("gate before guard fails", func(t *testing.T) {
		capture := &captureTB{}
		runChainOrdering(capture, &reversedHarness{})
		if !capture.Failed() {
			t.Fatal("suite passed a chain that ran the gate before the guard")
		}
	})

	t.Run("unnamed step error fails", func(t *testing.T) {
		capture := &captureTB{}
		runChainStepError(capture, swallowingHarness{})
		if !capture.Failed() {
			t.Fatal("suite passed an unnamed step error")
		}
	})
}
