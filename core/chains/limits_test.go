package chains

import (
	"context"
	"errors"
	"iter"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestChainLimits(t *testing.T) {
	limits := func(maxCost float64) types.RunLimits {
		return types.RunLimits{MaxTurns: 10, MaxToolCalls: 10, MaxCost: maxCost, MaxWallClock: time.Minute, SoftRatio: 0.8}
	}
	pending := types.Pricing{Input: 0.04}
	spend := func(n int) types.ModelFunc {
		return func(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			return func(yield func(types.ModelChunk, error) bool) {
				yield(types.ModelChunk{Kind: types.DeltaText, Delta: "x", Usage: &types.Usage{InputTokens: n}}, nil)
			}
		}
	}
	call := func(t *testing.T, mw types.ModelMiddleware, next types.ModelFunc) error {
		t.Helper()
		_, err := collect(mw(next)(context.Background(), types.ModelRequest{}))
		return err
	}

	t.Run("limits.hard-cost-abort", func(t *testing.T) {
		st := NewLimitsState()
		mw := Limits(limits(0.05), pending, st)
		err := call(t, mw, spend(2))
		var over *types.LimitExceededError
		if !errors.As(err, &over) || over.Limit != "MaxCost" {
			t.Fatalf("err = %v, want *LimitExceededError{Limit: MaxCost}", err)
		}
		nextCalled := false
		err = call(t, mw, func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			nextCalled = true
			return spend(1)(ctx, req)
		})
		if !errors.As(err, &over) || over.Limit != "MaxCost" {
			t.Fatalf("second call err = %v, want abort before the next model call", err)
		}
		if nextCalled {
			t.Fatal("next was called after the cost overrun")
		}
		if st.Cost() != 0.08 {
			t.Fatalf("cost = %v, want 0.08", st.Cost())
		}
		warns := st.Warnings()
		if len(warns) != 1 || warns[0].Limit != "MaxCost" || math.Abs(warns[0].Ratio-0.08/0.05) > 1e-9 {
			t.Fatalf("warnings = %+v, want one MaxCost warning at the soft ratio", warns)
		}
	})

	t.Run("limits.wall-clock", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			st := NewLimitsState()
			mw := ToolLimits(types.RunLimits{MaxTurns: 1, MaxToolCalls: 10, MaxWallClock: time.Second}, st)
			var toolCtx context.Context
			tool := mw(func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
				toolCtx = ctx
				select {
				case <-ctx.Done():
					return types.ToolResult{}, ctx.Err()
				case <-time.After(time.Hour):
					return types.ToolResult{}, nil
				}
			})
			_, err := tool(context.Background(), types.ToolUse{})
			var over *types.LimitExceededError
			if !errors.As(err, &over) || over.Limit != "MaxWallClock" {
				t.Fatalf("err = %v, want *LimitExceededError{Limit: MaxWallClock}", err)
			}
			if toolCtx == nil || !errors.Is(toolCtx.Err(), context.DeadlineExceeded) {
				t.Fatalf("tool ctx = %v, want cancelled by the wall clock", toolCtx)
			}
		})
	})

	t.Run("limits.cost-accumulates", func(t *testing.T) {
		st := NewLimitsState()
		mw := Limits(limits(0.10), pending, st)
		if err := call(t, mw, spend(1)); err != nil {
			t.Fatalf("first spend: %v", err)
		}
		if err := call(t, mw, spend(1)); err != nil {
			t.Fatalf("second spend: %v", err)
		}
		err := call(t, mw, spend(1))
		var over *types.LimitExceededError
		if !errors.As(err, &over) || over.Limit != "MaxCost" {
			t.Fatalf("third spend err = %v, want *LimitExceededError{Limit: MaxCost}", err)
		}
		if st.Cost() != 0.12 {
			t.Fatalf("cost = %v, want 0.12", st.Cost())
		}
		// Row 25 owns the root projection: Done.Cost on the root run must
		// equal this sum once the tree accounting lands.
	})

	t.Run("soft ratio does not abort", func(t *testing.T) {
		st := NewLimitsState()
		mw := Limits(limits(1.0), pending, st)
		if err := call(t, mw, spend(20)); err != nil {
			t.Fatalf("err = %v, want a spend under MaxCost to pass", err)
		}
		warns := st.Warnings()
		if len(warns) != 1 || warns[0].Ratio != 0.8 {
			t.Fatalf("warnings = %+v, want one warning at ratio 0.8", warns)
		}
	})

	t.Run("fallback charges once per chunk and twice over two chunks", func(t *testing.T) {
		st := NewLimitsState()
		mw := Limits(limits(1.0), pending, st)
		// The fallback runs inside the limit step: each message tries
		// endpoint A, fails before the first chunk, fails over to
		// endpoint B, and only B's chunk crosses the limit boundary.
		attempts := 0
		next := func(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			return func(yield func(types.ModelChunk, error) bool) {
				attempts += 2
				yield(types.ModelChunk{Kind: types.DeltaText, Delta: "x", Usage: &types.Usage{InputTokens: 1}}, nil)
			}
		}
		if err := call(t, mw, next); err != nil {
			t.Fatalf("first chunk: %v", err)
		}
		if attempts != 2 {
			t.Fatalf("attempts = %d, want two endpoints per message", attempts)
		}
		if st.Cost() != 0.04 {
			t.Fatalf("cost = %v, want one charge of 0.04", st.Cost())
		}
		if err := call(t, mw, next); err != nil {
			t.Fatalf("second chunk: %v", err)
		}
		if attempts != 4 {
			t.Fatalf("attempts = %d, want two endpoints per message", attempts)
		}
		if st.Cost() != 0.08 {
			t.Fatalf("cost = %v, want 0.08 over two chunks", st.Cost())
		}
	})
}

func collect(seq iter.Seq2[types.ModelChunk, error]) ([]types.ModelChunk, error) {
	var chunks []types.ModelChunk
	for c, err := range seq {
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, c)
	}
	return chunks, nil
}
