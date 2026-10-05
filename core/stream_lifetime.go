package gohan

import (
	"context"
	"errors"
	"fmt"
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

// lifecycleOptions lists the options every run lifecycle carries: the runs
// store and lease, the session, the flow, the approval policy, the tool
// specs, the session appender and the originator when one is known.
func (c *conversation) lifecycleOptions(sessionID string, lease stores.Lease, originator types.Principal, hasOriginator bool) []LifecycleOption {
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
	if hasOriginator {
		opts = append(opts, WithLifecycleOriginator(originator))
	}
	return opts
}

// failRun records one terminal error payload for a failed run and emits the
// one live (nil, err) tuple the consumer sees. The terminal payload is the
// only durable trace: a Done never follows a failure.
func (c *conversation) failRun(ctx context.Context, lease stores.Lease, recorded int64, emit func(Event, error) bool, err error) {
	if c.events != nil {
		te := TerminalFailure(err, lease.RunID, recorded+2)
		if aerr := c.events.Append(ctx, lease.RunID, stores.Event{Payload: te, Meta: types.EventMeta{RunID: lease.RunID}}); aerr != nil {
			err = fmt.Errorf("record terminal error: %w", aerr)
		}
	}
	emit(nil, err)
}

// run drives one run on the harness-owned run worker and returns an
// iterator over the events the worker produced. The caller's iterator only
// reads what the worker already recorded, so a consumer that stops taking
// events never stops the run: the consumer stall guard preempts it instead.
func (c *conversation) run(ctx context.Context, lease stores.Lease, sessionID string, input []Message) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		pre := NewPreemptor()
		rt := c.rt
		ag := runtime.AgentRun{Input: input}
		runCtx := ctx
		originator, hasOriginator := PrincipalFrom(ctx)
		lc := NewLifecycle(c.lifecycleOptions(sessionID, lease, originator, hasOriginator)...)
		if c.newRun != nil {
			rt = runtime.NewNative()
			rctx, run, err := c.newRun(ctx, sessionID, lease, input)
			if err != nil {
				// The run is already acquired: close it as Failed so the
				// lease never dangles behind the error tuple.
				if ferr := lc.finishRun(ctx, runtime.State{}, stores.Failed, nil); ferr != nil {
					yield(nil, ferr)
					return
				}
				c.failRun(ctx, lease, 0, yield, err)
				return
			}
			runCtx, ag = rctx, run
			if ag.History.Version > 0 {
				opts := append(c.lifecycleOptions(sessionID, lease, originator, hasOriginator),
					WithLifecycleHistoryVersion(ag.History.Version))
				lc = NewLifecycle(opts...)
			}
		}
		if c.cps != nil {
			// Suspension persists through the conversation's checkpoints
			// store; without one the runtime cannot suspend.
			ag.Save = c.cps.Put
		}
		for ev, err := range c.runPrepared(runCtx, lease, lc, rt, ag, pre) {
			if !yield(ev, err) {
				return
			}
		}
	}
}

// runPrepared drives one prepared run on the harness-owned worker and
// returns the bounded iterator over its events. The lifecycle carries the
// run's restored state and ledger; the method owns worker completion,
// recording, delivery and stall monitoring. Recovery stays headless.
func (c *conversation) runPrepared(ctx context.Context, lease stores.Lease, lc *Lifecycle, rt runtime.Runtime, ag runtime.AgentRun, pre *Preemptor) iter.Seq2[Event, error] {
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
	rt = pre.Runtime(rt)
	q := newStreamHandoff()
	onBreak := func() {
		if c.stallAction == StallPreempt {
			pre.Preempt()
		}
	}
	go func() {
		defer q.close()
		defer cancelRun()
		defer c.markRunEnded(lease.RunID)
		c.drivePrepared(runCtx, lease, lc, rt, ag, q.push)
	}()
	return guard.Watch(ctx, q.seq(onBreak))
}

