package hedge

import (
	"context"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

type fakeModel struct {
	name string
	prof types.ModelProfile
	gen  func(ctx context.Context, yield func(types.ModelChunk, error) bool)
}

func (m *fakeModel) Profile() types.ModelProfile {
	if m.prof.Name == "" {
		p := m.prof
		p.Name = m.name
		p.Caps.Streaming = true
		return p
	}
	return m.prof
}

func (m *fakeModel) Generate(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if m.gen != nil {
			m.gen(ctx, yield)
			return
		}
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "ok"}, nil)
	}
}

func collect(seq iter.Seq2[types.ModelChunk, error]) ([]types.ModelChunk, error) {
	var (
		out []types.ModelChunk
		err error
	)
	for c, e := range seq {
		if e != nil {
			err = e
			break
		}
		out = append(out, c)
	}
	return out, err
}

func hedgeTargets(models ...types.Model) func() []types.Model {
	return func() []types.Model { return models }
}

func callWith(t *testing.T, mw types.ModelMiddleware, ctx context.Context, req types.ModelRequest, next types.ModelFunc) ([]types.ModelChunk, error) {
	t.Helper()
	return collect(mw(next)(ctx, req))
}

func TestHedgePolicy(t *testing.T) {
	t.Run("zero-policy-is-inert-without-targets", func(t *testing.T) {
		mw := New(HedgePolicy{})
		chunks, err := callWith(t, mw, context.Background(), types.ModelRequest{},
			func(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
				return func(yield func(types.ModelChunk, error) bool) {
					yield(types.ModelChunk{Kind: types.DeltaText, Delta: "hi"}, nil)
				}
			})
		if err != nil || len(chunks) != 1 {
			t.Fatalf("chunks = %d err = %v, want passthrough", len(chunks), err)
		}
	})
	t.Run("options-compose", func(t *testing.T) {
		st := chains.NewLimitsState()
		_ = New(HedgePolicy{After: time.Second},
			WithLedger(st, types.Pricing{}),
			WithLatency(func(string) time.Duration { return time.Second }),
			WithTelemetry(nil),
		)
	})
}

func TestHedgeTimer(t *testing.T) {
	t.Run("fires-after-latency-source-p90-clamped-to-floor", func(t *testing.T) {
		slow := &fakeModel{name: "a", gen: func(ctx context.Context, yield func(types.ModelChunk, error) bool) {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
				yield(types.ModelChunk{Kind: types.DeltaText, Delta: "slow"}, nil)
			}
		}}
		fast := &fakeModel{name: "b"}
		mw := New(HedgePolicy{Targets: hedgeTargets(slow, fast)},
			WithLatency(func(string) time.Duration { return 20 * time.Millisecond }))
		start := time.Now()
		chunks, _ := callWith(t, mw, context.Background(), types.ModelRequest{}, slow.Generate)
		if len(chunks) == 0 || chunks[0].Delta != "ok" {
			t.Fatalf("winner = %v, want the fast endpoint's chunk", chunks)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("hedge fired after %v, want the 20ms P90 not the floor", elapsed)
		}
	})
	t.Run("floor-holds-without-source", func(t *testing.T) {
		slow := &fakeModel{name: "a", gen: func(ctx context.Context, yield func(types.ModelChunk, error) bool) {
			<-ctx.Done()
		}}
		fast := &fakeModel{name: "b"}
		mw := New(HedgePolicy{Targets: hedgeTargets(slow, fast), After: 30 * time.Millisecond})
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = callWith(t, mw, context.Background(), types.ModelRequest{}, slow.Generate)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("hedge never completed")
		}
	})
	t.Run("metadata-only-first-chunk-does-not-stop-timer", func(t *testing.T) {
		primary := &fakeModel{name: "a", gen: func(ctx context.Context, yield func(types.ModelChunk, error) bool) {
			yield(types.ModelChunk{Kind: types.DeltaReasoning, Delta: "thinking"}, nil)
			yield(types.ModelChunk{Kind: types.DeltaReasoning, Usage: &types.Usage{InputTokens: 1}}, nil)
			select {
			case <-ctx.Done():
			case <-time.After(500 * time.Millisecond):
				yield(types.ModelChunk{Kind: types.DeltaText, Delta: "late"}, nil)
			}
		}}
		second := &fakeModel{name: "b"}
		mw := New(HedgePolicy{Targets: hedgeTargets(primary, second), After: 30 * time.Millisecond})
		chunks, _ := callWith(t, mw, context.Background(), types.ModelRequest{}, primary.Generate)
		if len(chunks) == 0 || chunks[0].Delta != "ok" {
			t.Fatalf("winner = %v, want the second attempt's body chunk", chunks)
		}
	})
}

