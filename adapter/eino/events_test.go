package eino

import (
	"context"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/conformance"
)

type recordingSink struct {
	mu  sync.Mutex
	evs []types.Event
}

func (s *recordingSink) Emit(_ context.Context, e types.Event) {
	s.mu.Lock()
	s.evs = append(s.evs, e)
	s.mu.Unlock()
}

func (s *recordingSink) events() []types.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]types.Event(nil), s.evs...)
}

func TestComponentEvents(t *testing.T) {
	t.Run("component_events_come_from_the_sink", func(t *testing.T) {
		h := newEinoHarness(conformance.ScenarioPlainAnswer)
		sink := &recordingSink{}
		ctx := types.WithSink(context.Background(), sink)
		evs, err := h.Run(ctx, harnessInput())
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if len(evs) == 0 {
			t.Fatal("events = [], want a completed run")
		}
		var deltas []types.TextDelta
		for _, e := range sink.events() {
			if d, ok := e.(types.TextDelta); ok {
				deltas = append(deltas, d)
			}
		}
		if len(deltas) != 1 {
			t.Fatalf("sink deltas = %d, want exactly one per model delta", len(deltas))
		}
		if deltas[0].Delta != "done" {
			t.Errorf("delta = %q, want done", deltas[0].Delta)
		}
		if deltas[0].MessageID != assistantID {
			t.Errorf("message id = %q, want %q", deltas[0].MessageID, assistantID)
		}
	})
}

func TestStepEvents(t *testing.T) {
	t.Run("step_returns_runtime_events_only", func(t *testing.T) {
		h := newEinoHarness(conformance.ScenarioToolRoundTrip)
		if _, err := h.Run(context.Background(), harnessInput()); err != nil {
			t.Fatalf("Run error = %v", err)
		}
		for i, step := range h.(*einoHarness).StepEvents() {
			for _, ev := range step {
				switch ev.(type) {
				case types.TextDelta, types.ToolStarted, types.ToolFinished:
					t.Errorf("step %d returned component event %T, want runtime events only", i, ev)
				}
			}
		}
	})
}