// drivePrepared drives one prepared run under the lifecycle ordering and
// emits every event through emit after recording it in the run's log.
func (c *conversation) drivePrepared(ctx context.Context, lease stores.Lease, lc *Lifecycle, rt runtime.Runtime, ag runtime.AgentRun, emit func(Event, error) bool) {
	// recorded counts the events relayed so far, so the terminal error can
	// name the sequence a reattaching client resumes from.
	var recorded int64
	fail := func(err error) {
		c.failRun(ctx, lease, recorded, emit, err)
	}
	for ev, err := range DriveLifecycle(ctx, lc, rt, ag) {
		if err != nil {
			var gb *types.GuardBlockedError
			if errors.As(err, &gb) {
				gbEv := types.GuardBlocked{Stage: gb.Stage, Reason: gb.Reason}
				if !c.relay(ctx, lease.RunID, gbEv, emit) {
					return
				}
				recorded++
				if !emit(gbEv, nil) {
					return
				}
				doneEv := types.Done{Reason: types.StopGuardBlocked}
				if !c.relay(ctx, lease.RunID, doneEv, emit) {
					return
				}
				recorded++
				emit(doneEv, nil)
				return
			}
			fail(err)
			return
		}
		if !c.relay(ctx, lease.RunID, ev, emit) {
			return
		}
		recorded++
		if !emit(ev, nil) {
			return
		}
	}
}

// runTuple is one event or error the run worker produced. A tuple with a
// non-nil err is the run's terminal: it is never followed by another tuple.
type runTuple struct {
	ev  Event
	err error
}

// streamHandoff is the single bounded seam between the run worker and the
// consumer's iterator. Ordinary tuples occupy streamBuffer slots; the
// terminal tuple owns one separate slot, so it is never stuck behind a full
// ordinary buffer. Ordinary production blocks while the buffer is full and
// wakes on delivery, on consumer abandonment or on the worker's shutdown,
// so nothing is dropped, overwritten or reordered, and a delivered tuple's
// references are cleared from the buffer.
type streamHandoff struct {
	mu    sync.Mutex
	space *sync.Cond
	taken *sync.Cond

	buf  [DefaultStreamBuffer]runTuple
	n    int
	head int

	term   runTuple
	hasTer bool

	closed bool
	broken bool
}

func newStreamHandoff() *streamHandoff {
	q := &streamHandoff{}
	q.space = sync.NewCond(&q.mu)
	q.taken = sync.NewCond(&q.mu)
	return q
}

// push offers one tuple to the consumer. It blocks while the ordinary
// buffer is full and reports false only when the worker must stop: the
// consumer abandoned delivery or the handoff shut down. A terminal tuple
// always lands, past abandonment as a drop.
func (q *streamHandoff) push(ev Event, err error) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err != nil {
		for q.hasTer && !q.broken && !q.closed {
			q.space.Wait()
		}
		if q.broken || q.closed {
			return !q.closed
		}
		q.term = runTuple{ev, err}
		q.hasTer = true
		q.taken.Signal()
		return true
	}
	for q.n == DefaultStreamBuffer && !q.broken && !q.closed {
		q.space.Wait()
	}
	if q.broken || q.closed {
		return !q.closed
	}
	q.buf[(q.head+q.n)%DefaultStreamBuffer] = runTuple{ev, err}
	q.n++
	q.taken.Signal()
	return true
}

// close marks the worker done and wakes every wait.
func (q *streamHandoff) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.space.Broadcast()
	q.taken.Broadcast()
}

// abandon marks the consumer gone: production stops retaining tuples and
// every blocked wait wakes. The worker keeps recording through emit, which
// drops past abandonment.
func (q *streamHandoff) abandon() {
	q.mu.Lock()
	q.broken = true
	q.mu.Unlock()
	q.space.Broadcast()
	q.taken.Broadcast()
}

// seq returns the consumer-facing iterator over the recorded events. On
// early break it abandons the handoff and applies onBreak, so a full or
// empty producer wait wakes and the run's stall action lands.
func (q *streamHandoff) seq(onBreak func()) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		defer func() {
			q.abandon()
			if onBreak != nil {
				onBreak()
			}
		}()
		for {
			q.mu.Lock()
			for q.n == 0 && !q.hasTer && !q.closed && !q.broken {
				q.taken.Wait()
			}
			if q.broken {
				q.mu.Unlock()
				return
			}
			if q.n == 0 && !q.hasTer {
				q.mu.Unlock()
				return
			}
			var t runTuple
			if q.n > 0 {
				t = q.buf[q.head]
				q.buf[q.head] = runTuple{}
				q.head = (q.head + 1) % DefaultStreamBuffer
				q.n--
				q.space.Signal()
			} else {
				t = q.term
				q.term = runTuple{}
				q.hasTer = false
			}
			q.mu.Unlock()
			if !yield(t.ev, t.err) {
				return
			}
		}
	}
}
