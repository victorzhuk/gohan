package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// HistoryAppender is the append port a turn uses, with the session id
// already bound. expectedVersion is mandatory: an append against a stale
// version fails with types.ErrVersionConflict.
type HistoryAppender interface {
	Append(ctx context.Context, expectedVersion int64, msgs ...types.Message) (int64, error)
}

// AppendFunc adapts a bound stores.SessionLog.Append as a HistoryAppender.
type AppendFunc func(ctx context.Context, expectedVersion int64, msgs ...types.Message) (int64, error)

// Append calls f.
func (f AppendFunc) Append(ctx context.Context, expectedVersion int64, msgs ...types.Message) (int64, error) {
	return f(ctx, expectedVersion, msgs...)
}

// TurnShape says where in the step a turn's messages are appended.
type TurnShape int

const (
	// ShapeStepEnd merges the assistant message and the batch results into
	// one append at step end. A turn with no calls or only ReadOnly calls
	// takes this shape.
	ShapeStepEnd TurnShape = iota
	// ShapeBeforeBatch appends the assistant message with its pending calls
	// before the batch executes. A turn carrying Idempotent or SideEffect
	// calls takes this shape, so a crash mid-batch recovers the calls the
	// run had started.
	ShapeBeforeBatch
)

// ShapeFor derives the turn's append shape from the call effects.
func ShapeFor(effects ...types.Effect) TurnShape {
	for _, e := range effects {
		if e == types.Idempotent || e == types.SideEffect {
			return ShapeBeforeBatch
		}
	}
	return ShapeStepEnd
}

// AppendBeforeBatch appends the assistant message carrying its pending
// calls before the batch executes and returns the advanced history
// version.
func AppendBeforeBatch(ctx context.Context, h HistoryAppender, expected int64, asst types.Message) (int64, error) {
	return h.Append(ctx, expected, asst)
}

// AppendStepEnd appends the assistant message and the batch results in one
// append at step end and returns the advanced history version. Results
// carry no calls of their own: one Append advances the version once.
func AppendStepEnd(ctx context.Context, h HistoryAppender, expected int64, asst types.Message, results ...types.Message) (int64, error) {
	msgs := make([]types.Message, 0, 1+len(results))
	msgs = append(msgs, asst)
	msgs = append(msgs, results...)
	return h.Append(ctx, expected, msgs...)
}

// Lifecycle carries the run-scoped handles DriveLifecycle needs to enforce
// the lifecycle ordering runtime/spec.md fixes: history append before the
// gate, Checkpoints.Put before Runs.Suspend, Runs.Finish before Done.
type Lifecycle struct {
	runs      stores.Runs
	lease     stores.Lease
	sessionID string
	telemetry types.Telemetry
	reason    types.SuspendReason
	appender  HistoryAppender
	uncertain func(runtime.State) []types.CallKey
	verify    func(ctx context.Context, keys []types.CallKey) ([]types.CallKey, error)
	maxTurns  int
	resultRef string
	waker     Waker
}

// LifecycleOption configures a Lifecycle.
type LifecycleOption func(*Lifecycle)

// WithLifecycleRuns binds the runs store and the lease Drive holds. Both
// are required for suspension and Finish; without them the driver only
// orders events.
func WithLifecycleRuns(r stores.Runs, l stores.Lease) LifecycleOption {
	return func(lc *Lifecycle) { lc.runs, lc.lease = r, l }
}

// WithLifecycleSession sets the session id a checkpoint records.
func WithLifecycleSession(id string) LifecycleOption {
	return func(lc *Lifecycle) { lc.sessionID = id }
}

// WithLifecycleAppender binds the history port a drained steer is appended
// through.
func WithLifecycleAppender(h HistoryAppender) LifecycleOption {
	return func(lc *Lifecycle) { lc.appender = h }
}

// WithLifecycleUncertain injects the collector for the journal keys whose
// outcome is still unknown at the end of the run.
func WithLifecycleUncertain(fn func(runtime.State) []types.CallKey) LifecycleOption {
	return func(lc *Lifecycle) { lc.uncertain = fn }
}

// WithLifecycleVerify injects the verifier that reconciles unknown entries
// before Finish. It returns the keys still unknown after verification.
func WithLifecycleVerify(fn func(ctx context.Context, keys []types.CallKey) ([]types.CallKey, error)) LifecycleOption {
	return func(lc *Lifecycle) { lc.verify = fn }
}

// WithLifecycleMaxTurns bounds the turns a run takes. A steer that arrives
// after the last drain forces one more turn only when the bound is not yet
// reached; otherwise the run ends with Done{StopLimit}.
func WithLifecycleMaxTurns(n int) LifecycleOption {
	return func(lc *Lifecycle) { lc.maxTurns = n }
}

