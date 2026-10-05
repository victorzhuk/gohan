package limit

import (
	"context"
	"errors"
	"iter"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

var testPricing = types.Pricing{Input: 0.01, Output: 0.02}

// spendModel returns a model func that streams one chunk carrying usage for
// cost coins of spend.
func spendModel(cost float64) types.ModelFunc {
	return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			yield(types.ModelChunk{Usage: &types.Usage{InputTokens: int(cost / testPricing.Input)}}, nil)
		}
	}
}

func collect(seq iter.Seq2[types.ModelChunk, error]) error {
	for _, err := range seq {
		if err != nil {
			return err
		}
	}
	return nil
}

var okTool types.ToolFunc = func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
	return types.ToolResult{}, nil
}

func TestRunLimits(t *testing.T) {
	t.Run("two independent runs each spend the full budget", func(t *testing.T) {
		mw := Limits(types.RunLimits{MaxCost: 0.03, MaxWallClock: time.Minute}, testPricing, nil)
		for run := range 2 {
			st := NewLimitsState()
			ctx := WithLimitsState(context.Background(), st)
			err := collect(mw(spendModel(0.01))(ctx, types.ModelRequest{}))
			if err != nil {
				t.Fatalf("run %d: err = %v, want within budget", run, err)
			}
			err = collect(mw(spendModel(0.01))(ctx, types.ModelRequest{}))
			if err != nil {
				t.Fatalf("run %d: err = %v, want within budget", run, err)
			}
		}
	})

	t.Run("a preset ledger without a context ledger still bounds one run", func(t *testing.T) {
		st := NewLimitsState()
		mw := Limits(types.RunLimits{MaxCost: 0.015, MaxWallClock: time.Minute}, testPricing, st)
		if err := collect(mw(spendModel(0.01))(context.Background(), types.ModelRequest{})); err != nil {
			t.Fatalf("first call: %v", err)
		}
		err := collect(mw(spendModel(0.01))(context.Background(), types.ModelRequest{}))
		var over *types.LimitExceededError
		if !errors.As(err, &over) || over.Limit != "MaxCost" {
			t.Fatalf("err = %v, want *LimitExceededError{Limit: MaxCost}", err)
		}
		if st.Cost() != 0.02 {
			t.Fatalf("cost = %v, want 0.02", st.Cost())
		}
	})

	t.Run("provider calls share the tool total", func(t *testing.T) {
		st := NewLimitsState()
		ctx := WithLimitsState(context.Background(), st)
		l := types.RunLimits{MaxToolCalls: 2, MaxWallClock: time.Minute}
		mw := Limits(l, testPricing, st)
		tmw := ToolLimits(l, st)
		for i := range 2 {
			if err := collect(mw(spendModel(0))(ctx, types.ModelRequest{})); err != nil {
				t.Fatalf("model call %d: %v", i, err)
			}
			if _, err := tmw(okTool)(ctx, types.ToolUse{}); err != nil {
				t.Fatalf("tool call %d: %v", i, err)
			}
		}
		snap := st.Snapshot()
		if snap.ToolUses != 2 {
			t.Fatalf("toolUses = %d, want 2", snap.ToolUses)
		}
	})

	t.Run("provider tool usage charges the tool total", func(t *testing.T) {
		st := NewLimitsState()
		ctx := WithLimitsState(context.Background(), st)
		l := types.RunLimits{MaxCost: 0, MaxWallClock: time.Minute}
		mw := Limits(l, types.Pricing{Input: 0.01, ProviderCall: map[string]float64{"search": 0.5}}, st)
		err := collect(mw(func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			return func(yield func(types.ModelChunk, error) bool) {
				yield(types.ModelChunk{Usage: &types.Usage{InputTokens: 1, ProviderToolCalls: map[string]int{"search": 1}}}, nil)
			}
		})(ctx, types.ModelRequest{}))
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if st.Cost() != 0.51 {
			t.Fatalf("cost = %v, want 0.51", st.Cost())
		}
	})

	t.Run("wall clock overrun refuses the next model call", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			st := NewLimitsState()
			ctx := WithLimitsState(context.Background(), st)
			mw := Limits(types.RunLimits{MaxWallClock: time.Minute}, testPricing, st)
			if err := collect(mw(spendModel(0))(ctx, types.ModelRequest{})); err != nil {
				t.Fatalf("first call: %v", err)
			}
			time.Sleep(2 * time.Minute)
			err := collect(mw(spendModel(0))(ctx, types.ModelRequest{}))
			var over *types.LimitExceededError
			if !errors.As(err, &over) || over.Limit != "MaxWallClock" {
				t.Fatalf("err = %v, want *LimitExceededError{Limit: MaxWallClock}", err)
			}
		})
	})

	t.Run("a streaming call cut by the wall clock reports MaxWallClock", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			st := NewLimitsState()
			ctx := WithLimitsState(context.Background(), st)
			mw := Limits(types.RunLimits{MaxWallClock: time.Minute}, testPricing, st)
			slow := func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
				return func(yield func(types.ModelChunk, error) bool) {
					time.Sleep(2 * time.Minute)
					yield(types.ModelChunk{}, nil)
				}
			}
			err := collect(mw(slow)(ctx, types.ModelRequest{}))
			var over *types.LimitExceededError
			if !errors.As(err, &over) || over.Limit != "MaxWallClock" {
				t.Fatalf("err = %v, want *LimitExceededError{Limit: MaxWallClock}", err)
			}
		})
	})

	t.Run("passing MaxTurns records the counter and does not refuse", func(t *testing.T) {
		st := NewLimitsState()
		ctx := WithLimitsState(context.Background(), st)
		mw := Limits(types.RunLimits{MaxTurns: 2, MaxWallClock: time.Minute}, testPricing, st)
		for range 4 {
			if err := collect(mw(spendModel(0))(ctx, types.ModelRequest{})); err != nil {
				t.Fatalf("call: err = %v, want no refusal past MaxTurns", err)
			}
		}
		if snap := st.Snapshot(); snap.Turns != 4 {
			t.Fatalf("turns = %d, want 4", snap.Turns)
		}
	})

	t.Run("a refused batch leaves the tool total unchanged", func(t *testing.T) {
		st := NewLimitsState()
		l := types.RunLimits{MaxToolCalls: 1, MaxWallClock: time.Minute}
		_, release, err := st.ReserveBatch(context.Background(), l, 2)
		if !errors.Is(err, runtime.ErrBatchOverrun) {
			t.Fatalf("err = %v, want ErrBatchOverrun", err)
		}
		release()
		if got := st.Snapshot().ToolUses; got != 0 {
			t.Fatalf("toolUses = %d, want 0 after refused reservation", got)
		}
	})

	t.Run("an accepted batch reserves once and its calls charge nothing more", func(t *testing.T) {
		st := NewLimitsState()
		ctx := WithLimitsState(context.Background(), st)
		l := types.RunLimits{MaxToolCalls: 3, MaxWallClock: time.Minute}
		batchCtx, release, err := st.ReserveBatch(ctx, l, 2)
		if err != nil {
			t.Fatalf("ReserveBatch: %v", err)
		}
		tmw := ToolLimits(l, st)
		for i := range 2 {
			if _, err := tmw(okTool)(batchCtx, types.ToolUse{}); err != nil {
				t.Fatalf("batch call %d: %v", i, err)
			}
		}
		if got := st.Snapshot().ToolUses; got != 2 {
			t.Fatalf("toolUses = %d, want 2 (reserved once)", got)
		}
		release()
	})

	t.Run("releasing a refused batch after partial execution refunds spent slots", func(t *testing.T) {
		st := NewLimitsState()
		ctx := WithLimitsState(context.Background(), st)
		l := types.RunLimits{MaxToolCalls: 3, MaxWallClock: time.Minute}
		batchCtx, release, err := st.ReserveBatch(ctx, l, 3)
		if err != nil {
			t.Fatalf("ReserveBatch: %v", err)
		}
		tmw := ToolLimits(l, st)
		if _, err := tmw(okTool)(batchCtx, types.ToolUse{}); err != nil {
			t.Fatalf("first batch call: %v", err)
		}
		release()
		if got := st.Snapshot().ToolUses; got != 0 {
			t.Fatalf("toolUses = %d, want 0 after release", got)
		}
	})

	t.Run("a tool cut by the wall clock reports MaxWallClock", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			st := NewLimitsState()
			ctx := WithLimitsState(context.Background(), st)
			l := types.RunLimits{MaxWallClock: time.Minute}
			tmw := ToolLimits(l, st)
			slow := types.ToolFunc(func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
				time.Sleep(2 * time.Minute)
				return types.ToolResult{}, context.DeadlineExceeded
			})
			_, err := tmw(slow)(ctx, types.ToolUse{})
			var over *types.LimitExceededError
			if !errors.As(err, &over) || over.Limit != "MaxWallClock" {
				t.Fatalf("err = %v, want *LimitExceededError{Limit: MaxWallClock}", err)
			}
		})
	})

	t.Run("concurrent independent ledgers never share counters", func(t *testing.T) {
		var wg sync.WaitGroup
		l := types.RunLimits{MaxCost: 0.03, MaxWallClock: time.Minute}
		mw := Limits(l, testPricing, nil)
		for run := 0; run < 8; run++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				st := NewLimitsState()
				ctx := WithLimitsState(context.Background(), st)
				for i := 0; i < 2; i++ {
					if err := collect(mw(spendModel(0.01))(ctx, types.ModelRequest{})); err != nil {
						t.Errorf("run: %v", err)
						return
					}
				}
				if got := st.Snapshot().Cost; got != 0.02 {
					t.Errorf("cost = %v, want 0.02", got)
				}
			}()
		}
		wg.Wait()
	})
}
