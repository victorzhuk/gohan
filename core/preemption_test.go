package gohan

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// appendSpy records every append the fake runtime makes, so a test can
// assert that a dropped call left nothing behind.
type appendSpy struct {
	mu      sync.Mutex
	appends [][]types.Message
}

func (s *appendSpy) Append(_ context.Context, expected int64, msgs ...types.Message) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appends = append(s.appends, msgs)
	return expected + int64(len(msgs)), nil
}

func (s *appendSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.appends)
}

// preemptRT scripts the steps the preemption properties need: a plain turn,
// a turn whose SideEffect call runs under the cancel shield, and a turn
// whose model call blocks mid-stream until the test releases it. started is
// buffered so a second pass never blocks on a signal the test already read.
type preemptRT struct {
	mode    int // 0 plain, 1 tool, 2 stream
	log     *appendSpy
	started chan struct{}
	release chan struct{}

	mu         sync.Mutex
	toolRan    int
	modelCalls int
	stepCtx    context.Context
	dropped    bool
}

// cancelled reports the running context of the streaming step, so the test
// waits for the driver's cancellation before releasing the model call.
func (r *preemptRT) cancelled() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stepCtx == nil {
		return nil
	}
	return r.stepCtx.Done()
}

func (r *preemptRT) Name() string                         { return "preempt.test" }
func (r *preemptRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *preemptRT) Start(_ context.Context, _ runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *preemptRT) counts() (toolRan, modelCalls int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.toolRan, r.modelCalls
}

func (r *preemptRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	resuming := r.mode == 2 && r.dropped
	dropTurn := st.Turn
	r.mu.Unlock()
	if resuming && dropTurn >= 1 {
		return st, nil, runtime.DoneStatus, nil
	}
	r.mu.Lock()
	r.modelCalls++
	r.mu.Unlock()
	turn := st.Turn + 1
	sink, _ := types.SinkFrom(ctx)
	if r.mode == 2 && !r.dropped {
		r.mu.Lock()
		r.dropped = true
		r.mu.Unlock()
		r.mu.Lock()
		r.stepCtx = ctx
		r.mu.Unlock()
		sink.Emit(ctx, types.TextDelta{Turn: turn, Delta: "partial"})
		r.started <- struct{}{}
		<-r.release
		if err := ctx.Err(); err != nil {
			return st, nil, runtime.Continue, err
		}
	}
	if r.mode == 1 {
		r.mu.Lock()
		r.toolRan++
		r.mu.Unlock()
		call := types.ToolUse{ID: "call-1", Name: "book", Args: jsontext.Value(`{}`)}
		asst := types.Message{
			ID: assistantID(turn), Role: types.RoleAssistant,
			Blocks: []types.Block{call},
		}
		v, err := r.log.Append(ctx, st.HistoryVersion, asst)
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		r.started <- struct{}{}
		<-r.release
		// The cancel shield hands the effect a context the driver's
		// cancellation never reaches; the effect always completes.
		res := types.ToolResult{ID: "call-1", Outcome: types.Succeeded}
		v, err = r.log.Append(ctx, v, types.Message{
			ID: resultID(turn), Role: types.RoleAssistant,
			Blocks: []types.Block{res},
		})
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		fin := types.ToolFinished{Turn: turn, Result: res}
		sink.Emit(ctx, fin)
		return runtime.State{Turn: turn, HistoryVersion: v}, []types.Event{fin}, runtime.Continue, nil
	}
	asst := types.Message{
		ID: assistantID(turn), Role: types.RoleAssistant,
		Blocks: []types.Block{types.Text{Text: "done"}},
	}
	evs := []types.Event{types.AssistantMessage{Turn: turn, Message: asst}}
	return runtime.State{Turn: turn, HistoryVersion: st.HistoryVersion + 1}, evs, runtime.Continue, nil
}

func streamCollect(seq iter.Seq2[types.Event, error]) <-chan streamResult {
	ch := make(chan streamResult, 1)
	go func() {
		ch <- collectStream(seq)
	}()
	return ch
}

func lastOf(evs []types.Event) types.Event {
	if len(evs) == 0 {
		return nil
	}
	return evs[len(evs)-1]
}

