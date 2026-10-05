package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// steerNotes carries the steers a safe point drained to the next model
// effect, which folds them into its working history. The lifecycle owns
// the mailbox; the effect owns the history it assembles from.
type steerNotes struct {
	mu   sync.Mutex
	msgs []types.Message
}

func (n *steerNotes) add(m types.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.msgs = append(n.msgs, m)
}

func (n *steerNotes) take() []types.Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	msgs := n.msgs
	n.msgs = nil
	return msgs
}

type steerNotesKey struct{}

func withSteerNotes(ctx context.Context, n *steerNotes) context.Context {
	return context.WithValue(ctx, steerNotesKey{}, n)
}

func steerNotesFrom(ctx context.Context) *steerNotes {
	n, _ := ctx.Value(steerNotesKey{}).(*steerNotes)
	return n
}

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
	runs        stores.Runs
	lease       stores.Lease
	hb          *StreamHeartbeat
	resumeState *runtime.State
	sessionID   string
	flow        string
	originator  types.Principal
	telemetry   types.Telemetry
	reason      types.SuspendReason
	appender    HistoryAppender
	histVersion int64
	uncertain   func(runtime.State) []types.CallKey
	verify      func(ctx context.Context, keys []types.CallKey) ([]types.CallKey, error)
	maxTurns    int
	resultRef   string
	waker       Waker
	ledger      *chains.LimitsState
	approval    permission.ApprovalPolicySource
	toolLookup  func(name string) (types.ToolSpec, bool)
}

// LifecycleOption configures a Lifecycle.
type LifecycleOption func(*Lifecycle)

// WithLifecycleRuns binds the runs store and the lease Drive holds. Both
// are required for suspension and Finish; without them the driver only
// orders events.
func WithLifecycleRuns(r stores.Runs, l stores.Lease) LifecycleOption {
	return func(lc *Lifecycle) { lc.runs, lc.lease = r, l }
}

// WithLifecycleResumeState drives the state a checkpoint carried instead of
// the position Runtime.Start returns. Start still runs once to install the
// per-run wiring; the saved state then replaces its initial position, so
// initial effects never replay.
func WithLifecycleResumeState(st runtime.State) LifecycleOption {
	return func(lc *Lifecycle) { lc.resumeState = &st }
}

// WithLifecycleLease sets the externally acquired lease the lifecycle
// holds: every Drain, Suspend, Finish and heartbeat refresh runs under it.
// A later option replaces an earlier lease binding.
func WithLifecycleLease(l stores.Lease) LifecycleOption {
	return func(lc *Lifecycle) { lc.lease = l }
}

// WithLifecycleFlow sets the flow spec a suspended checkpoint records.
func WithLifecycleFlow(flow string) LifecycleOption {
	return func(lc *Lifecycle) { lc.flow = flow }
}

// WithLifecycleOriginator sets the principal a suspended checkpoint
// restores its credentials for.
func WithLifecycleOriginator(p types.Principal) LifecycleOption {
	return func(lc *Lifecycle) { lc.originator = p }
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

// WithLifecycleLedger binds the run's accounting ledger. DriveLifecycle
// carries it in the context every effect runs under, so a resumed or
// recovered run charges the cost it already spent instead of a fresh
// ledger, and the terminal Done projects the accrued cost.
func WithLifecycleLedger(s *chains.LimitsState) LifecycleOption {
	return func(lc *Lifecycle) { lc.ledger = s }
}

// WithLifecycleApprovalPolicy binds the source the suspension resolves each
// pending request's approval policy through. A HumanApproval suspension
// without one refuses the approval path.
func WithLifecycleApprovalPolicy(src permission.ApprovalPolicySource) LifecycleOption {
	return func(lc *Lifecycle) { lc.approval = src }
}

// WithLifecycleToolSpecs binds the registered tool-spec lookup a suspension
// resolves each pending call's declaration through. It takes precedence
// over the run's carried tools.
func WithLifecycleToolSpecs(lookup func(name string) (types.ToolSpec, bool)) LifecycleOption {
	return func(lc *Lifecycle) { lc.toolLookup = lookup }
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
		if lc.ledger != nil {
			// The ledger travels in the run context for the whole run, so
			// the effects charge the same ledger a resume restored.
			ctx = chains.WithLimitsState(ctx, lc.ledger)
		}
		st, err := rt.Start(ctx, r)
		if err != nil {
			// The conversation already holds the lease; the lifecycle
			// owns the failed terminal transition for it.
			_ = lc.finishRun(ctx, runtime.State{}, stores.Failed, nil)
			yield(nil, err)
			return
		}
		if lc.resumeState != nil {
			st = copyResumeState(*lc.resumeState)
		}
		if lc.appender != nil {
			ctx = withHistoryAppender(ctx, lc.appender)
		}
		if lc.histVersion > st.HistoryVersion {
			st.HistoryVersion = lc.histVersion
		}
		if lc.runs != nil {
			// The heartbeat starts as soon as the lifecycle holds the
			// lease and stops before the iterator returns.
			lc.hb = StartStreamHeartbeat(ctx, lc.runs, lc.lease, 0)
		}
		lc.drive(ctx, rt, r, st, yield)
	}
}

