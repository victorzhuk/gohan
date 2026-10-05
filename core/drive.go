package gohan

import (
	"context"
	"iter"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// sinkRelay forwards the component events a governed decorator emits during
// a step to Drive's iterator, and to any sink the caller already attached.
// The attached sink records first, then the event is delivered, so the
// durable log never trails what a consumer has already seen. Delivery
// happens synchronously while the effect runs: a consumer that stops
// taking events stops the relay, which cancels the drive context and so
// interrupts cancellable effect work; no event is delivered afterwards.
type sinkRelay struct {
	next    types.Sink
	deliver func(types.Event) bool
	cancel  context.CancelFunc
	stopped bool
}

func (r *sinkRelay) Emit(ctx context.Context, e types.Event) {
	if r.stopped {
		return
	}
	if r.next != nil {
		r.next.Emit(ctx, e)
	}
	if r.deliver != nil && !r.deliver(e) {
		r.stopped = true
		if r.cancel != nil {
			r.cancel()
		}
	}
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
