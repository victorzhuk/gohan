package gohan

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/cache"
)

func TestCacheUsageReported(t *testing.T) {
	pricing := types.Pricing{Input: 1, CachedInput: 0.1, CacheWrite: 2}
	model := func(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "x", Usage: &types.Usage{InputTokens: 10}}, nil)
		}
	}
	mark := func(hit bool) types.ModelRequest {
		req := types.ModelRequest{Messages: []types.Message{{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}}}
		return cache.Mark(req, cache.Outcome{Hit: hit, Tokens: 40})
	}

	t.Run("hit fills cached input and leaves the write at zero", func(t *testing.T) {
		st := chains.NewLimitsState()
		mw := chains.Limits(types.RunLimits{MaxWallClock: time.Minute}, pricing, st)
		for _, err := range mw(model)(context.Background(), mark(true)) {
			if err != nil {
				t.Fatalf("model: %v", err)
			}
		}
		if want := 10.0 + 40*0.1; st.TreeCost() != want {
			t.Fatalf("cost = %v, want %v with 40 cached tokens", st.TreeCost(), want)
		}
	})

	t.Run("repopulating miss fills the cache write and leaves the read at zero", func(t *testing.T) {
		st := chains.NewLimitsState()
		mw := chains.Limits(types.RunLimits{MaxWallClock: time.Minute}, pricing, st)
		for _, err := range mw(model)(context.Background(), mark(false)) {
			if err != nil {
				t.Fatalf("model: %v", err)
			}
		}
		if want := 10.0 + 40*2; st.TreeCost() != want {
			t.Fatalf("cost = %v, want %v with 40 written tokens", st.TreeCost(), want)
		}
	})

	t.Run("unmarked request prices plain input only", func(t *testing.T) {
		st := chains.NewLimitsState()
		mw := chains.Limits(types.RunLimits{MaxWallClock: time.Minute}, pricing, st)
		for _, err := range mw(model)(context.Background(), types.ModelRequest{}) {
			if err != nil {
				t.Fatalf("model: %v", err)
			}
		}
		if st.TreeCost() != 10 {
			t.Fatalf("cost = %v, want 10", st.TreeCost())
		}
	})
}

func TestCacheWriteSocialized(t *testing.T) {
	model := func(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "x", Usage: &types.Usage{InputTokens: 10}}, nil)
		}
	}
	mark := func(hit bool) types.ModelRequest {
		req := types.ModelRequest{Messages: []types.Message{{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}}}
		return cache.Mark(req, cache.Outcome{Hit: hit, Tokens: 40})
	}
	run := func(t *testing.T, p types.Pricing) float64 {
		st := chains.NewLimitsState()
		mw := chains.Limits(types.RunLimits{MaxWallClock: time.Minute}, p, st)
		for _, hit := range []bool{false, true} {
			for _, err := range mw(model)(context.Background(), mark(hit)) {
				if err != nil {
					t.Fatalf("model: %v", err)
				}
			}
		}
		return st.TreeCost()
	}
	base := types.Pricing{Input: 1, CachedInput: 0.1, CacheWrite: 2}
	socialized := base
	socialized.SocializeCacheWrites = true

	t.Run("cache write socialized", func(t *testing.T) {
		if got := run(t, socialized); got != 2*(10.0+40*0.1) {
			t.Fatalf("cost = %v, want %v with the write priced at the cached rate", got, 2*(10.0+40*0.1))
		}
	})
	t.Run("lifecycle.cache-write-socialized", func(t *testing.T) {
		if got := run(t, socialized); got != 2*(10.0+40*0.1) {
			t.Fatalf("socialized run cost = %v, want %v with the write priced at the cached rate", got, 2*(10.0+40*0.1))
		}
		if got := run(t, base); got != 10.0+40*2+10.0+40*0.1 {
			t.Fatalf("flag-off run cost = %v, want %v with the write premium kept", got, 10.0+40*2+10.0+40*0.1)
		}
	})
	t.Run("cache write premium kept", func(t *testing.T) {
		if got := run(t, base); got != 10.0+40*2+10.0+40*0.1 {
			t.Fatalf("cost = %v, want %v with the write premium kept", got, 10.0+40*2+10.0+40*0.1)
		}
	})
}
