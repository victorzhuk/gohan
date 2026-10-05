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
type sinkRelay struct {
	next types.Sink
	seen []types.Event
}

func (r *sinkRelay) Emit(ctx context.Context, e types.Event) {
	r.seen = append(r.seen, e)
	if r.next != nil {
		r.next.Emit(ctx, e)
	}
}

func (r *sinkRelay) take() []types.Event {
	evs := r.seen
	r.seen = nil
	return evs
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