func TestHedgeAttempts(t *testing.T) {
	t.Run("chains.hedge-fires-on-ttft", func(t *testing.T) {
		primary := &fakeModel{name: "a", gen: func(ctx context.Context, yield func(types.ModelChunk, error) bool) {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
				yield(types.ModelChunk{Kind: types.DeltaText, Delta: "slow"}, nil)
			}
		}}
		second := &fakeModel{name: "b"}
		mw := New(HedgePolicy{Targets: hedgeTargets(primary, second), After: 40 * time.Millisecond})
		chunks, _ := callWith(t, mw, context.Background(), types.ModelRequest{}, primary.Generate)
		if len(chunks) == 0 || chunks[0].Delta != "ok" {
			t.Fatalf("winner = %v, want the second attempt's chunk", chunks)
		}
	})
	t.Run("chains.hedge-never-same-endpoint", func(t *testing.T) {
		only := &fakeModel{name: "a"}
		mw := New(HedgePolicy{Targets: hedgeTargets(only), After: 10 * time.Millisecond})
		start := time.Now()
		chunks, _ := callWith(t, mw, context.Background(), types.ModelRequest{}, only.Generate)
		if len(chunks) != 1 || chunks[0].Delta != "ok" {
			t.Fatalf("chunks = %v, want the single endpoint's own response", chunks)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("inert step took %v", elapsed)
		}
	})
	t.Run("same-name-target-skipped", func(t *testing.T) {
		primary := &fakeModel{name: "a", gen: func(ctx context.Context, yield func(types.ModelChunk, error) bool) {
			<-ctx.Done()
		}}
		same := &fakeModel{name: "a"}
		other := &fakeModel{name: "b"}
		mw := New(HedgePolicy{Targets: hedgeTargets(primary, same, other), After: 20 * time.Millisecond})
		chunks, _ := callWith(t, mw, context.Background(), types.ModelRequest{}, primary.Generate)
		if len(chunks) == 0 || chunks[0].Delta != "ok" {
			t.Fatalf("winner = %v, want the differently named target", chunks)
		}
	})
}

func TestHedgeLoser(t *testing.T) {
	t.Run("chains.hedge-loser-cancelled-and-charged", func(t *testing.T) {
		var (
			mu       sync.Mutex
			primDone bool
		)
		primary := &fakeModel{name: "a", gen: func(ctx context.Context, yield func(types.ModelChunk, error) bool) {
			yield(types.ModelChunk{Kind: types.DeltaReasoning, Usage: &types.Usage{InputTokens: 10, OutputTokens: 5}}, nil)
			<-ctx.Done()
			mu.Lock()
			primDone = true
			mu.Unlock()
		}}
		second := &fakeModel{name: "b"}
		st := chains.NewLimitsState()
		pricing := types.Pricing{Input: 0.01, Output: 0.02}
		mw := New(HedgePolicy{Targets: hedgeTargets(primary, second), After: 20 * time.Millisecond}, WithLedger(st, pricing))
		chunks, _ := callWith(t, mw, context.Background(), types.ModelRequest{}, primary.Generate)
		if len(chunks) == 0 || chunks[0].Delta != "ok" {
			t.Fatalf("winner = %v, want the second attempt", chunks)
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			done := primDone
			mu.Unlock()
			if done {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("primary attempt's context was never cancelled")
			}
			time.Sleep(time.Millisecond)
		}
		want := 10*0.01 + 5*0.02
		if got := st.Cost(); got != want {
			t.Fatalf("ledger cost = %v, want the loser's %v only", got, want)
		}
	})
}

