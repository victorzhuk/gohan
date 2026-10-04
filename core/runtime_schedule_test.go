package gohan

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// scheduleStack builds a Stack with the given options and resolves the
// scheduling plan for one flow against a profile that declares parallel
// tools, so the cap and the hint arrive through the resolved strategy.
func scheduleStack(t *testing.T, opts ...Option) runtime.SchedulerConfig {
	t.Helper()
	stack, err := Build(opts...)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	profiles := map[string]types.ModelProfile{
		"m": {Name: "m", Caps: types.Caps{ParallelTools: true}},
	}
	plan, err := stack.ResolveStrategies(FlowRequest{Name: "flow"}, profiles, "m")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return runtime.SchedulerConfig{
		Parallel:    plan.ParallelTools,
		MaxParallel: plan.MaxParallelTools,
	}
}

func effectLookup(specs map[string]types.Effect) func(types.ToolUse) types.Effect {
	return func(call types.ToolUse) types.Effect { return specs[call.Name] }
}

func calls(names ...string) []types.ToolUse {
	out := make([]types.ToolUse, len(names))
	for i, n := range names {
		out[i] = types.ToolUse{ID: n, Name: n}
	}
	return out
}

// recExecutor records the call order and tracks peak in-flight concurrency
// with an atomic, never with time.
type recExecutor struct {
	mu     sync.Mutex
	order  []string
	flight atomic.Int32
	peak   atomic.Int32
}

func (r *recExecutor) exec(_ context.Context, call types.ToolUse) (types.ToolResult, error) {
	cur := r.flight.Add(1)
	for {
		p := r.peak.Load()
		if cur <= p || r.peak.CompareAndSwap(p, cur) {
			break
		}
	}
	defer r.flight.Add(-1)
	r.mu.Lock()
	r.order = append(r.order, call.ID)
	r.mu.Unlock()
	return types.ToolResult{ID: call.ID, Outcome: types.Succeeded}, nil
}

// gateExecutor blocks each call on the batch's release channel before it
// records; inside a synctest bubble the blocked goroutines cost nothing.
type gateExecutor struct {
	gate    chan struct{}
	entered chan string
	rec     recExecutor
	flight  atomic.Int32
	peak    atomic.Int32
}

func (g *gateExecutor) exec(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
	// The peak is counted at gate entry, before the call blocks, so the
	// invariant holds while the batch is held open.
	cur := g.flight.Add(1)
	for {
		p := g.peak.Load()
		if cur <= p || g.peak.CompareAndSwap(p, cur) {
			break
		}
	}
	defer g.flight.Add(-1)
	g.entered <- call.ID
	<-g.gate
	return g.rec.exec(ctx, call)
}

// fakeJournal is a local stand-in for the journal port: it records the
// reserve/complete order and replays a stored entry for a repeated key.
type fakeJournal struct {
	mu     sync.Mutex
	ended  map[types.CallKey]stores.Entry
	seq    []string
	res    map[types.CallKey]types.ToolResult
	fps    map[types.CallKey]stores.Fingerprint
	inOpen int
}

func newFakeJournal() *fakeJournal {
	return &fakeJournal{ended: map[types.CallKey]stores.Entry{}, res: map[types.CallKey]types.ToolResult{}, fps: map[types.CallKey]stores.Fingerprint{}}
}

func (j *fakeJournal) Reserve(_ context.Context, k types.CallKey, fp stores.Fingerprint) (stores.Entry, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq = append(j.seq, "reserve:"+k.CallID)
	j.inOpen++
	if e, ok := j.ended[k]; ok {
		return e, true, nil
	}
	j.fps[k] = fp
	return stores.Entry{}, false, nil
}

