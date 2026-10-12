package gohan

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
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

// detachedDriveCtx copies the caller's principal, run identity and
// idempotency key onto a fresh base context. The detached run re-issues its
// credential instead of reusing the caller's token — one that may expire
// before the run ends — and carries none when the build wires no source. The
// caller's approval never crosses a detach boundary.
func (c *conversation) detachedDriveCtx(ctx context.Context) context.Context {
	det := context.Background()
	if p, ok := PrincipalFrom(ctx); ok {
		det = WithPrincipal(det, p)
		if c.creds != nil {
			cred, err := c.creds.Credentials(det, p)
			if err != nil {
				slog.WarnContext(ctx, "detached credential re-issue failed; run continues credential-free", "err", err)
			} else {
				det = WithCredential(det, cred)
			}
		}
	}
	if info, ok := RunInfoFrom(ctx); ok {
		det = types.WithRunInfo(det, info)
	}
	if key, ok := IdempotencyKey(ctx); ok {
		det = WithIdempotencyKey(det, key)
	}
	return det
}

// lifecycleOptions lists the options every run lifecycle carries: the runs
// store and lease, the session, the flow, the approval policy, the tool
// specs, the session appender, the originator when one is known, and the
// verifier and journal when the stack wired them.
func (c *conversation) lifecycleOptions(sessionID string, lease stores.Lease, originator types.Principal, hasOriginator bool) []LifecycleOption {
	opts := []LifecycleOption{
		WithLifecycleRuns(c.runs, lease),
		WithLifecycleSession(sessionID),
		WithLifecycleFlow(c.spec),
		WithLifecycleResidency(c.tenant.Residency),
	}
	if c.telemetry != nil {
		opts = append(opts, WithLifecycleTelemetry(c.telemetry))
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
		if hw, ok := c.log.(SessionControlWriter); ok {
			opts = append(opts, WithLifecycleHandoffSink(func(ctx context.Context, hc types.HandoffContext) error {
				return c.requestHandoff(ctx, sessionID, hc, hw, originator, hasOriginator)
			}))
		}
	}
	if c.consolidator != nil {
		opts = append(opts, WithLifecycleConsolidation(c.consolidator, c.consolidationAudit))
	}
	if idx, ok := c.log.(stores.SessionIndex); ok {
		opts = append(opts, WithLifecycleSessions(idx))
	}
	if hasOriginator {
		opts = append(opts, WithLifecycleOriginator(originator))
	}
	if h := c.stack.sandboxHandles; h != nil {
		opts = append(opts, WithLifecycleSandboxHandles(h))
	}
	return c.verifyOptions(opts)
}

// requestHandoff performs the session-side transition of a model handoff:
// control moves to ControlHandoffRequested, the HandoffContext persists on
// the session index and the transition lands in the audit trail.
func (c *conversation) requestHandoff(ctx context.Context, sessionID string, hc types.HandoffContext, hw SessionControlWriter, originator types.Principal, hasOriginator bool) error {
	if err := hw.SetControl(ctx, sessionID, stores.ControlHandoffRequested); err != nil {
		return fmt.Errorf("set control: %w", err)
	}
	if idx, ok := c.log.(stores.SessionIndex); ok {
		if err := idx.UpdateSession(ctx, sessionID, stores.SessionPatch{Handoff: &hc}); err != nil {
			return fmt.Errorf("persist handoff: %w", err)
		}
	}
	if !hasOriginator {
		originator = types.Principal{}
	}
	return c.auditControl(ctx, sessionID, "", originator, auditHandoffRequest, decisionControlHandoffRequested)
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
func (c *conversation) run(ctx context.Context, lease stores.Lease, sessionID string, input []Message, pre *Preemptor) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		rt := c.rt
		ag := runtime.AgentRun{Input: input}
		runCtx := ctx
		originator, hasOriginator := PrincipalFrom(ctx)
		lc := NewLifecycle(c.lifecycleOptions(sessionID, lease, originator, hasOriginator)...)
		if c.foreignRun != nil {
			rctx, frt, fag, _, err := c.foreignRun(ctx, sessionID, stores.History{}, 0, input)
			if err != nil {
				// The run is already acquired: close it as Failed so the
				// lease never dangles behind the error tuple.
				if ferr := lc.finishRun(ctx, runtime.State{}, stores.Failed, nil, types.StopFailed); ferr != nil {
					yield(nil, ferr)
					return
				}
				c.failRun(ctx, lease, 0, yield, err)
				return
			}
			runCtx, rt, ag = rctx, frt, fag
			if ag.History.Version > 0 {
				opts := append(slices.Clone(c.lifecycleOptions(sessionID, lease, originator, hasOriginator)),
					WithLifecycleHistoryVersion(ag.History.Version))
				lc = NewLifecycle(opts...)
			}
		} else if c.newRun != nil {
			rt = runtime.NewNative()
			rctx, run, err := c.newRun(ctx, sessionID, lease, input)
			if err != nil {
				// The run is already acquired: close it as Failed so the
				// lease never dangles behind the error tuple.
				if ferr := lc.finishRun(ctx, runtime.State{}, stores.Failed, nil, types.StopFailed); ferr != nil {
					yield(nil, ferr)
					return
				}
				c.failRun(ctx, lease, 0, yield, err)
				return
			}
			runCtx, ag = rctx, run
			opts := c.lifecycleOptions(sessionID, lease, originator, hasOriginator)
			if ledger, ok := chains.LimitsStateFrom(runCtx); ok {
				opts = append(opts, WithLifecycleLedger(ledger))
			}
			if ag.History.Version > 0 {
				opts = append(opts, WithLifecycleHistoryVersion(ag.History.Version))
			}
			lc = NewLifecycle(opts...)
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
	capacity := DefaultStreamBuffer
	if c.stack != nil && c.stack.streamBuffer > 0 {
		capacity = c.stack.streamBuffer
	}
	q := newStreamHandoff(capacity)
	onBreak := func() {
		if c.stallAction == StallPreempt {
			pre.Preempt()
		}
	}
	go func() {
		defer q.close()
		defer cancelRun()
		defer c.markRunEnded(lease.RunID)
		defer c.dropRun(lease.RunID)
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

	buf  []runTuple
	n    int
	head int

	term   runTuple
	hasTer bool

	closed bool
	broken bool
}

func newStreamHandoff(capacity int) *streamHandoff {
	if capacity < 1 {
		// One ordinary slot minimum: the terminal tuple owns its own.
		capacity = 1
	}
	q := &streamHandoff{buf: make([]runTuple, capacity)}
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
	for q.n == len(q.buf) && !q.broken && !q.closed {
		q.space.Wait()
	}
	if q.broken || q.closed {
		return !q.closed
	}
	q.buf[(q.head+q.n)%len(q.buf)] = runTuple{ev, err}
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
				q.head = (q.head + 1) % len(q.buf)
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