// WithLifecycleResultRef sets the result reference Finish records.
func WithLifecycleResultRef(ref string) LifecycleOption {
	return func(lc *Lifecycle) { lc.resultRef = ref }
}

// WithLifecycleSuspendReason overrides the reason a checkpoint carries;
// the default is types.AwaitingTool.
func WithLifecycleSuspendReason(r types.SuspendReason) LifecycleOption {
	return func(lc *Lifecycle) { lc.reason = r }
}

// WithLifecycleTelemetry binds the emission port the run span and the
// governed counters record through. A lifecycle without one emits nothing.
func WithLifecycleTelemetry(t types.Telemetry) LifecycleOption {
	return func(lc *Lifecycle) { lc.telemetry = t }
}

// NewLifecycle builds the driver-side lifecycle with its defaults.
func NewLifecycle(opts ...LifecycleOption) *Lifecycle {
	lc := &Lifecycle{reason: types.AwaitingTool}
	for _, opt := range opts {
		opt(lc)
	}
	return lc
}

// DriveLifecycle runs the Drive loop under the lifecycle ordering: the
// appended history precedes the gate inside the runtime's step, suspension
// persists the checkpoint before Runs.Suspend and emits Suspended last,
// and a terminal Done follows verification of every Uncertain entry and
// Runs.Finish.
func DriveLifecycle(ctx context.Context, lc *Lifecycle, rt runtime.Runtime, r runtime.AgentRun) iter.Seq2[types.Event, error] {
	return func(yield func(types.Event, error) bool) {
		if lc == nil {
			lc = NewLifecycle()
		}
		ctx, endSpan := startRunSpan(ctx, lc.telemetry)
		defer endSpan()
		st, err := rt.Start(ctx, r)
		if err != nil {
			yield(nil, err)
			return
		}
		relay := &sinkRelay{}
		if prev, ok := types.SinkFrom(ctx); ok {
			relay.next = prev
		}
		sctx := types.WithSink(ctx, relay)
		for {
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}
			for _, e := range relay.take() {
				if !yield(e, nil) {
					return
				}
			}
			next, evs, status, err := rt.Step(sctx, st)
			for _, e := range relay.take() {
				if !yield(e, nil) {
					return
				}
			}
			for _, e := range evs {
				if !yield(e, nil) {
					return
				}
			}
			if err != nil {
				if se, ok := errors.AsType[*types.SuspendError](err); ok {
					ev, serr := lc.suspend(sctx, rt, r, st, se)
					if serr != nil {
						yield(nil, serr)
						return
					}
					yield(ev, nil)
					return
				}
				if ferr := lc.finishRun(sctx, st, stores.Failed, nil); ferr != nil {
					yield(nil, ferr)
					return
				}
				yield(nil, err)
				return
			}
			st = next
			switch status {
			case runtime.SuspendedStatus:
				ev, err := lc.suspend(sctx, rt, r, st, nil)
				if err != nil {
					yield(nil, err)
					return
				}
				yield(ev, nil)
				return
			case runtime.DoneStatus:
				evs, again, err := lc.finishTurn(sctx, st)
				for _, e := range evs {
					if !yield(e, nil) {
						return
					}
				}
				if err != nil {
					yield(nil, err)
					return
				}
				if again {
					st.Turn++
					continue
				}
				return
			}
		}
	}
}

// suspend persists the checkpoint, releases the lease and only then emits
// Suspended: the order runtime/spec.md fixes. A crash before Suspend leaves
// the run to the reaper; a crash after it leaves the client the EventLog.
// sig, when the step returned a SuspendError, supplies the reason, the
// payload and the wake time the event carries; a Scheduled wake arms the
// waker exactly once, after the run is marked suspended.
func (lc *Lifecycle) suspend(ctx context.Context, rt runtime.Runtime, r runtime.AgentRun, st runtime.State, sig *types.SuspendError) (types.Event, error) {
	reason, payload, wakeAt := lc.reason, any(nil), time.Time{}
	if sig != nil {
		reason, payload, wakeAt = sig.Reason, sig.Payload, sig.WakeAt
	}
	data, err := json.Marshal(st)
	if err != nil {
		return nil, fmt.Errorf("gohan: encode checkpoint: %w", err)
	}
	token, err := r.Save(ctx, stores.Checkpoint{
		SchemaVersion: stores.CurrentSchemaVersion,
		SessionID:     lc.sessionID,
		Backend:       rt.Name(),
		Reason:        reason,
		Data:          data,
	})
	if err != nil {
		return nil, err
	}
	if lc.runs != nil {
		if err := lc.runs.Suspend(ctx, lc.lease, token); err != nil {
			return nil, err
		}
	}
	if reason == types.Scheduled && lc.waker != nil {
		if err := lc.waker.Schedule(ctx, token, wakeAt); err != nil {
			return nil, err
		}
	}
	return types.Suspended{Token: token, Reason: reason, Payload: payload, WakeAt: wakeAt}, nil
}