func TestSafePointPreemption(t *testing.T) {
	ctx := principalCtx(context.Background())

	t.Run("runtime.shutdown-preempts-at-safe-point", func(t *testing.T) {
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now))
		cps := stores.NewMemoryCheckpoints()
		rt := &preemptRT{log: &appendSpy{}}
		pre := NewPreemptor()
		lease, err := runs.Start(ctx, stores.Run{SessionID: "s1", RunID: "r1", Flow: "chat"}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		lc := NewLifecycle(WithLifecycleRuns(runs, lease), WithLifecycleSession("s1"))
		var saved stores.Checkpoint
		ag := runtime.AgentRun{Save: func(_ context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
			saved = cp
			return cps.Put(ctx, cp)
		}}
		next, stop := iter.Pull2(DriveLifecycle(ctx, lc, pre.Runtime(rt), ag))
		defer stop()
		first, _, ok := next()
		if !ok {
			t.Fatal("stream ended before the first turn")
		}
		if _, isAsst := first.(types.AssistantMessage); !isAsst {
			t.Fatalf("first event %T, want AssistantMessage", first)
		}
		pre.Preempt()
		second, err, ok := next()
		if !ok || err != nil {
			t.Fatalf("second pull: ok=%v err=%v", ok, err)
		}
		sus, isSus := second.(types.Suspended)
		if !isSus {
			t.Fatalf("last event %T, want Suspended", second)
		}
		if sus.Reason != types.Preempted || sus.Token == "" {
			t.Fatalf("suspended with reason %q token %q", sus.Reason, sus.Token)
		}
		if saved.Reason != types.Preempted {
			t.Fatalf("checkpoint reason %q, want preempted", saved.Reason)
		}
		var raw struct {
			State runtime.State `json:"state"`
		}
		if jerr := json.Unmarshal(saved.Data, &raw); jerr != nil {
			t.Fatalf("checkpoint state: %v", jerr)
		}
		if raw.State.Turn != 1 {
			t.Fatalf("checkpoint turn %d, want 1", raw.State.Turn)
		}
		if runs.SessionLeaseActive(ctx, "s1") {
			t.Fatal("lease still held after preemption")
		}
	})

	t.Run("runtime.shutdown-side-effect-completes", func(t *testing.T) {
		rt := &preemptRT{mode: 1, log: &appendSpy{}, started: make(chan struct{}, 1), release: make(chan struct{})}
		pre := NewPreemptor()
		res := streamCollect(DriveLifecycle(ctx, nil, pre.Runtime(rt), runtime.AgentRun{Save: stores.NewMemoryCheckpoints().Put}))
		<-rt.started
		pre.Preempt()
		close(rt.release)
		out := <-res
		if out.err != nil {
			t.Fatalf("drive: %v", out.err)
		}
		sus, isSus := lastOf(out.evs).(types.Suspended)
		if !isSus || sus.Reason != types.Preempted {
			t.Fatalf("last event %v, want Suspended{preempted}", lastOf(out.evs))
		}
		var sawToolFinished bool
		for _, ev := range out.evs {
			if _, ok := ev.(types.ToolFinished); ok {
				sawToolFinished = true
			}
		}
		if !sawToolFinished {
			t.Fatal("tool result never surfaced")
		}
		toolRan, _ := rt.counts()
		if toolRan != 1 {
			t.Fatalf("side effect ran %d times, want 1", toolRan)
		}
		if got := rt.log.count(); got != 2 {
			t.Fatalf("log appends %d, want assistant and results", got)
		}
	})

	t.Run("runtime.shutdown-partial-model-call-dropped", func(t *testing.T) {
		dropped, resumed := preemptPartialDrop(t, ctx)
		sus, isSus := lastOf(dropped.evs).(types.Suspended)
		if !isSus || sus.Reason != types.Preempted {
			t.Fatalf("last event %v, want Suspended{preempted}", lastOf(dropped.evs))
		}
		if resumed.err != nil {
			t.Fatalf("resume drive: %v", resumed.err)
		}
		done, isDone := lastOf(resumed.evs).(types.Done)
		if !isDone || done.Reason != types.StopCompleted {
			t.Fatalf("last event %v, want Done{completed}", lastOf(resumed.evs))
		}
		var reissued bool
		for _, ev := range resumed.evs {
			if asst, ok := ev.(types.AssistantMessage); ok && asst.Turn == 1 {
				reissued = true
			}
		}
		if !reissued {
			t.Fatal("resume did not re-issue the model call for the dropped turn")
		}
	})

	t.Run("preempted run leaves no partial message in the log", func(t *testing.T) {
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now))
		rt := &preemptRT{mode: 2, log: &appendSpy{}, started: make(chan struct{}, 1), release: make(chan struct{})}
		pre := NewPreemptor()
		lease, err := runs.Start(ctx, stores.Run{SessionID: "s3", RunID: "r4", Flow: "chat"}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		lc := NewLifecycle(WithLifecycleRuns(runs, lease), WithLifecycleSession("s3"))
		res := streamCollect(DriveLifecycle(ctx, lc, pre.Runtime(rt), runtime.AgentRun{Save: stores.NewMemoryCheckpoints().Put}))
		<-rt.started
		pre.Preempt()
		<-rt.cancelled()
		close(rt.release)
		out := <-res
		if out.err != nil {
			t.Fatalf("drive: %v", out.err)
		}
		if got := rt.log.count(); got != 0 {
			t.Fatalf("log holds %d appends after the drop, want none", got)
		}
		var sawDelta bool
		for _, ev := range out.evs {
			if _, ok := ev.(types.TextDelta); ok {
				sawDelta = true
			}
			if _, ok := ev.(types.AssistantMessage); ok {
				t.Fatal("a partial assistant message reached the stream")
			}
		}
		if !sawDelta {
			t.Fatal("the dropped call never streamed")
		}
	})
}

