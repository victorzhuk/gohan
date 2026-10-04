package gohan

import (
	"context"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// SuspendTool is the error a tool returns to suspend the run instead of
// finishing: the reason must be resumable (the spec's async-tool source),
// the payload is what the client fetches and what Resume delivers against.
func SuspendTool(reason types.SuspendReason, payload any) *types.SuspendError {
	return &types.SuspendError{Reason: reason, Payload: payload}
}

// ResumeStrategy names how a suspended run resumes. Replay is the strategy
// runtime/spec.md fixes for agent runtimes: DriveResume rebuilds State from
// the checkpoint, appends the resume input and re-executes Step from there,
// with completed calls answered from the journal. It is distinct from
// stores.ResumeReplay, the checkpoint-compatibility plan of the same idea.
type ResumeStrategy int

const Replay ResumeStrategy = iota

// Waker schedules the wake for a Scheduled suspension
// (suspension/spec.md). std/permission declares a Waker of its own for
// approval expiry with a different signature; the collision is resolved per
// package, and the delivery path consumes this spec shape.
type Waker interface {
	Schedule(ctx context.Context, t types.ResumeToken, at time.Time) error
}

// WithLifecycleWaker binds the scheduler that arms a Scheduled wake exactly
// once per suspension, after the run is marked suspended.
func WithLifecycleWaker(w Waker) LifecycleOption {
	return func(lc *Lifecycle) { lc.waker = w }
}