// finishTurn verifies every Uncertain entry, closes the run and returns
// the terminal events ending in Done. A Finish refused with
// ErrSignalsPending drains the mailbox, appends the steers to history and
// reports one more turn; when MaxTurns is already reached the run ends
// with Done{StopLimit} instead, and a pending cancel stops it with
// Done{cancelled}.
func (lc *Lifecycle) finishTurn(ctx context.Context, st runtime.State) ([]types.Event, bool, error) {
	if lc.runs != nil {
		sigs, err := lc.runs.Drain(ctx, lc.lease)
		if err != nil {
			return nil, false, err
		}
		terminal, again, evs, err := lc.applySignals(ctx, st, sigs)
		if err != nil || terminal || again {
			return evs, again, err
		}
	}
	unknown := lc.callKeys(st)
	if lc.verify != nil && len(unknown) > 0 {
		rest, err := lc.verify(ctx, unknown)
		if err != nil {
			return nil, false, err
		}
		unknown = rest
	}
	err := lc.finishRun(ctx, st, stores.Finished, unknown)
	if errors.Is(err, types.ErrSignalsPending) {
		// A steer arrived between the safe point and Finish: drain it and
		// run one more turn.
		if lc.runs == nil {
			return nil, false, fmt.Errorf("gohan: %w without a runs store", types.ErrSignalsPending)
		}
		sigs, derr := lc.runs.Drain(ctx, lc.lease)
		if derr != nil {
			return nil, false, derr
		}
		terminal, again, evs, aerr := lc.applySignals(ctx, st, sigs)
		if aerr != nil {
			return nil, false, aerr
		}
		if !terminal && !again {
			return nil, false, fmt.Errorf("gohan: %w reported but the mailbox drained empty", types.ErrSignalsPending)
		}
		return evs, again, nil
	}
	if err != nil {
		return nil, false, err
	}
	return []types.Event{lc.done(st, unknown, types.StopCompleted)}, false, nil
}

// applySignals acts on the drained mailbox. A cancel stops the run with
// Done{cancelled}; drained steers are appended to history and, past
// MaxTurns, end the run with Done{StopLimit} instead of another turn.
func (lc *Lifecycle) applySignals(ctx context.Context, st runtime.State, sigs []stores.Signal) (terminal, again bool, evs []types.Event, err error) {
	steered := false
	for _, sig := range sigs {
		if sig.Kind == stores.SignalCancel {
			if err := lc.finishRun(ctx, st, stores.Finished, nil); err != nil {
				return true, false, nil, err
			}
			return true, false, []types.Event{lc.done(st, nil, types.StopCancelled)}, nil
		}
		if lc.appender != nil {
			v, aerr := lc.appender.Append(ctx, st.HistoryVersion, sig.Message)
			if aerr != nil {
				return false, false, nil, aerr
			}
			st.HistoryVersion = v
		}
		evs = append(evs, types.SteerApplied{MessageID: sig.Message.ID})
		steered = true
	}
	if !steered {
		return false, false, nil, nil
	}
	if lc.maxTurns > 0 && st.Turn >= lc.maxTurns {
		if err := lc.finishRun(ctx, st, stores.Finished, lc.callKeys(st)); err != nil {
			return true, false, nil, err
		}
		return true, false, append(evs, lc.done(st, nil, types.StopLimit)), nil
	}
	return false, true, evs, nil
}

// finishRun closes the run with the given terminal state.
func (lc *Lifecycle) finishRun(ctx context.Context, st runtime.State, state stores.RunState, uncertain []types.CallKey) error {
	if lc.runs == nil {
		return nil
	}
	return lc.runs.Finish(ctx, lc.lease, state, uncertain, lc.resultRef)
}

func (lc *Lifecycle) callKeys(st runtime.State) []types.CallKey {
	if lc.uncertain == nil {
		return nil
	}
	return lc.uncertain(st)
}

func (lc *Lifecycle) done(st runtime.State, uncertain []types.CallKey, reason types.StopReason) types.Event {
	return types.Done{Reason: reason, Usage: st.Usage, Uncertain: uncertain}
}
