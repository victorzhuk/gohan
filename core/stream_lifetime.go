package gohan

import (
	"context"
	"errors"
	"iter"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// errDetachedNoLog is the constructor error for a Detached flow without an
// event log: a client could never reattach, so the run would be unreadable.
var errDetachedNoLog = errors.New("gohan: detached flow needs an event log")

// Detached switches the conversation's runs to detached lifetime: Send
// returns once the run is started, the run executes under a harness-owned
// context bounded by the flow's MaxWallClock, and clients consume events
// through Attach. A detached conversation needs an event log.
func Detached() ConversationOption {
	return func(c *conversation) { c.detached = true }
}

// detachedContext derives the context a detached run executes under. It
// keeps values from the caller's context but not its cancellation: the run
// outlives the client that started it. The wall-clock bound comes from the
// flow's limits; zero leaves the run to the runs store's lease expiry.
func detachedContext(ctx context.Context, wall time.Duration) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(ctx)
	if wall <= 0 {
		return context.WithCancel(base)
	}
	return context.WithTimeout(base, wall)
}

// run drives one run on the harness-owned run worker and returns an
// iterator over the events the worker produced. The caller's iterator only
// reads what the worker already recorded, so a consumer that stops taking
// events never stops the run: the consumer stall guard preempts it instead.
func (c *conversation) run(ctx context.Context, lease stores.Lease, sessionID string, input []Message) iter.Seq2[Event, error] {
	pre := NewPreemptor()
	limit := c.stall
	if c.detached {
		// A detached run has no attached consumer to measure.
		limit = 0
	}
	runCtx := ctx
	cancelRun := context.CancelFunc(func() {})
	if c.stallAction == StallDetach && limit > 0 {
		// The run continues past the stall, so it never lives under the
		// consumer's cancellation: the wall-clock bound replaces it.
		runCtx, cancelRun = detachedContext(ctx, c.wall)
	}
	guard := NewStallGuard(limit, c.stallAction, pre.Preempt)
	q := newRunQueue()
	go func() {
		defer q.close()
		defer cancelRun()
		defer c.markRunEnded(lease.RunID)
		c.driveRun(runCtx, lease, sessionID, input, pre, q.push)
	}()
	return guard.Watch(ctx, q.seq())
}

// driveRun drives one run under the lifecycle ordering and emits every
// event through emit after recording it in the run's log.
func (c *conversation) driveRun(ctx context.Context, lease stores.Lease, sessionID string, input []Message, pre *Preemptor, emit func(Event, error) bool) {
	opts := []LifecycleOption{
		WithLifecycleRuns(c.runs, lease),
		WithLifecycleSession(sessionID),
		WithLifecycleFlow(c.spec),
	}
	if c.policySrc != nil {
		opts = append(opts, WithLifecycleApprovalPolicy(c.policySrc))
	}
	if c.toolSpecs != nil {
		opts = append(opts, WithLifecycleToolSpecs(c.toolSpecs))
	}
	if c.log != nil {
		// The steers a drained mailbox carries are appended to history
		// before SteerApplied acknowledges them; without the appender the
		// signal path fails the step.
		opts = append(opts, WithLifecycleAppender(AppendFunc(func(ctx context.Context, expected int64, msgs ...types.Message) (int64, error) {
			return c.log.Append(ctx, sessionID, expected, msgs...)
		})))
	}
	if p, ok := PrincipalFrom(ctx); ok {
		opts = append(opts, WithLifecycleOriginator(p))
	}
	lc := NewLifecycle(opts...)
	rt := c.rt
	ag := runtime.AgentRun{Input: input}
	runCtx := ctx
	if c.newRun != nil {
		rt = runtime.NewNative()
		rctx, run, err := c.newRun(ctx, sessionID, lease, input)
		if err != nil {
			// The run is already acquired: close it as Failed so the
			// lease never dangles behind the error tuple.
			if ferr := lc.finishRun(ctx, runtime.State{}, stores.Failed, nil); ferr != nil {
				emit(nil, ferr)
				return
			}
			emit(nil, err)
			return
		}
		runCtx, ag = rctx, run
		if ag.History.Version > 0 {
			opts = append(opts, WithLifecycleHistoryVersion(ag.History.Version))
			lc = NewLifecycle(opts...)
		}
	}
	if c.cps != nil {
		// Suspension persists through the conversation's checkpoints
		// store; without one the runtime cannot suspend.
		ag.Save = c.cps.Put
	}
	rt = pre.Runtime(rt)
	for ev, err := range DriveLifecycle(runCtx, lc, rt, ag) {
		if err != nil {
			var gb *types.GuardBlockedError
			if errors.As(err, &gb) {
				gbEv := types.GuardBlocked{Stage: gb.Stage, Reason: gb.Reason}
				if !c.relay(ctx, lease.RunID, gbEv, emit) {
					return
				}
				if !emit(gbEv, nil) {
					return
				}
				doneEv := types.Done{Reason: types.StopGuardBlocked}
				if !c.relay(ctx, lease.RunID, doneEv, emit) {
					return
				}
				emit(doneEv, nil)
				return
			}
			emit(nil, err)
			return
		}
		if !c.relay(ctx, lease.RunID, ev, emit) {
			return
		}
		if !emit(ev, nil) {
			return
		}
	}
}

// runTuple is one event or error the run worker produced.
type runTuple struct {
	ev  Event
	err error
}

// runQueue buffers what the run worker produced for the consumer's
// iterator. It is unbounded: the worker never blocks on a slow consumer,
// and the consumer stall guard bounds what an unbounded buffer can cost.
type runQueue struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []runTuple
	done bool
}

func newRunQueue() *runQueue {
	q := &runQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *runQueue) push(ev Event, err error) bool {
	q.mu.Lock()
	q.buf = append(q.buf, runTuple{ev, err})
	q.mu.Unlock()
	q.cond.Signal()
	return true
}

func (q *runQueue) close() {
	q.mu.Lock()
	q.done = true
	q.mu.Unlock()
	q.cond.Broadcast()
}

// seq returns the consumer-facing iterator over the recorded events.
func (q *runQueue) seq() iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		for {
			q.mu.Lock()
			for len(q.buf) == 0 && !q.done {
				q.cond.Wait()
			}
			if len(q.buf) == 0 {
				q.mu.Unlock()
				return
			}
			t := q.buf[0]
			q.buf = q.buf[1:]
			q.mu.Unlock()
			if !yield(t.ev, t.err) {
				return
			}
		}
	}
}
