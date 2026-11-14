package stores

import (
	"context"
	"testing"
)

type plainRuns struct {
	Runs
}

var _ Runs = (*plainRuns)(nil)

func TestRunCostSurface(t *testing.T) {
	t.Parallel()

	t.Run("stores.run-cost-optional", func(t *testing.T) {
		t.Parallel()
		s := NewMemoryRuns()
		ctx := context.Background()

		l, err := s.Start(ctx, Run{SessionID: "s1", RunID: "run-1", Flow: "chat"}, LeaseTTL)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if err := s.RecordCost(ctx, l, 0.07); err != nil {
			t.Fatalf("record cost: %v", err)
		}
		if err := s.Finish(ctx, l, Finished, nil, ""); err != nil {
			t.Fatalf("finish: %v", err)
		}
		run, err := s.ByID(ctx, "run-1")
		if err != nil {
			t.Fatalf("by id: %v", err)
		}
		if run.Cost != 0.07 {
			t.Fatalf("cost = %v, want 0.07", run.Cost)
		}

		l2, err := s.Start(ctx, Run{SessionID: "s2", RunID: "run-2", Flow: "chat"}, LeaseTTL)
		if err != nil {
			t.Fatalf("start second: %v", err)
		}
		plain := &plainRuns{Runs: s}
		if _, ok := any(plain).(RunCostRecorder); ok {
			t.Fatal("plain delegation must not expose RecordCost")
		}
		if err := plain.Finish(ctx, l2, Finished, nil, ""); err != nil {
			t.Fatalf("finish second: %v", err)
		}
		run2, err := s.ByID(ctx, "run-2")
		if err != nil {
			t.Fatalf("by id second: %v", err)
		}
		if run2.Cost != 0 {
			t.Fatalf("cost = %v, want 0", run2.Cost)
		}
	})
}
