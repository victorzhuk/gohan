package gohan

import (
	"context"
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// stallTestRT scripts the run a stall test drives. Step 0 emits one event and
// continues; step 1 blocks on the gate and, once released, honors a
// cancelled context as the preemption signal a safe point sees. Further
// steps come from steps; past them the run finishes.
type stallTestRT struct {
	mu   sync.Mutex
	i    int
	gate chan struct{}
	done chan struct{}

	steps []func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error)
}

func (r *stallTestRT) Name() string                         { return "stall.test" }
func (r *stallTestRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *stallTestRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *stallTestRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	i := r.i
	r.i++
	r.mu.Unlock()
	if i < len(r.steps) {
		return r.steps[i](ctx, st)
	}
	if r.gate != nil && i == 1 {
		select {
		case <-r.gate:
		case <-ctx.Done():
		}
	}
	if err := ctx.Err(); err != nil {
		// The safe point converts the preemption signal into a suspension
		// the landed lifecycle persists.
		return st, nil, runtime.Continue, &types.SuspendError{Reason: types.Preempted}
	}
	if i == 0 {
		return st, []types.Event{types.TextDelta{Turn: 0, Delta: "one"}}, runtime.Continue, nil
	}
	if r.done != nil {
		close(r.done)
	}
	return st, nil, runtime.DoneStatus, nil
}

func (r *stallTestRT) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.i
}

// stallRuns records the runs-store writes the suspension path makes.
type stallRuns struct {
	*stores.MemoryRuns
	mu        sync.Mutex
	runID     string
	suspended []types.ResumeToken
	starts    int
}

func (f *stallRuns) Start(ctx context.Context, r stores.Run, ttl time.Duration) (stores.Lease, error) {
	f.mu.Lock()
	f.starts++
	f.runID = r.RunID
	f.mu.Unlock()
	return f.MemoryRuns.Start(ctx, r, ttl)
}

func (f *stallRuns) Suspend(ctx context.Context, l stores.Lease, t types.ResumeToken) error {
	f.mu.Lock()
	f.suspended = append(f.suspended, t)
	f.mu.Unlock()
	return f.MemoryRuns.Suspend(ctx, l, t)
}

func stallWait(t *testing.T, name string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", name)
		}
		time.Sleep(time.Millisecond)
	}
}

// stallConversation builds a conversation with every store the suspension
// and reattachment paths touch.
func stallConversation(t *testing.T, rt runtime.Runtime, opts ...ConversationOption) (Conversation, *stallRuns, *stores.MemoryEventLog) {
	t.Helper()
	runs := &stallRuns{MemoryRuns: stores.NewMemoryRuns()}
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	stack := &Stack{stores: stores.Stores{SessionLog: log}}
	conv, err := NewConversation(stack, "chat", rt,
		append([]ConversationOption{
			WithConversationRuns(runs),
			WithConversationEventLog(events),
			WithConversationCheckpoints(stores.NewMemoryCheckpoints()),
		}, opts...)...)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	return conv, runs, events
}

