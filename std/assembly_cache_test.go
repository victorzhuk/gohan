package std

import (
	"context"
	jsonv2 "encoding/json/v2"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/cache"
)

type countingSink struct {
	mu     sync.Mutex
	counts map[string]int64
}

func newCountingSink() *countingSink {
	return &countingSink{counts: map[string]int64{}}
}

func (s *countingSink) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (s *countingSink) Count(_ context.Context, name string, n int64, _ ...types.Attr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[name] += n
}

func (s *countingSink) Record(context.Context, string, float64, ...types.Attr) {}

type buildCounter struct {
	calls int
}

func (p *buildCounter) Slot() types.ContextSlot { return types.SlotStatic }

func (p *buildCounter) Provide(_ context.Context, _ types.RunInfo) ([]types.Block, error) {
	p.calls++
	return []types.Block{types.Text{Text: "static"}}, nil
}

func cacheInput(system string, providers map[types.ContextSlot][]types.ContextProvider) types.AssembleInput {
	return types.AssembleInput{
		Run:       types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1", Subject: "u1"}},
		Profile:   types.ModelProfile{Name: "primary", Version: "v1"},
		System:    []types.Block{types.Text{Text: system}},
		Input:     []types.Message{{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}},
		Providers: providers,
	}
}

func freshCacheInput(system string, providers map[types.ContextSlot][]types.ContextProvider) types.AssembleInput {
	in := cacheInput(system, providers)
	in.System = append([]types.Block(nil), in.System...)
	in.Input = append([]types.Message(nil), in.Input...)
	return in
}

func cacheCtx(t *testing.T, sink *countingSink) context.Context {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	if sink != nil {
		ctx = types.WithTelemetry(ctx, sink)
	}
	return ctx
}

func marshalReq(t *testing.T, req types.ModelRequest) string {
	t.Helper()
	wire := req
	extra := make(map[string]any, len(req.Options.Extra))
	for k, v := range req.Options.Extra {
		if k == chains.ExtraKey {
			continue
		}
		extra[k] = v
	}
	wire.Options.Extra = extra
	// Marshal through the frozen ModelRequest codec (encoding/json/v2),
	// the same path cassette.go hashes: it strips Verify, which has no
	// wire form.
	raw, err := jsonv2.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return string(raw)
}

