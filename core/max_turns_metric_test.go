package gohan

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type maxTurnsSink struct {
	counts map[string]int64
}

func (s *maxTurnsSink) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (s *maxTurnsSink) Count(_ context.Context, name string, n int64, _ ...types.Attr) {
	if s.counts == nil {
		s.counts = map[string]int64{}
	}
	s.counts[name] += n
}

func (s *maxTurnsSink) Record(context.Context, string, float64, ...types.Attr) {}

func TestDriveTurnsMaxTurnsMetric(t *testing.T) {
	toolTurn := []types.ModelChunk{toolCall("c", "echo"), {Finish: types.FinishToolUse}}

	t.Run("one increment on a limit-ending run", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{toolTurn, toolTurn, toolTurn}}
		sink := &maxTurnsSink{}
		ctx := types.WithTelemetry(context.Background(), sink)
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(&echoTool{}),
			maxTurns: 2,
		}
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) { driveTurns(ctx, cfg, y) }); err != nil {
			t.Fatalf("drive: %v", err)
		}
		if n := sink.counts["gohan.max_turns.reached"]; n != 1 {
			t.Fatalf("gohan.max_turns.reached = %d, want 1", n)
		}
	})

	t.Run("zero on a completed run", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{{{Finish: types.FinishStop}}}}
		sink := &maxTurnsSink{}
		ctx := types.WithTelemetry(context.Background(), sink)
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			maxTurns: 6,
		}
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) { driveTurns(ctx, cfg, y) }); err != nil {
			t.Fatalf("drive: %v", err)
		}
		if n := sink.counts["gohan.max_turns.reached"]; n != 0 {
			t.Fatalf("gohan.max_turns.reached = %d, want 0", n)
		}
	})
}