func TestStreamStall(t *testing.T) {
	// runStall guards run at a nominal limit with a channel-driven clock:
	// every take resets the guard, and the test's first tick then shows an
	// idle gap longer than the limit.
	runStall := func(g *StallGuard) *StallGuard {
		g.limit = time.Nanosecond
		return g
	}
	ticks := func() chan time.Time { return make(chan time.Time, 4) }

	t.Run("streams.slow-consumer-no-idle-retry", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := &countedStallProvider{fakeProvider: newFakeProvider(
				types.ModelTimeout{FirstChunk: time.Hour, Idle: time.Hour},
				textChunk("a"), textChunk("b"), finishChunk())}
			s := NewModelStream(p)
			n := 0
			for _, err := range s.Generate(context.Background(), types.ModelRequest{}) {
				if err != nil {
					t.Fatalf("chunk %d: unexpected error %v", n, err)
				}
				n++
				// One chunk per idle interval: a slow consumer never paces
				// the provider read, so the idle clock never fires.
				time.Sleep(time.Hour)
			}
			if n != 3 {
				t.Fatalf("got %d chunks, want 3", n)
			}
			if calls := p.calls.Load(); calls != 1 {
				t.Fatalf("model called %d times, want 1", calls)
			}
		})
	})

	t.Run("streams.consumer-stall-preempts", func(t *testing.T) {
		const stall = 10 * time.Millisecond
		rt := &stallTestRT{gate: make(chan struct{}), done: make(chan struct{})}
		runs := &stallRuns{MemoryRuns: stores.NewMemoryRuns()}
		events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		stack := &Stack{
			stores: stores.Stores{SessionLog: log},
			limits: map[string]types.RunLimits{"chat": {ConsumerStall: stall}},
		}
		conv, err := NewConversation(stack, "chat", rt,
			WithConversationRuns(runs),
			WithConversationEventLog(events),
			WithConversationCheckpoints(stores.NewMemoryCheckpoints()),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		ctx := principalCtx(context.Background())

		var got []types.Event
		streamDone := make(chan struct{})
		go func() {
			defer close(streamDone)
			for ev, err := range conv.Send(ctx, "sess-1", userMsg("hi")) {
				if err != nil {
					t.Errorf("stream: %v", err)
					return
				}
				got = append(got, ev)
				if len(got) == 1 {
					// The consumer stops taking events past the stall
					// limit: the run worker keeps driving and the guard
					// preempts the run at the safe point.
					time.Sleep(4 * stall)
				}
			}
		}()
		<-streamDone

		if len(got) < 2 {
			t.Fatalf("got %d events, want at least the delta and the suspension", len(got))
		}
		if _, ok := got[0].(types.TextDelta); !ok {
			t.Fatalf("first event %T, want TextDelta", got[0])
		}
		for _, ev := range got[1 : len(got)-1] {
			if d, ok := ev.(types.Done); ok {
				t.Fatalf("Done %v delivered before the suspension", d)
			}
		}
		s, ok := got[len(got)-1].(types.Suspended)
		if !ok {
			t.Fatalf("last event %T, want Suspended", got[len(got)-1])
		}
		if s.Reason != types.Preempted || s.Token == "" {
			t.Fatalf("suspension reason %q token %q, want preempted with a token", s.Reason, s.Token)
		}
		if runs.SessionLeaseActive(ctx, "sess-1") {
			t.Fatal("lease still active after suspension")
		}
		if calls := rt.calls(); calls != 2 {
			t.Fatalf("run took %d model calls before the suspension, want 2 (no retry)", calls)
		}

		// The client resumes the preempted run with Continue().
		resumed := 0
		for ev, err := range conv.Resume(ctx, s.Token, Continue()) {
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			resumed++
			if d, ok := ev.(types.Done); !ok || d.Reason != types.StopCompleted {
				t.Fatalf("resume event %T, want the completed Done", ev)
			}
		}
		if resumed == 0 {
			t.Fatal("resume produced no events")
		}
	})

	t.Run("streams.consumer-stall-detaches-with-log", func(t *testing.T) {
		const stall = 10 * time.Millisecond
		rt := &stallTestRT{gate: make(chan struct{}), done: make(chan struct{})}
		runs := &stallRuns{MemoryRuns: stores.NewMemoryRuns()}
		events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		stack := &Stack{
			stores: stores.Stores{SessionLog: log},
			limits: map[string]types.RunLimits{"chat": {ConsumerStall: stall}},
		}
		conv, err := NewConversation(stack, "chat", rt,
			WithConversationRuns(runs),
			WithConversationEventLog(events),
			WithConversationCheckpoints(stores.NewMemoryCheckpoints()),
			OnStall(StallDetach),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		ctx := principalCtx(context.Background())

		stalled := make(chan struct{})
		release := make(chan struct{})
		streamDone := make(chan struct{})
		var got []types.Event
		go func() {
			defer close(streamDone)
			for ev, err := range conv.Send(ctx, "sess-1", userMsg("hi")) {
				if err != nil {
					t.Errorf("stream: %v", err)
					return
				}
				got = append(got, ev)
				if len(got) == 1 {
					close(stalled)
					// The consumer stops taking events past the stall
					// limit: the run detaches and attached delivery
					// ends without a terminal event.
					<-release
				}
			}
		}()
		<-stalled
		time.Sleep(4 * stall)
		close(release)
		<-streamDone

		if len(got) != 1 {
			t.Fatalf("attached delivery sent %d events, want only the first delta", len(got))
		}
		if _, ok := got[0].(types.TextDelta); !ok {
			t.Fatalf("first event %T, want TextDelta", got[0])
		}
		runs.mu.Lock()
		suspended, starts := len(runs.suspended), runs.starts
		runs.mu.Unlock()
		if suspended != 0 {
			t.Fatalf("run suspended %d times, want 0", suspended)
		}
		if starts != 1 {
			t.Fatalf("run started %d times, want 1", starts)
		}
		// The detached run keeps its lease while it finishes.
		if !runs.SessionLeaseActive(ctx, "sess-1") {
			t.Fatal("lease released before the detached run finished")
		}

		// The run continues: releasing the gate lets it finish.
		close(rt.gate)
		<-rt.done
		stallWait(t, "run end", func() bool {
			return !runs.SessionLeaseActive(ctx, "sess-1")
		})

		// A reattach from the sequence the consumer last saw delivers
		// the remaining events, ending in the run's single Done.
		var missed []types.Event
		for ev, err := range conv.Attach(ctx, runs.runID, 1) {
			if err != nil {
				t.Fatalf("attach from 1: %v", err)
			}
			missed = append(missed, ev)
		}
		if len(missed) == 0 {
			t.Fatal("reattachment from 1 delivered no events")
		}
		d, ok := missed[len(missed)-1].(types.Done)
		if !ok || d.Reason != types.StopCompleted {
			t.Fatalf("last reattached event %T, want the completed Done", missed[len(missed)-1])
		}
		// The full log holds every event in order, with exactly one Done.
		var all []types.Event
		for ev, err := range conv.Attach(ctx, runs.runID, 0) {
			if err != nil {
				t.Fatalf("attach from 0: %v", err)
			}
			all = append(all, ev)
		}
		if len(all) < 2 {
			t.Fatalf("log holds %d events, want the delta and the Done", len(all))
		}
		if _, ok := all[0].(types.TextDelta); !ok {
			t.Fatalf("first logged event %T, want TextDelta", all[0])
		}
		dones := 0
		for _, ev := range all[1:] {
			if _, ok := ev.(types.Done); ok {
				dones++
			}
		}
		if dones != 1 {
			t.Fatalf("log holds %d Done events, want 1", dones)
		}
	})

	t.Run("stall-guard-disabled-at-zero", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g := NewStallGuard(0, StallPreempt, cancel)
		base := func(yield func(types.Event, error) bool) {
			yield(types.TextDelta{Delta: "a"}, nil)
		}
		n := 0
		for ev, err := range g.Watch(ctx, base) {
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if _, ok := ev.(types.TextDelta); !ok {
				t.Fatalf("event %T, want TextDelta", ev)
			}
			n++
		}
		if n != 1 {
			t.Fatalf("got %d events, want 1", n)
		}
		if g.Stalled() || g.Preempts() != 0 || g.Detaches() != 0 {
			t.Fatal("disabled guard fired")
		}
	})

	t.Run("stalled-consumer-gets-no-model-retry", func(t *testing.T) {
		rt := &stallTestRT{gate: make(chan struct{}), done: make(chan struct{})}
		conv, _, _ := stallConversation(t, rt)
		tk := ticks()
		fired := make(chan struct{})
		ctx, cancel := context.WithCancel(principalCtx(context.Background()))
		defer cancel()
		g := runStall(NewStallGuard(time.Minute, StallPreempt, func() {
			cancel()
			close(fired)
		}))
		g.ticks = tk

		stalled := make(chan struct{})
		preempted := make(chan struct{})
		seq := g.Watch(ctx, conv.Send(ctx, "sess-1", userMsg("hi")))
		streamDone := make(chan struct{})
		go func() {
			defer close(streamDone)
			first := true
			for _, err := range seq {
				if err != nil {
					t.Errorf("stream: %v", err)
					return
				}
				if first {
					first = false
					close(stalled)
					<-preempted
				}
			}
		}()
		go func() {
			<-stalled
			tk <- time.Time{}
			tk <- time.Time{}
			<-fired
			close(rt.gate)
			close(preempted)
		}()
		<-streamDone

		// The stall preempted the run instead of re-running the turn: the
		// model call happened once per turn and the run holds no lease.
		if calls := rt.calls(); calls != 2 {
			t.Fatalf("run took %d model calls, want 2 (one per turn, no retry)", calls)
		}
		if got := g.Preempts(); got != 1 {
			t.Fatalf("preempts = %d, want 1", got)
		}
	})
}

// countedStallProvider counts how often the model itself is called.
type countedStallProvider struct {
	*fakeProvider
	calls atomic.Int32
}

func (p *countedStallProvider) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	p.calls.Add(1)
	return p.fakeProvider.Generate(ctx, req)
}