// ByFingerprint answers the replay lookup: it returns every completed
// entry for the session whose fingerprint matches.
func (j *fakeJournal) ByFingerprint(_ context.Context, sessionID string, fp stores.Fingerprint) ([]stores.Entry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []stores.Entry
	for k, got := range j.fps {
		if k.SessionID == sessionID && got == fp {
			if e, ok := j.ended[k]; ok {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

func (j *fakeJournal) Complete(_ context.Context, k types.CallKey, res types.ToolResult) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq = append(j.seq, "complete:"+k.CallID)
	j.inOpen--
	j.ended[k] = stores.Entry{State: stores.Completed, Key: k.CallID, Result: res}
	return nil
}

// shieldFake wraps the inner executor and tracks that it was applied to
// every side-effect call; overlap is counted, never timed.
type shieldFake struct {
	inner runtime.ToolFunc
}

func (s *shieldFake) apply(next runtime.ToolFunc) runtime.ToolFunc {
	return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
		s.inner = next
		return next(ctx, call)
	}
}

func TestRuntimeScheduling(t *testing.T) {
	readOnly := map[string]types.Effect{
		"search": types.ReadOnly, "list": types.ReadOnly, "peek": types.ReadOnly,
		"r1": types.ReadOnly, "r2": types.ReadOnly, "r3": types.ReadOnly,
		"r4": types.ReadOnly, "r5": types.ReadOnly, "r6": types.ReadOnly,
		"r7": types.ReadOnly, "r8": types.ReadOnly, "r9": types.ReadOnly,
		"ra": types.ReadOnly, "rb": types.ReadOnly, "rc": types.ReadOnly,
	}
	t.Run("runtime.readonly-parallel", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			cfg := scheduleStack(t, MaxParallelTools(16))
			cfg.EffectOf = effectLookup(readOnly)
			gate := make(chan struct{})
			ge := &gateExecutor{gate: gate, entered: make(chan string, len(readOnly))}
			var (
				done  = make(chan error, 1)
				got   []types.ToolResult
				names []string
			)
			for n := range readOnly {
				names = append(names, n)
			}
			go func() {
				var err error
				got, err = runtime.Schedule(context.Background(), calls(names...), ge.exec, cfg)
				done <- err
			}()
			synctest.Wait()
			if len(ge.entered) != len(readOnly) {
				t.Fatalf("entered %d read-only calls, want %d concurrently", len(ge.entered), len(readOnly))
			}
			close(gate)
			if err := <-done; err != nil {
				t.Fatalf("schedule: %v", err)
			}
			synctest.Wait()
			if len(got) != len(names) {
				t.Fatalf("got %d results, want %d", len(got), len(names))
			}
		})
	})

	t.Run("runtime.parallel-tools-cap", func(t *testing.T) {
		cfg := scheduleStack(t, MaxParallelTools(3))
		cfg.EffectOf = effectLookup(readOnly)
		gate := make(chan struct{})
		ge := &gateExecutor{gate: gate, entered: make(chan string, 12)}
		names := []string{"r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8", "r9", "ra", "rb", "rc"}
		done := make(chan error, 1)
		go func() {
			_, err := runtime.Schedule(context.Background(), calls(names...), ge.exec, cfg)
			done <- err
		}()
		// The semaphore admits exactly the cap; the next three entries
		// prove the cap is reached, and none beyond it can enter while
		// the gate holds.
		for i := 0; i < 3; i++ {
			<-ge.entered
		}
		if got := ge.peak.Load(); got != 3 {
			t.Fatalf("peak concurrency %d, want cap 3", got)
		}
		close(gate)
		if err := <-done; err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if got := ge.peak.Load(); got > 3 {
			t.Fatalf("peak concurrency %d exceeded cap 3", got)
		}
	})

	t.Run("runtime.side-effects-sequential", func(t *testing.T) {
		cfg := scheduleStack(t, MaxParallelTools(8))
		effects := map[string]types.Effect{"w1": types.SideEffect, "w2": types.SideEffect, "w3": types.SideEffect}
		cfg.EffectOf = effectLookup(effects)
		j := newFakeJournal()
		cfg.Journal = j
		cfg.Key = func(call types.ToolUse) types.CallKey { return types.CallKey{SessionID: "s", CallID: call.ID} }
		cfg.Fingerprint = func(types.ToolUse) stores.Fingerprint { return "fp" }
		rec := &recExecutor{}
		cfg.Shield = (&shieldFake{}).apply
		got, err := runtime.Schedule(context.Background(), calls("w1", "w2", "w3"), rec.exec, cfg)
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if rec.peak.Load() != 1 {
			t.Fatalf("peak concurrency %d, want 1 for side effects", rec.peak.Load())
		}
		for i, call := range calls("w1", "w2", "w3") {
			if got[i].ID != call.ID {
				t.Fatalf("result %d is %q, want %q in call order", i, got[i].ID, call.ID)
			}
		}
		want := []string{
			"reserve:w1", "complete:w1",
			"reserve:w2", "complete:w2",
			"reserve:w3", "complete:w3",
		}
		if len(j.seq) != len(want) {
			t.Fatalf("journal sequence %v, want %v", j.seq, want)
		}
		for i := range want {
			if j.seq[i] != want[i] {
				t.Fatalf("journal step %d is %q, want %q", i, j.seq[i], want[i])
			}
		}
	})

	t.Run("runtime.sequential-tools-hint", func(t *testing.T) {
		cfg := scheduleStack(t, SequentialTools(), MaxParallelTools(8))
		if cfg.Parallel {
			t.Fatal("resolved strategy is parallel despite SequentialTools")
		}
		if cfg.MaxParallel != 1 {
			t.Fatalf("resolved cap %d, want 1 under the sequential hint", cfg.MaxParallel)
		}
		cfg.EffectOf = effectLookup(readOnly)
		gate := make(chan struct{})
		ge := &gateExecutor{gate: gate, entered: make(chan string, 1)}
		names := []string{"r1", "r2", "r3", "r4", "r5", "r6"}
		done := make(chan error, 1)
		go func() {
			var err error
			_, err = runtime.Schedule(context.Background(), calls(names...), ge.exec, cfg)
			done <- err
		}()
		for i, want := range names {
			if got := <-ge.entered; got != want {
				t.Fatalf("call %d entered is %q, want %q", i, got, want)
			}
			if len(ge.entered) != 0 {
				t.Fatal("second call entered while the first holds the batch")
			}
			gate <- struct{}{}
		}
		if err := <-done; err != nil {
			t.Fatalf("schedule: %v", err)
		}
	})

	t.Run("call-order", func(t *testing.T) {
		cfg := scheduleStack(t, MaxParallelTools(4))
		effects := map[string]types.Effect{
			"search": types.ReadOnly, "scrape": types.ReadOnly,
			"deploy": types.SideEffect, "mail": types.SideEffect,
		}
		cfg.EffectOf = effectLookup(effects)
		// The read-only calls unblock in reverse order; the results must
		// still come back in call order.
		gates := map[string]chan struct{}{
			"search": make(chan struct{}), "scrape": make(chan struct{}),
		}
		exec := func(_ context.Context, call types.ToolUse) (types.ToolResult, error) {
			if g, ok := gates[call.ID]; ok {
				<-g
			}
			return types.ToolResult{ID: call.ID, Outcome: types.Succeeded}, nil
		}
		done := make(chan []types.ToolResult, 1)
		order := []string{"search", "deploy", "scrape", "mail"}
		go func() {
			res, _ := runtime.Schedule(context.Background(), calls(order...), exec, cfg)
			done <- res
		}()
		close(gates["scrape"])
		close(gates["search"])
		got := <-done
		for i, id := range order {
			if got[i].ID != id {
				t.Fatalf("result %d is %q, want %q in call order", i, got[i].ID, id)
			}
		}
	})
}