func TestHedgeBudget(t *testing.T) {
	t.Run("chains.hedge-budget-cap", func(t *testing.T) {
		slow := &fakeModel{name: "a", gen: func(ctx context.Context, yield func(types.ModelChunk, error) bool) {
			select {
			case <-ctx.Done():
			case <-time.After(50 * time.Millisecond):
				yield(types.ModelChunk{Kind: types.DeltaText, Delta: "slow"}, nil)
			}
		}}
		fast := &fakeModel{name: "b"}
		counts := map[string]int64{}
		var mu sync.Mutex
		tel := &fakeTel{onCount: func(name string) {
			mu.Lock()
			counts[name]++
			mu.Unlock()
		}}
		mw := New(HedgePolicy{
			Targets: hedgeTargets(slow, fast),
			After:   10 * time.Millisecond,
			Budget:  0.05,
		}, WithTelemetry(tel))
		info := types.RunInfo{RunID: "r1"}
		ctx := types.WithRunInfo(context.Background(), info)
		fired := 0
		for range 30 {
			chunks, _ := callWith(t, mw, ctx, types.ModelRequest{}, slow.Generate)
			if len(chunks) > 0 && chunks[0].Delta == "ok" {
				fired++
			}
		}
		if fired != 2 {
			t.Fatalf("hedges fired = %d, want 2 with a 0.05 bucket capped at one token", fired)
		}
		mu.Lock()
		defer mu.Unlock()
		if counts[MetricBudgetExhausted] != 28 {
			t.Fatalf("budget_exhausted = %d, want 28", counts[MetricBudgetExhausted])
		}
	})
	t.Run("zero-tag-rate-never-hedges", func(t *testing.T) {
		primary := &fakeModel{name: "a", gen: func(ctx context.Context, yield func(types.ModelChunk, error) bool) {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}}
		second := &fakeModel{name: "b"}
		mw := New(HedgePolicy{
			Targets:     hedgeTargets(primary, second),
			After:       10 * time.Millisecond,
			Budget:      1,
			BudgetByTag: func(types.CostTags) float64 { return 0 },
		})
		ctx := types.WithRunInfo(context.Background(), types.RunInfo{RunID: "r2"})
		chunks, _ := callWith(t, mw, ctx, types.ModelRequest{}, primary.Generate)
		if len(chunks) != 0 {
			t.Fatalf("chunks = %d, want none: zero tag rate must never hedge", len(chunks))
		}
	})
	t.Run("separate-runs-have-separate-buckets", func(t *testing.T) {
		primary := &fakeModel{name: "a", gen: func(ctx context.Context, yield func(types.ModelChunk, error) bool) {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}}
		second := &fakeModel{name: "b"}
		mw := New(HedgePolicy{Targets: hedgeTargets(primary, second), After: 10 * time.Millisecond, Budget: 0.05})
		for _, run := range []string{"r1", "r2"} {
			ctx := types.WithRunInfo(context.Background(), types.RunInfo{RunID: run})
			chunks, _ := callWith(t, mw, ctx, types.ModelRequest{}, primary.Generate)
			if len(chunks) == 0 {
				t.Fatalf("run %s: first hedge must fire on a full bucket", run)
			}
		}
	})
}

