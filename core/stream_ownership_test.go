package gohan

import (
	"context"
	"iter"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

// emitRT streams deltas through the run sink while its step is still
// running, then returns the events a step carries. The step blocks until
// the test releases it, so a delivered delta proves delivery while the
// effect runs.
type emitRT struct {
	mu sync.Mutex

	emitted  int
	returned int

	// cancelledObserved records that the step saw its context cancelled.
	cancelledObserved bool

	// hold blocks the step after its emissions until closed; nil runs
	// straight through. When observeCancel is set the step waits for its
	// context instead.
	hold          chan struct{}
	observeCancel bool

	err error
}

func (r *emitRT) Name() string                         { return "emit.test" }
func (r *emitRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *emitRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *emitRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	sink, _ := types.SinkFrom(ctx)
	sink.Emit(ctx, types.TextDelta{Turn: 1, Delta: "a"})
	sink.Emit(ctx, types.TextDelta{Turn: 1, Delta: "b"})
	r.mu.Lock()
	r.emitted += 2
	r.mu.Unlock()
	if r.observeCancel {
		<-ctx.Done()
		r.mu.Lock()
		r.cancelledObserved = ctx.Err() != nil
		r.mu.Unlock()
		r.returned++
		return st, nil, runtime.Continue, ctx.Err()
	}
	if r.hold != nil {
		<-r.hold
	}
	r.mu.Lock()
	r.returned++
	r.mu.Unlock()
	if r.err != nil {
		return st, nil, runtime.Continue, r.err
	}
	next := st
	next.Turn = st.Turn + 1
	return next, []types.Event{types.ToolFinished{Turn: 1}}, runtime.DoneStatus, nil
}

func TestDriveDeliversEventsWhileEffectRuns(t *testing.T) {
	hold := make(chan struct{})
	rt := &emitRT{hold: hold}
	next, stop := iter.Pull2(Drive(context.Background(), rt, runtime.AgentRun{}))
	defer stop()

	for i, want := range []string{"a", "b"} {
		ev, _, ok := next()
		if !ok {
			t.Fatalf("stream ended before %q", want)
		}
		delta, isDelta := ev.(types.TextDelta)
		if !isDelta || delta.Delta != want {
			t.Fatalf("event %d: got %#v, want TextDelta %q", i, ev, want)
		}
	}
	rt.mu.Lock()
	returned := rt.returned
	rt.mu.Unlock()
	if returned != 0 {
		t.Fatalf("step returned before its emissions were delivered")
	}

	// Delivered while the effect is still blocked: release it and expect
	// the step-returned event only after the component emissions.
	close(hold)
	ev, _, ok := next()
	if !ok {
		t.Fatal("stream ended before the step events")
	}
	if _, isTool := ev.(types.ToolFinished); !isTool {
		t.Fatalf("got %#v, want ToolFinished", ev)
	}
	ev, _, ok = next()
	if !ok {
		t.Fatal("stream ended before Done")
	}
	done, isDone := ev.(types.Done)
	if !isDone || done.Reason != types.StopCompleted {
		t.Fatalf("got %#v, want Done{StopCompleted}", ev)
	}
	if _, _, ok := next(); ok {
		t.Fatal("stream continued past Done")
	}
}

func TestDriveStopsEffectWorkWhenConsumerStops(t *testing.T) {
	rt := &emitRT{observeCancel: true}
	consumed := 0
	for range Drive(context.Background(), rt, runtime.AgentRun{}) {
		consumed++
		if consumed == 1 {
			break
		}
	}
	if consumed != 1 {
		t.Fatalf("consumed %d events, want 1", consumed)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if !rt.cancelledObserved {
		t.Fatal("step never observed the cancellation")
	}
}

func TestDriveFailingRunHasSingleTerminalIndication(t *testing.T) {
	rt := &emitRT{err: context.DeadlineExceeded}
	var deltas int
	var terminals int
	var err error
	for ev, e := range Drive(context.Background(), rt, runtime.AgentRun{}) {
		if e != nil {
			err = e
			terminals++
			continue
		}
		if _, isDone := ev.(types.Done); isDone {
			t.Fatalf("got Done %#v alongside the failure", ev)
		}
		if _, isDelta := ev.(types.TextDelta); isDelta {
			deltas++
		}
	}
	if deltas != 2 {
		t.Fatalf("got %d deltas before the failure, want 2", deltas)
	}
	if err == nil || terminals != 1 {
		t.Fatalf("got %d terminal tuples (last %v), want exactly one", terminals, err)
	}
}