func TestPrefixCache(t *testing.T) {
	t.Run("cache.key-includes-manifest", func(t *testing.T) {
		store := cache.NewStore()
		sink := newCountingSink()
		p := NewStablePrefix(WithCacheStore(store, cache.ScopeShared))
		ctx := cacheCtx(t, sink)

		if _, err := p.Assemble(ctx, cacheInput("prompt A", nil)); err != nil {
			t.Fatalf("assemble A: %v", err)
		}
		if got := sink.counts["gohan.cache.miss"]; got != 1 {
			t.Fatalf("miss count = %d, want 1 after the first assembly", got)
		}
		if _, err := p.Assemble(ctx, cacheInput("prompt B", nil)); err != nil {
			t.Fatalf("assemble B: %v", err)
		}
		if got := sink.counts["gohan.cache.miss"]; got != 2 {
			t.Fatalf("miss count = %d, want 2 after the changed prompt set", got)
		}
		if got := sink.counts["gohan.cache.hit"]; got != 0 {
			t.Fatalf("hit count = %d, want 0 across different manifests", got)
		}
	})

	t.Run("cache.prefix-miss-repopulates", func(t *testing.T) {
		store := cache.NewStore()
		counter := &buildCounter{}
		providers := map[types.ContextSlot][]types.ContextProvider{types.SlotStatic: {counter}}
		p := NewStablePrefix(WithCacheStore(store, cache.ScopeShared))
		ctx := cacheCtx(t, nil)
		in := cacheInput("prompt A", providers)

		first, err := p.Assemble(ctx, in)
		if err != nil {
			t.Fatalf("first assemble: %v", err)
		}
		second, err := p.Assemble(ctx, in)
		if err != nil {
			t.Fatalf("second assemble: %v", err)
		}
		if counter.calls != 1 {
			t.Fatalf("provider calls = %d, want 1 assembly for the key", counter.calls)
		}
		if marshalReq(t, first) != marshalReq(t, second) {
			t.Fatal("served prefix differs between the two calls")
		}
	})

	t.Run("cache.prefix-hit", func(t *testing.T) {
		store := cache.NewStore()
		counter := &buildCounter{}
		providers := func() map[types.ContextSlot][]types.ContextProvider {
			return map[types.ContextSlot][]types.ContextProvider{types.SlotStatic: {counter}}
		}
		p := NewStablePrefix(WithCacheStore(store, cache.ScopeShared))
		ctx := cacheCtx(t, nil)

		first, err := p.Assemble(ctx, freshCacheInput("prompt A", providers()))
		if err != nil {
			t.Fatalf("first assemble: %v", err)
		}
		// A different input evicts the single-slot memo, so the repeat
		// falls through to the content-keyed store.
		if _, err := p.Assemble(ctx, cacheInput("prompt B", nil)); err != nil {
			t.Fatalf("evicting assemble: %v", err)
		}
		repeat, err := p.Assemble(ctx, freshCacheInput("prompt A", providers()))
		if err != nil {
			t.Fatalf("repeat assemble: %v", err)
		}
		o, ok := chains.OutcomeOf(repeat)
		if !ok || !o.Hit {
			t.Fatalf("outcome = %+v ok=%v, want a hit mark", o, ok)
		}
		mark, ok := chains.OutcomeOf(first)
		if !ok || mark.Hit {
			t.Fatalf("first outcome = %+v ok=%v, want a write mark", mark, ok)
		}
		firstWire := marshalReq(t, first)
		repeatWire := marshalReq(t, repeat)
		if repeatWire != firstWire {
			t.Fatal("stored prefix is not byte-identical to the first assembly")
		}
	})

	t.Run("usage marks report write on a miss and cached read on a hit", func(t *testing.T) {
		store := cache.NewStore()
		p := NewStablePrefix(WithCacheStore(store, cache.ScopeShared))
		ctx := cacheCtx(t, nil)

		miss, err := p.Assemble(ctx, cacheInput("prompt A", nil))
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		o, ok := chains.OutcomeOf(miss)
		if !ok || o.Hit || o.Tokens <= 0 {
			t.Fatalf("miss outcome = %+v ok=%v, want a write mark with tokens", o, ok)
		}
		if miss.Options.MaxTokens != 0 {
			t.Fatalf("assembled options drifted: %+v", miss.Options)
		}

		hit, err := p.Assemble(ctx, cacheInput("prompt A", nil))
		if err != nil {
			t.Fatalf("repeat assemble: %v", err)
		}
		o, ok = chains.OutcomeOf(hit)
		if !ok || !o.Hit || o.Tokens <= 0 {
			t.Fatalf("hit outcome = %+v ok=%v, want a hit mark with tokens", o, ok)
		}
	})

	t.Run("zero value memoizes without consulting a store", func(t *testing.T) {
		sink := newCountingSink()
		ctx := cacheCtx(t, sink)
		in := cacheInput("prompt A", nil)

		if _, err := (StablePrefix{}).Assemble(ctx, in); err != nil {
			t.Fatalf("first assemble: %v", err)
		}
		if _, err := (StablePrefix{}).Assemble(ctx, in); err != nil {
			t.Fatalf("second assemble: %v", err)
		}
		if len(sink.counts) != 0 {
			t.Fatalf("counts = %v, want no cache metrics without a store", sink.counts)
		}
	})

	t.Run("cache.hit-miss-metrics", func(t *testing.T) {
		store := cache.NewStore()
		sink := newCountingSink()
		p := NewStablePrefix(WithCacheStore(store, cache.ScopeShared))
		ctx := cacheCtx(t, sink)
		in := cacheInput("prompt A", nil)

		if _, err := p.Assemble(ctx, in); err != nil {
			t.Fatalf("first assemble: %v", err)
		}
		if _, err := p.Assemble(ctx, in); err != nil {
			t.Fatalf("second assemble: %v", err)
		}
		if got := sink.counts["gohan.cache.miss"]; got != 1 {
			t.Fatalf("gohan.cache.miss = %d, want 1", got)
		}
		if got := sink.counts["gohan.cache.hit"]; got != 1 {
			t.Fatalf("gohan.cache.hit = %d, want 1", got)
		}
	})
}