func TestHedgeProviderTools(t *testing.T) {
	t.Run("chains.hedge-refused-with-provider-tools", func(t *testing.T) {
		primary := &fakeModel{name: "a", gen: func(ctx context.Context, yield func(types.ModelChunk, error) bool) {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "primary"}, nil)
		}}
		second := &fakeModel{name: "b"}
		counts := map[string]int64{}
		var mu sync.Mutex
		tel := &fakeTel{onCount: func(name string) {
			mu.Lock()
			counts[name]++
			mu.Unlock()
		}}
		mw := New(HedgePolicy{Targets: hedgeTargets(primary, second), After: 10 * time.Millisecond}, WithTelemetry(tel))
		req := types.ModelRequest{Tools: []types.ToolSpec{{Name: "search", Executor: types.ByProvider}}}
		chunks, _ := callWith(t, mw, context.Background(), req, primary.Generate)
		if len(chunks) != 1 || chunks[0].Delta != "primary" {
			t.Fatalf("chunks = %v, want the primary's response unchanged", chunks)
		}
		mu.Lock()
		defer mu.Unlock()
		if counts[MetricRefused] != 1 {
			t.Fatalf("refused count = %d, want 1", counts[MetricRefused])
		}
	})
}

func TestHedgeIneligible(t *testing.T) {
	cases := []struct {
		name  string
		prof  types.ModelProfile
		short string
	}{
		{"no-streaming", types.ModelProfile{Name: "a", Caps: types.Caps{Streaming: false}}, "no_streaming"},
		{"batch", types.ModelProfile{Name: "a", Caps: types.Caps{Streaming: true}, LatencyClass: types.Batch}, "batch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			primary := &fakeModel{name: "a", prof: tc.prof}
			second := &fakeModel{name: "b"}
			var refused int64
			tel := &fakeTel{onCount: func(string) { refused++ }}
			mw := New(HedgePolicy{Targets: hedgeTargets(primary, second), After: 10 * time.Millisecond}, WithTelemetry(tel))
			chunks, _ := callWith(t, mw, context.Background(), types.ModelRequest{}, primary.Generate)
			if len(chunks) != 1 {
				t.Fatalf("chunks = %d, want the primary alone", len(chunks))
			}
			if refused != 1 {
				t.Fatalf("refusals = %d, want 1 for %s", refused, tc.short)
			}
		})
	}
}

func TestSink(t *testing.T) {
	t.Run("records-attempts-and-winner", func(t *testing.T) {
		primary := &fakeModel{name: "a", gen: func(ctx context.Context, yield func(types.ModelChunk, error) bool) {
			<-ctx.Done()
		}}
		second := &fakeModel{name: "b"}
		mw := New(HedgePolicy{Targets: hedgeTargets(primary, second), After: 10 * time.Millisecond})
		sink := &Sink{}
		ctx := WithSink(context.Background(), sink)
		_, _ = callWith(t, mw, ctx, types.ModelRequest{}, primary.Generate)
		attempts, endpoint := sink.Outcome()
		if attempts != 2 || endpoint != "b" {
			t.Fatalf("attempts = %d endpoint = %q, want 2 and b", attempts, endpoint)
		}
	})
	t.Run("unfired-reports-single-attempt", func(t *testing.T) {
		only := &fakeModel{name: "a"}
		mw := New(HedgePolicy{Targets: hedgeTargets(only), After: time.Hour})
		sink := &Sink{}
		_, _ = callWith(t, mw, WithSink(context.Background(), sink), types.ModelRequest{}, only.Generate)
		if attempts, _ := sink.Outcome(); attempts != 1 {
			t.Fatalf("attempts = %d, want 1", attempts)
		}
	})
}

type fakeTel struct {
	onCount func(name string)
}

func (f *fakeTel) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (f *fakeTel) Count(_ context.Context, name string, _ int64, _ ...types.Attr) {
	if f.onCount != nil {
		f.onCount(name)
	}
}

func (f *fakeTel) Record(context.Context, string, float64, ...types.Attr) {}
