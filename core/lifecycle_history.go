package gohan

import (
	"context"

	"github.com/victorzhuk/gohan/core/chains"
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

// WithLifecycleAppender binds the history port a drained steer is appended
// through.
func WithLifecycleAppender(h HistoryAppender) LifecycleOption {
	return func(lc *Lifecycle) { lc.appender = h }
}

// WithLifecycleHistoryVersion sets the history version the session log
// holds when the drive starts, so the first turn append expects it.
func WithLifecycleHistoryVersion(v int64) LifecycleOption {
	return func(lc *Lifecycle) { lc.histVersion = v }
}

// WithLifecycleUncertain injects the collector for the journal keys whose
// outcome is still unknown at the end of the run.
func WithLifecycleUncertain(fn func(runtime.State) []types.CallKey) LifecycleOption {
	return func(lc *Lifecycle) { lc.uncertain = fn }
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

// WithLifecycleLedger binds the run's accounting ledger. DriveLifecycle
// carries it in the context every effect runs under, so a resumed or
// recovered run charges the cost it already spent instead of a fresh
// ledger, and the terminal Done projects the accrued cost.
func WithLifecycleLedger(s *chains.LimitsState) LifecycleOption {
	return func(lc *Lifecycle) { lc.ledger = s }
}

// safePoint drains the mailbox and acts on what it holds: a cancel or a
// steer past MaxTurns ends the run with its terminal transition and Done,
// a steer otherwise reaches the next model request. It refuses to start
// further work when the lease is lost.
func (lc *Lifecycle) safePoint(ctx context.Context, st runtime.State) (runtime.State, []types.Event, bool, error) {
	if err := lc.heartbeatErr(); err != nil {
		return st, nil, true, err
	}
	if lc.runs == nil {
		return st, nil, false, nil
	}
	sigs, err := lc.runs.Drain(ctx, lc.lease)
	if err != nil {
		return st, nil, true, err
	}
	st, terminal, _, evs, err := lc.applySignals(ctx, st, sigs)
	if err != nil {
		return st, nil, true, err
	}
	return st, evs, terminal, nil
}

// applySignals acts on the drained mailbox. A cancel stops the run with
// Done{cancelled}; drained steers are appended to history and, past
// MaxTurns, end the run with Done{StopLimit} instead of another turn.
func (lc *Lifecycle) applySignals(ctx context.Context, st runtime.State, sigs []stores.Signal) (runtime.State, bool, bool, []types.Event, error) {
	var evs []types.Event
	steered := false
	for _, sig := range sigs {
		if sig.Kind == stores.SignalCancel {
			if err := lc.finishRun(ctx, st, stores.Finished, nil, types.StopCancelled); err != nil {
				return st, true, false, nil, err
			}
			return st, true, false, []types.Event{lc.done(st, nil, types.StopCancelled)}, nil
		}
		if lc.appender == nil {
			// A drive-only lifecycle has no session history to append to;
			// the steer still reaches the next assembly, without the
			// SteerApplied acknowledgement.
			if n := steerNotesFrom(ctx); n != nil {
				n.add(sig.Message)
			}
			steered = true
			continue
		}
		messageID := sig.Message.ID
		msg := sig.Message
		if lc.redaction.redactor != nil && len(lc.redaction.policy.Detectors) > 0 {
			redacted, err := lc.redactMessages(ctx, []types.Message{sig.Message})
			if err != nil {
				return st, false, false, nil, err
			}
			msg = redacted[0]
			msg.ID = messageID
		}
		v, aerr := lc.appender.Append(ctx, st.HistoryVersion, msg)
		if aerr != nil {
			return st, false, false, nil, aerr
		}
		sig.Message = msg
		st.HistoryVersion = v
		if n := steerNotesFrom(ctx); n != nil {
			n.add(sig.Message)
		}
		evs = append(evs, types.SteerApplied{MessageID: messageID})
		steered = true
	}
	if !steered {
		return st, false, false, nil, nil
	}
	if lc.maxTurns > 0 && st.Turn >= lc.maxTurns {
		if tel := telemetryFrom(ctx); tel != nil {
			tel.Count(ctx, "gohan.max_turns.reached", 1)
		}
		if err := lc.finishRun(ctx, st, stores.Finished, lc.callKeys(st), types.StopLimit); err != nil {
			return st, true, false, nil, err
		}
		return st, true, false, append(evs, lc.done(st, nil, types.StopLimit)), nil
	}
	return st, false, true, evs, nil
}

func (lc *Lifecycle) callKeys(st runtime.State) []types.CallKey {
	if lc.uncertain == nil {
		return nil
	}
	return lc.uncertain(st)
}
