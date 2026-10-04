package gohan

import (
	"context"
	"errors"
	"sync"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

// Preemptor carries one preemption request for one run. Preempt arms it;
// the run lands the request at a persisted safe point and suspends with
// types.Preempted, from where Continue() resumes it without approval.
type Preemptor struct {
	mu        sync.Mutex
	requested bool
	notify    chan struct{}
}

// NewPreemptor builds an unarmed preemption request.
func NewPreemptor() *Preemptor {
	return &Preemptor{notify: make(chan struct{}, 1)}
}

// Preempt arms the request. Arming twice changes nothing: one request lands
// one suspension.
func (p *Preemptor) Preempt() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.requested {
		return
	}
	p.requested = true
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

func (p *Preemptor) pending() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requested
}

// take consumes the request, so a run resumed through the same runtime
// proceeds instead of suspending again.
func (p *Preemptor) take() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requested = false
}

// Runtime wraps rt so the request never lands mid-call. Before the next
// call starts the wrapper suspends the run at once; while a call is in
// flight it cancels the call's context and waits for the step to settle:
// a shielded side effect finishes and its results append, a partial model
// call aborts before anything appends. Either way the run suspends with
// types.Preempted and the checkpoint the lifecycle persists carries the
// state the step started from.
func (p *Preemptor) Runtime(rt runtime.Runtime) runtime.Runtime {
	return &preemptableRT{rt: rt, p: p}
}

type preemptableRT struct {
	rt runtime.Runtime
	p  *Preemptor
}

func (r *preemptableRT) Name() string                         { return r.rt.Name() }
func (r *preemptableRT) Granularity() runtime.StepGranularity { return r.rt.Granularity() }

func (r *preemptableRT) Start(ctx context.Context, a runtime.AgentRun) (runtime.State, error) {
	return r.rt.Start(ctx, a)
}

type stepOutcome struct {
	st     runtime.State
	evs    []types.Event
	status runtime.Status
	err    error
}

func (r *preemptableRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if r.p.pending() {
		r.p.take()
		return st, nil, runtime.Continue, preemptedSuspend()
	}
	stepCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan stepOutcome, 1)
	go func() {
		s, evs, status, err := r.rt.Step(stepCtx, st)
		done <- stepOutcome{st: s, evs: evs, status: status, err: err}
	}()
	r.p.mu.Lock()
	armed := r.p.requested
	r.p.mu.Unlock()
	var o stepOutcome
	if armed {
		cancel()
		o = <-done
		// The armed request is taken below; drop its notification so a
		// later step does not consume it as a fresh request.
		select {
		case <-r.p.notify:
		default:
		}
	} else {
		select {
		case o = <-done:
			// Drop a notification armed after the step settled; the
			// pending check below still lands it.
			select {
			case <-r.p.notify:
			default:
			}
		case <-r.p.notify:
			cancel()
			o = <-done
		}
	}
	var sig *types.SuspendError
	if o.err != nil {
		sig, _ = errors.AsType[*types.SuspendError](o.err)
	}
	// The step settled on its own terms, or failed for a reason that is not
	// the cancellation the preemption injected: either wins over the request.
	won := o.err == nil && o.status != runtime.Continue ||
		sig != nil ||
		o.err != nil && sig == nil && !errors.Is(o.err, context.Canceled)
	if won || !r.p.pending() {
		return o.st, o.evs, o.status, o.err
	}
	r.p.take()
	return o.st, o.evs, runtime.Continue, preemptedSuspend()
}

func preemptedSuspend() *types.SuspendError {
	return &types.SuspendError{Reason: types.Preempted}
}
