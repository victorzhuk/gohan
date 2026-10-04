package gohan

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// reasoningTurns scripts two turns: a reasoning turn followed by the final
// text reply, so the accumulated reasoning must reach the second request.
func reasoningTurns() *scriptTurns {
	return &scriptTurns{turns: [][]types.ModelChunk{
		{
			{Kind: types.DeltaReasoning, Delta: "think "},
			{Kind: types.DeltaReasoning, Delta: "hard"},
			toolCall("c1", "echo"),
			{Finish: types.FinishToolUse},
		},
		{
			{Kind: types.DeltaReasoning, Delta: "mull"},
			turnTextChunk("done"),
		},
	}}
}

func driveReasoning(t *testing.T, visible bool) (evs []types.Event) {
	t.Helper()
	m := reasoningTurns()
	tool := &echoTool{}
	cfg := turnConfig{
		model:            m.model(),
		assemble:         turnAssemble(nil),
		tools:            turnToolset(tool),
		maxTurns:         6,
		reasoningVisible: visible,
	}
	if _, err := driveTurnCollect(func(y func(types.Event, error) bool) {
		driveTurns(types.WithSink(context.Background(), allSink(&evs)), cfg, y)
	}); err != nil {
		t.Fatalf("drive: %v", err)
	}
	return evs
}

func TestReasoningGating(t *testing.T) {
	ctx := context.Background()

	t.Run("reasoning-hidden-without-cap", func(t *testing.T) {
		evs := driveReasoning(t, false)
		for _, ev := range evs {
			if d, ok := ev.(types.ReasoningDelta); ok {
				t.Fatalf("consumer saw ReasoningDelta %q with ReasoningVisible off", d.Delta)
			}
		}
	})

	t.Run("reasoning-visible-with-cap", func(t *testing.T) {
		evs := driveReasoning(t, true)
		var got []string
		for _, ev := range evs {
			if d, ok := ev.(types.ReasoningDelta); ok {
				got = append(got, d.Delta)
			}
		}
		if len(got) != 3 || got[0] != "think " || got[1] != "hard" || got[2] != "mull" {
			t.Fatalf("ReasoningDelta deltas = %q, want the streamed fragments in order", got)
		}
	})

	t.Run("reasoning-retained-when-hidden", func(t *testing.T) {
		m := reasoningTurns()
		tool := &echoTool{}
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(tool),
			maxTurns: 6,
		}
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, noopSink()), cfg, y)
		}); err != nil {
			t.Fatalf("drive: %v", err)
		}
		if len(m.reqs) != 2 {
			t.Fatalf("model calls = %d, want 2", len(m.reqs))
		}
		assertRetained(t, m.reqs[1].Messages, "think hard")
	})

	t.Run("reasoning-retained-when-visible", func(t *testing.T) {
		m := reasoningTurns()
		tool := &echoTool{}
		cfg := turnConfig{
			model:            m.model(),
			assemble:         turnAssemble(nil),
			tools:            turnToolset(tool),
			maxTurns:         6,
			reasoningVisible: true,
		}
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, noopSink()), cfg, y)
		}); err != nil {
			t.Fatalf("drive: %v", err)
		}
		if len(m.reqs) != 2 {
			t.Fatalf("model calls = %d, want 2", len(m.reqs))
		}
		assertRetained(t, m.reqs[1].Messages, "think hard")
	})
}

// assertRetained checks the second request carries the first turn's
// retained reasoning on its assistant message.
func assertRetained(t *testing.T, msgs []types.Message, want string) {
	t.Helper()
	for _, msg := range msgs {
		if msg.Role != types.RoleAssistant {
			continue
		}
		if got, _ := msg.Meta[metaReasoning].(string); got == want {
			return
		}
	}
	t.Fatalf("no assistant message in the next request retains reasoning %q", want)
}