// copyResumeState copies the resumable mutable data of a saved state, so
// the checkpoint bytes the caller keeps can never alias the driven state.
func copyResumeState(st runtime.State) runtime.State {
	st.Pending = append([]types.ToolUse(nil), st.Pending...)
	st.ActiveTools = append([]string(nil), st.ActiveTools...)
	st.Backend = append([]byte(nil), st.Backend...)
	calib := make(map[string]float64, len(st.Calibration))
	for k, v := range st.Calibration {
		calib[k] = v
	}
	st.Calibration = calib
	return st
}

// heartbeatErr reports a lost lease: the driver starts no further effect.
func (lc *Lifecycle) heartbeatErr() error {
	if lc.hb == nil {
		return nil
	}
	if err := lc.hb.Err(); err != nil {
		return fmt.Errorf("run lease lost: %w", err)
	}
	return nil
}

// stopHeartbeat joins the heartbeat helper and adopts the last lease it
// held. Every terminal store transition runs after it.
func (lc *Lifecycle) stopHeartbeat() {
	if lc.hb == nil {
		return
	}
	lc.lease = lc.hb.Stop()
	lc.hb = nil
}

// drive is the one stepping loop Drive, DriveResume and DriveLifecycle
// share: initial and resumed execution differ only in the state they enter
// with.
func (lc *Lifecycle) drive(ctx context.Context, rt runtime.Runtime, r runtime.AgentRun, st runtime.State, yield func(types.Event, error) bool) {
	hbCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Every exit stops the heartbeat; the terminal branches below stop it
	// before their last event so no refresh follows a terminal write.
	defer lc.stopHeartbeat()
	relay := &sinkRelay{}
	if prev, ok := types.SinkFrom(ctx); ok {
		relay.next = prev
	}
	relay.cancel = cancel
	relay.deliver = func(e types.Event) bool { return yield(e, nil) }
	sctx := types.WithSink(hbCtx, relay)
	notes := &steerNotes{}
	sctx = withSteerNotes(sctx, notes)
	for {
		if err := hbCtx.Err(); err != nil {
			yield(nil, err)
			return
		}
		next, evs, status, err := rt.Step(sctx, st)
		if relay.stopped {
			// The consumer stopped taking events: the relay already
			// cancelled cancellable work; deliver nothing further.
			return
		}
		for _, e := range evs {
			if !yield(e, nil) {
				return
			}
		}
		if hErr := lc.heartbeatErr(); hErr != nil {
			// Ownership is gone: cancel cancellable work, record no
			// terminal transition and run nothing further.
			cancel()
			lc.stopHeartbeat()
			yield(nil, hErr)
			return
		}
		if err != nil {
			if se, ok := errors.AsType[*types.SuspendError](err); ok {
				st = next
				ev, serr := lc.suspend(sctx, rt, r, st, se)
				lc.stopHeartbeat()
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
			lc.stopHeartbeat()
			yield(nil, err)
			return
		}
		st = next
		switch status {
		case runtime.SuspendedStatus:
			ev, err := lc.suspend(sctx, rt, r, st, nil)
			lc.stopHeartbeat()
			if err != nil {
				yield(nil, err)
				return
			}
			yield(ev, nil)
			return
		case runtime.DoneStatus:
			next, evs, again, err := lc.finishTurn(sctx, st)
			for _, e := range evs {
				if !yield(e, nil) {
					return
				}
			}
			if err != nil {
				lc.stopHeartbeat()
				yield(nil, err)
				return
			}
			st = next
			if again {
				st.Turn++
				continue
			}
			lc.stopHeartbeat()
			return
		}
		if nextPhaseIsModel(st.Backend) {
			// A batch just settled: reach the same safe point the loop
			// reaches between turns, so a suspension or a steer that
			// arrived during the batch is observed before the next model
			// effect instead of after it.
			var evs []types.Event
			var terminal bool
			st, evs, terminal, err = lc.safePoint(sctx, st)
			for _, e := range evs {
				if !yield(e, nil) {
					return
				}
			}
			if err != nil || terminal {
				if err != nil {
					lc.stopHeartbeat()
					yield(nil, err)
				}
				return
			}
		}
	}
}

// nextPhaseIsModel reports whether the state a step returned carries the
// model phase, which under the effect-granular native runtime means the
// step that produced it settled a batch.
func nextPhaseIsModel(backend []byte) bool {
	var p struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(backend, &p); err != nil {
		return false
	}
	return p.Phase == "model"
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
	env := checkpointEnvelope{
		Run: types.RunInfo{
			Flow:      lc.flow,
			SessionID: lc.sessionID,
			RunID:     lc.lease.RunID,
			Principal: lc.originator,
		},
		Generation: 1,
		State:      st,
	}
	if reason == types.HumanApproval {
		aps, aerr := lc.approvalsFor(ctx, r, st)
		if aerr != nil {
			return nil, aerr
		}
		env.Approvals = aps
	}
	data, err := encodeCheckpoint(env)
	if err != nil {
		return nil, err
	}
	token, err := r.Save(ctx, stores.Checkpoint{
		RunID:         lc.lease.RunID,
		SchemaVersion: stores.CurrentSchemaVersion,
		SessionID:     lc.sessionID,
		Flow:          lc.flow,
		Backend:       rt.Name(),
		Reason:        reason,
		Originator:    lc.originator,
		ExpiresAt:     lc.lease.Expires,
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

// approvalsFor builds one persisted approval request per approval-controlled
// pending call. A missing tool spec or an unresolvable policy refuses the
// suspension instead of persisting a partial approvals list.
func (lc *Lifecycle) approvalsFor(ctx context.Context, r runtime.AgentRun, st runtime.State) ([]checkpointApproval, error) {
	specs := make(map[string]types.ToolSpec, len(r.Tools))
	for _, t := range r.Tools {
		specs[t.Spec().Name] = t.Spec()
	}
	lookup := lc.toolLookup
	if lookup == nil {
		lookup = func(name string) (types.ToolSpec, bool) { s, ok := specs[name]; return s, ok }
	}
	aps := make([]checkpointApproval, 0, len(st.Pending))
	for _, call := range st.Pending {
		spec, ok := lookup(call.Name)
		if !ok {
			return nil, fmt.Errorf("%w: pending call %q has no registered tool", types.ErrCheckpointIncompatible, call.Name)
		}
		if lc.approval == nil {
			return nil, fmt.Errorf("%w: no approval policy wired", types.ErrApproverNotEligible)
		}
		reversible := spec.Effect != types.SideEffect
		pol, err := lc.approval.ApprovalPolicy(ctx, spec.Risk, call.Name, reversible)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve approval policy: %s", types.ErrApproverNotEligible, err)
		}
		elig := permission.Eligibility{Scopes: []string{pol.Scope}, Quorum: pol.Quorum}
		if pol.SeparateFromOriginator {
			elig.ExcludeSubjects = []string{lc.originator.Subject}
		}
		aps = append(aps, checkpointApproval{
			Call:        call,
			Risk:        spec.Risk,
			Fingerprint: ToolFingerprint(spec, json.RawMessage(call.Args)),
			Reversible:  reversible,
			Eligible:    elig,
		})
	}
	return aps, nil
}

// finishTurn verifies every Uncertain entry, closes the run and returns
// the terminal events ending in Done. A Finish refused with
// ErrSignalsPending drains the mailbox, appends the steers to history and
// reports one more turn; when MaxTurns is already reached the run ends
// with Done{StopLimit} instead, and a pending cancel stops it with
// Done{cancelled}. The returned state carries the history version the
// drained steers advanced to, so the next step appends against it.
func (lc *Lifecycle) finishTurn(ctx context.Context, st runtime.State) (runtime.State, []types.Event, bool, error) {
	if lc.runs != nil {
		sigs, err := lc.runs.Drain(ctx, lc.lease)
		if err != nil {
			return st, nil, false, err
		}
		st, terminal, again, evs, err := lc.applySignals(ctx, st, sigs)
		if err != nil || terminal || again {
			return st, evs, again, err
		}
	}
	unknown := lc.callKeys(st)
	if lc.verify != nil && len(unknown) > 0 {
		rest, err := lc.verify(ctx, unknown)
		if err != nil {
			return st, nil, false, err
		}
		unknown = rest
	}
	err := lc.finishRun(ctx, st, stores.Finished, unknown)
	if errors.Is(err, types.ErrSignalsPending) {
		// A steer arrived between the safe point and Finish: drain it and
		// run one more turn.
		if lc.runs == nil {
			return st, nil, false, fmt.Errorf("gohan: %w without a runs store", types.ErrSignalsPending)
		}
		sigs, derr := lc.runs.Drain(ctx, lc.lease)
		if derr != nil {
			return st, nil, false, derr
		}
		st, terminal, again, evs, aerr := lc.applySignals(ctx, st, sigs)
		if aerr != nil {
			return st, nil, false, aerr
		}
		if !terminal && !again {
			return st, nil, false, fmt.Errorf("gohan: %w reported but the mailbox drained empty", types.ErrSignalsPending)
		}
		return st, evs, again, nil
	}
	if err != nil {
		return st, nil, false, err
	}
	return st, []types.Event{lc.done(st, unknown, types.StopCompleted)}, false, nil
}

// applySignals acts on the drained mailbox. A cancel stops the run with
// Done{cancelled}; drained steers are appended to history and, past
// MaxTurns, end the run with Done{StopLimit} instead of another turn.
func (lc *Lifecycle) applySignals(ctx context.Context, st runtime.State, sigs []stores.Signal) (runtime.State, bool, bool, []types.Event, error) {
	var evs []types.Event
	steered := false
	for _, sig := range sigs {
		if sig.Kind == stores.SignalCancel {
			if err := lc.finishRun(ctx, st, stores.Finished, nil); err != nil {
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
		v, aerr := lc.appender.Append(ctx, st.HistoryVersion, sig.Message)
		if aerr != nil {
			return st, false, false, nil, aerr
		}
		st.HistoryVersion = v
		if n := steerNotesFrom(ctx); n != nil {
			n.add(sig.Message)
		}
		evs = append(evs, types.SteerApplied{MessageID: sig.Message.ID})
		steered = true
	}
	if !steered {
		return st, false, false, nil, nil
	}
	if lc.maxTurns > 0 && st.Turn >= lc.maxTurns {
		if err := lc.finishRun(ctx, st, stores.Finished, lc.callKeys(st)); err != nil {
			return st, true, false, nil, err
		}
		return st, true, false, append(evs, lc.done(st, nil, types.StopLimit)), nil
	}
	return st, false, true, evs, nil
}

// finishRun closes the run with the given terminal state. A Finish refused
// with ErrSignalsPending keeps the run alive, so the heartbeat survives it.
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
	d := types.Done{Reason: reason, Usage: st.Usage, Uncertain: uncertain}
	if lc.ledger != nil {
		d.Cost = lc.ledger.TreeCost()
	}
	return d
}
