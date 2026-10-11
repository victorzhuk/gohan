package gohan

import (
	"context"
	"iter"
	"sync"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// emitter is the single seam every event to the drive consumer passes
// through. An iterator's yield must never run concurrently, and the drive
// loop yields step events while a governed effect may deliver through the
// relay at the same time, so both sides take this lock.
type emitter struct {
	mu    sync.Mutex
	yield func(types.Event, error) bool
}

// send delivers one event to the consumer, serialized against every other
// delivery.
func (e *emitter) send(ev types.Event, err error) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.yield(ev, err)
}

// sinkRelay forwards the component events a governed decorator emits during
// a step to Drive's iterator, and to any sink the caller already attached.
// The attached sink records first, then the event is delivered, so the
// durable log never trails what a consumer has already seen. Delivery
// happens synchronously while the effect runs: a consumer that stops
// taking events stops the relay, which cancels the drive context and so
// interrupts cancellable effect work; no event is delivered afterwards.
type sinkRelay struct {
	em     *emitter
	next   types.Sink
	cancel context.CancelFunc
	// stopped is guarded by em.mu: only the delivery below and the drive
	// loop's own poll read it.
	stopped bool
}

func (r *sinkRelay) Emit(ctx context.Context, e types.Event) {
	// The lock spans the attached sink and the delivery, so two effect
	// goroutines emitting at once and the drive loop yielding all observe
	// one order.
	r.em.mu.Lock()
	defer r.em.mu.Unlock()
	if r.stopped {
		return
	}
	if r.next != nil {
		r.next.Emit(ctx, e)
	}
	if !r.em.yield(e, nil) {
		r.stopped = true
		if r.cancel != nil {
			r.cancel()
		}
	}
}

// hasStopped reports whether the consumer refused further events.
func (r *sinkRelay) hasStopped() bool {
	r.em.mu.Lock()
	defer r.em.mu.Unlock()
	return r.stopped
}

// Drive runs the effect loop over rt for r: it starts the stepper, installs
// the run-scoped sink, forwards every event in arrival order and emits Done
// after the last step reports completion. Component events (TextDelta and
// friends) arrive through the sink, never from the runtime itself.
func Drive(ctx context.Context, rt runtime.Runtime, r runtime.AgentRun) iter.Seq2[types.Event, error] {
	return func(yield func(types.Event, error) bool) {
		st, err := rt.Start(ctx, r)
		if err != nil {
			yield(nil, err)
			return
		}
		NewLifecycle().drive(ctx, rt, r, st, yield)
	}
}

// DriveResume re-drives a run from the state a checkpoint carries. The
// resume input is already appended to the history by the caller. Start runs
// once to install the per-run wiring; the saved state replaces the initial
// position it returns, so initial effects never replay.
func DriveResume(ctx context.Context, rt runtime.Runtime, r runtime.AgentRun, st runtime.State, _ stores.ResumeInput) iter.Seq2[types.Event, error] {
	return func(yield func(types.Event, error) bool) {
		if _, err := rt.Start(ctx, r); err != nil {
			yield(nil, err)
			return
		}
		NewLifecycle().drive(ctx, rt, r, st, yield)
	}
}
