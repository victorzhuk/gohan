package eino

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/testkit/conformance"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

func TestCtxPropagation(t *testing.T) {
	t.Run("cancel_mid_turn_runs_no_further_component", func(t *testing.T) {
		gohantest.LeakCheck(t, func() {
			h := newEinoHarness(conformance.ScenarioToolRoundTrip).(*einoHarness)
			blocking := &blockingModel{entered: make(chan struct{})}
			h.model = blocking
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := h.Run(ctx, harnessInput()); done <- err }()
			<-blocking.entered
			cancel()
			err := <-done
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Run error = %v, want context.Canceled", err)
			}
			obs := h.Observed()
			if len(obs.ToolCalls) > 0 {
				t.Errorf("tool calls %v, want none after cancel", obs.ToolCalls)
			}
			for _, step := range obs.Steps {
				if step == conformance.StepGate {
					t.Errorf("gate observed after cancel: steps %v", obs.Steps)
				}
			}
		})
	})
}