// preemptPartialDrop drops a streaming model call mid-flight and re-drives
// the checkpointed state, returning both stream results.
func preemptPartialDrop(t *testing.T, ctx context.Context) (streamResult, streamResult) {
	t.Helper()
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now))
	cps := stores.NewMemoryCheckpoints()
	rt := &preemptRT{mode: 2, log: &appendSpy{}, started: make(chan struct{}, 1), release: make(chan struct{})}
	pre := NewPreemptor()
	lease, err := runs.Start(ctx, stores.Run{SessionID: "s2", RunID: "r2", Flow: "chat"}, stores.LeaseTTL)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	lc := NewLifecycle(WithLifecycleRuns(runs, lease), WithLifecycleSession("s2"))
	var saved stores.Checkpoint
	ag := runtime.AgentRun{Save: func(_ context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
		saved = cp
		return cps.Put(ctx, cp)
	}}
	droppedCh := streamCollect(func(yield func(types.Event, error) bool) {
		for ev, err := range DriveLifecycle(ctx, lc, pre.Runtime(rt), ag) {
			if !yield(ev, err) {
				return
			}
		}
	})
	<-rt.started
	pre.Preempt()
	<-rt.cancelled()
	close(rt.release)
	dropped := <-droppedCh
	if dropped.err != nil {
		t.Fatalf("drop drive: %v", dropped.err)
	}
	if got := rt.log.count(); got != 0 {
		t.Fatalf("log appends %d after the drop, want none", got)
	}
	var st runtime.State
	if jerr := json.Unmarshal(saved.Data, &st); jerr != nil {
		t.Fatalf("checkpoint state: %v", jerr)
	}
	if st.Turn != 0 {
		t.Fatalf("checkpoint turn %d, want 0: the same turn re-issues", st.Turn)
	}
	lease2, err := runs.Start(ctx, stores.Run{SessionID: "s2", RunID: "r3", Flow: "chat"}, stores.LeaseTTL)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	lc2 := NewLifecycle(WithLifecycleRuns(runs, lease2), WithLifecycleSession("s2"))
	resumed := <-streamCollect(DriveLifecycle(ctx, lc2, pre.Runtime(rt), runtime.AgentRun{}))
	_, modelCalls := rt.counts()
	if modelCalls != 2 {
		t.Fatalf("model calls %d, want one per turn", modelCalls)
	}
	return dropped, resumed
}
