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

// finishMemo records the last model finish reason a turn produced, so the
// terminal cache-write gate can refuse a max_tokens or refusal finish
// without threading the finish through the runtime state.
type finishMemo struct {
	mu     sync.Mutex
	finish types.FinishReason
}

func (m *finishMemo) record(f types.FinishReason) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finish = f
}

func (m *finishMemo) last() types.FinishReason {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.finish
}

type finishMemoKey struct{}

func withFinishMemo(ctx context.Context, m *finishMemo) context.Context {
	return context.WithValue(ctx, finishMemoKey{}, m)
}

func recordFinish(ctx context.Context, f types.FinishReason) {
	if m, ok := ctx.Value(finishMemoKey{}).(*finishMemo); ok {
		m.record(f)
	}
}

func lastFinishFrom(ctx context.Context) types.FinishReason {
	m, _ := ctx.Value(finishMemoKey{}).(*finishMemo)
	if m == nil {
		return ""
	}
	return m.last()
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
	runs               stores.Runs
	lease              stores.Lease
	hb                 *StreamHeartbeat
	resumeState        *runtime.State
	sessionID          string
	flow               string
	residency          string
	originator         types.Principal
	telemetry          types.Telemetry
	reason             types.SuspendReason
	appender           HistoryAppender
	histVersion        int64
	uncertain          func(runtime.State) []types.CallKey
	verify             types.Verifier
	journal            stores.Journal
	maxTurns           int
	resultRef          string
	waker              Waker
	ledger             *chains.LimitsState
	approval           permission.ApprovalPolicySource
	toolLookup         func(name string) (types.ToolSpec, bool)
	redactor           types.Redactor
	redactionPolicy    types.RedactionPolicy
	redactionMaps      stores.RedactionMaps
	sessions           stores.SessionIndex
	consolidator       MemoryConsolidator
	consolidationAudit stores.AuditLog
	cacheWrite         CacheWriteFunc
	handoff            func(context.Context, types.HandoffContext) error
	handles            types.SandboxHandles
	use                *types.WorkspaceUse
}

// CacheWriteFunc is the terminal cache-write seam DriveLifecycle consults
// after the run closed. It fires only for a clean completion: the gate
// refuses an Uncertain run, a guard block, a refusal, or a max_tokens
// finish, so no entry is ever written for a failure shape.
type CacheWriteFunc func(ctx context.Context, st runtime.State)

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

// WithLifecycleHandoffSink binds the session-side transition a model
// handoff performs at the terminal seam: control moves to
// ControlHandoffRequested and the HandoffContext persists on the session
// index before the run closes.
func WithLifecycleHandoffSink(fn func(context.Context, types.HandoffContext) error) LifecycleOption {
	return func(lc *Lifecycle) { lc.handoff = fn }
}

// WithLifecycleFlow sets the flow spec a suspended checkpoint records.
func WithLifecycleFlow(flow string) LifecycleOption {
	return func(lc *Lifecycle) { lc.flow = flow }
}

// WithLifecycleResidency sets the residency a suspended checkpoint
// records in its run identity.
func WithLifecycleResidency(region string) LifecycleOption {
	return func(lc *Lifecycle) { lc.residency = region }
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

// WithLifecycleSessions binds the session index the handoff terminal
// transition persists its context and control through.
func WithLifecycleSessions(idx stores.SessionIndex) LifecycleOption {
	return func(lc *Lifecycle) { lc.sessions = idx }
}
func WithLifecycleRedaction(redactor types.Redactor, policy types.RedactionPolicy, maps stores.RedactionMaps, sessionID string) LifecycleOption {
	return func(lc *Lifecycle) {
		lc.redactor, lc.redactionPolicy, lc.redactionMaps, lc.sessionID = redactor, policy, maps, sessionID
	}
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

// WithLifecycleVerify injects the verifier that reconciles the session's
// Unknown journal entries before Finish. Each entry the verifier settles
// returns its result to the durable batch record; the rest stay in the
// uncertainty set.
func WithLifecycleVerify(v types.Verifier) LifecycleOption {
	return func(lc *Lifecycle) { lc.verify = v }
}

// WithLifecycleJournal binds the journal whose Unknown entries the
// verifier reconciles. A lifecycle with a verifier and no journal refuses
// the reconciliation instead of skipping it.
func WithLifecycleJournal(j stores.Journal) LifecycleOption {
	return func(lc *Lifecycle) { lc.journal = j }
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

// WithLifecycleConsolidation binds the Done seam that writes the closed
// run's subject memory, and the audit port a failed consolidation records
// through. A lifecycle without one finishes without consolidating.
func WithLifecycleConsolidation(c MemoryConsolidator, audit stores.AuditLog) LifecycleOption {
	return func(lc *Lifecycle) { lc.consolidator, lc.consolidationAudit = c, audit }
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

// WithLifecycleCacheWrite binds the terminal cache-write seam. A lifecycle
// without one writes nothing.
func WithLifecycleCacheWrite(fn CacheWriteFunc) LifecycleOption {
	return func(lc *Lifecycle) { lc.cacheWrite = fn }
}

// WithLifecycleSandboxHandles binds the sandbox coordinator the run's
// suspension and terminal seams publish and release through. A lifecycle
// without one publishes nowhere.
func WithLifecycleSandboxHandles(h types.SandboxHandles) LifecycleOption {
	return func(lc *Lifecycle) { lc.handles = h }
}

// WithLifecycleWorkspaceUse installs the run's acquisition tracker the
// tool path marks on Acquire. The lifecycle reads it at the suspension
// and terminal seams; a run that acquired nothing publishes nowhere.
func WithLifecycleWorkspaceUse(u *types.WorkspaceUse) LifecycleOption {
	return func(lc *Lifecycle) { lc.use = u }
}

// sandboxOwner reports the lease-fenced owner the run's sandbox calls
// acquire, publish and release under.
func (lc *Lifecycle) sandboxOwner() types.WorkspaceOwner {
	return types.WorkspaceOwner{RunID: lc.lease.RunID, LeaseGeneration: lc.lease.Generation}
}

// workspaceLive reports whether the run acquired a sandbox handle the
// lifecycle must publish and release.
func (lc *Lifecycle) workspaceLive() bool {
	return lc.handles != nil && lc.use != nil && lc.use.Acquired() &&
		lc.lease.RunID != "" && lc.sessionID != ""
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
		// The appender binds before Start, so a runtime that records
		// durable per-call state during a Step writes through the same
		// history port the lifecycle owns. Start itself stays
		// binding-only: it runs no governed call and appends nothing.
		if lc.appender != nil {
			ctx = withHistoryAppender(ctx, lc.appender)
		}
		// The sandbox coordinator, the lease-fenced owner and the run's
		// acquisition tracker ride the run context, so the per-session
		// tool path resolves them while it executes and the lifecycle
		// reads the tracker at the suspension and terminal seams. The
		// heartbeat preserves the lease generation, so the owner minted
		// once here stays the fence for the whole run.
		if lc.handles != nil {
			ctx = types.WithSandboxHandles(ctx, lc.handles)
		}
		if lc.use == nil {
			lc.use = &types.WorkspaceUse{}
		}
		ctx = types.WithWorkspaceUse(ctx, lc.use)
		if lc.lease.RunID != "" {
			ctx = types.WithWorkspaceOwner(ctx, lc.sandboxOwner())
		}
		st, err := rt.Start(ctx, r)
		if err != nil {
			// The conversation already holds the lease; the lifecycle
			// owns the failed terminal transition for it.
			_ = lc.finishRun(ctx, runtime.State{}, stores.Failed, nil, types.StopFailed)
			yield(nil, err)
			return
		}
		if lc.resumeState != nil {
			st = copyResumeState(*lc.resumeState)
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
	if st.Foreign != nil {
		fp := *st.Foreign
		fp.ReplayCallIDs = append([]string(nil), st.Foreign.ReplayCallIDs...)
		st.Foreign = &fp
	}
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
	// The loop's own yields and the relay's deliveries share one emitter:
	// an effect can emit while the loop is yielding step events, and the
	// consumer callback must never run twice at once.
	em := &emitter{yield: yield}
	yield = func(ev types.Event, err error) bool { return em.send(ev, err) }
	relay := &sinkRelay{em: em}
	if prev, ok := types.SinkFrom(ctx); ok {
		relay.next = prev
	}
	relay.cancel = cancel
	sctx := types.WithSink(hbCtx, relay)
	notes := &steerNotes{}
	sctx = withSteerNotes(sctx, notes)
	memo := &finishMemo{}
	sctx = withFinishMemo(sctx, memo)
	for {
		if err := hbCtx.Err(); err != nil {
			yield(nil, err)
			return
		}
		next, evs, status, err := rt.Step(sctx, st)
		if relay.hasStopped() {
			// The consumer stopped taking events: the relay already
			// cancelled cancellable work; deliver nothing further.
			return
		}
		var carried types.Done
		hasCarried := false
		if status == runtime.DoneStatus {
			evs, carried, hasCarried = splitStepDone(evs)
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
			if he, ok := errors.AsType[*types.HandoffError](err); ok {
				st = next
				if herr := lc.handoffTerminal(sctx, st, he.Context); herr != nil {
					lc.stopHeartbeat()
					yield(nil, herr)
					return
				}
				lc.stopHeartbeat()
				yield(lc.done(st, nil, types.StopHandedOff), nil)
				return
			}
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
			if ferr := lc.finishRun(sctx, st, stores.Failed, nil, types.StopFailed); ferr != nil {
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
			terminal := types.StopCompleted
			if hasCarried {
				terminal = carried.Reason
			}
			next, evs, again, err := lc.finishTurn(sctx, st, terminal)
			if hasCarried && !again {
				if lc.ledger != nil {
					carried.Cost = lc.ledger.TreeCost()
				}
				evs = withCarriedDone(evs, carried)
			}
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

// splitStepDone lifts a Done the step returned ahead of the terminal
// handling, so the run yields that one Done instead of the driver appending
// a second terminal after it.
func splitStepDone(evs []types.Event) ([]types.Event, types.Done, bool) {
	for i, e := range evs {
		if d, ok := e.(types.Done); ok {
			rest := append(evs[:i:i], evs[i+1:]...)
			return rest, d, true
		}
	}
	return evs, types.Done{}, false
}

// withCarriedDone puts the carried Done in place of the terminal the
// turn end produced, or appends it when the turn end emitted none.
func withCarriedDone(evs []types.Event, d types.Done) []types.Event {
	for i := len(evs) - 1; i >= 0; i-- {
		if _, ok := evs[i].(types.Done); ok {
			evs[i] = d
			return evs
		}
	}
	return append(evs, d)
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
			Residency: lc.residency,
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
		if len(aps) > 0 {
			ap := aps[0]
			payload = permission.ApprovalRequest{
				Tool:        lc.specFor(r, ap.Call.Name),
				Call:        ap.Call,
				Fingerprint: ap.Fingerprint,
				Risk:        ap.Risk,
				Reversible:  ap.Reversible,
				Eligible:    ap.Eligible,
				ArgOrigins:  permission.ArgOrigins(ap.Call.Args, ap.Taints),
			}
		}
	}
	redactedState, err := lc.redactState(ctx, st)
	if err != nil {
		return nil, err
	}
	env.State = redactedState
	// The durable snapshot precedes the save: a snapshot or compare-and-set
	// failure produces no checkpoint and no suspension. A run that acquired
	// no workspace publishes nowhere.
	var snap types.WorkspaceSnapshot
	if lc.workspaceLive() {
		s, perr := lc.handles.Publish(ctx, lc.sessionID, lc.sandboxOwner())
		if perr != nil {
			return nil, fmt.Errorf("suspend workspace publish: %w", perr)
		}
		snap = s
	}
	data, err := encodeCheckpoint(env)
	if err != nil {
		return nil, err
	}
	token, err := r.Save(ctx, stores.Checkpoint{
		RunID:               lc.lease.RunID,
		SchemaVersion:       stores.CurrentSchemaVersion,
		SessionID:           lc.sessionID,
		Flow:                lc.flow,
		Backend:             rt.Name(),
		Reason:              reason,
		Originator:          lc.originator,
		ExpiresAt:           lc.lease.Expires,
		Workspace:           snap.Ref,
		WorkspaceGeneration: snap.Generation,
		Data:                data,
	})
	if err != nil {
		return nil, err
	}
	if lc.runs != nil {
		if sr, ok := lc.runs.(stores.SuspendReasonRecorder); ok {
			if err := sr.SuspendWithReason(ctx, lc.lease, token, reason); err != nil {
				return nil, err
			}
		} else if err := lc.runs.Suspend(ctx, lc.lease, token); err != nil {
			return nil, err
		}
	}
	if reason == types.Scheduled && lc.waker != nil {
		if err := lc.waker.Schedule(ctx, token, wakeAt); err != nil {
			return nil, err
		}
	}
	// The release runs only after the suspension transition and the wake
	// schedule succeeded. A failed close keeps the durable snapshot and the
	// coordinator's cleanup record, which the next sweep retries.
	if lc.workspaceLive() {
		_ = lc.handles.Release(ctx, lc.sessionID, lc.sandboxOwner())
	}
	return types.Suspended{Token: token, Reason: reason, Payload: payload, WakeAt: wakeAt}, nil
}

func (lc *Lifecycle) redactState(ctx context.Context, st runtime.State) (runtime.State, error) {
	if lc.redactor == nil || len(lc.redactionPolicy.Detectors) == 0 {
		return st, nil
	}
	if len(st.Pending) == 0 {
		return st, nil
	}
	msgs := make([]types.Message, len(st.Pending))
	for i, call := range st.Pending {
		msgs[i] = types.Message{Blocks: []types.Block{call}}
	}
	redacted, err := lc.redactMessages(ctx, msgs)
	if err != nil {
		return st, err
	}
	for i := range st.Pending {
		if len(redacted[i].Blocks) > 0 {
			if call, ok := redacted[i].Blocks[0].(types.ToolUse); ok {
				st.Pending[i] = call
			}
		}
	}
	return st, nil
}

func (lc *Lifecycle) redactMessages(ctx context.Context, msgs []types.Message) ([]types.Message, error) {
	if lc.redactor == nil || len(lc.redactionPolicy.Detectors) == 0 {
		return msgs, nil
	}
	blocks := make([]types.Block, 0)
	for _, msg := range msgs {
		blocks = append(blocks, msg.Blocks...)
	}
	redacted, m, err := lc.redactor.Redact(ctx, blocks, lc.redactionPolicy)
	if err != nil {
		return nil, fmt.Errorf("redact history: %w", err)
	}
	if lc.redactionMaps == nil {
		return nil, fmt.Errorf("redaction map store missing")
	}
	info, _ := types.RunInfoFrom(ctx)
	info.SessionID = lc.sessionID
	m.Session, m.Tenant = lc.sessionID, info.Principal.Tenant
	if old, err := lc.redactionMaps.Get(ctx, info.Principal.Tenant, lc.sessionID); err == nil {
		for token, entity := range old.Entries {
			if m.Entries == nil {
				m.Entries = make(map[string]types.Entity)
			}
			m.Entries[token] = entity
		}
	} else if !errors.Is(err, stores.ErrRedactionMapNotFound) {
		return nil, fmt.Errorf("load redaction map: %w", err)
	}
	if err := lc.redactionMaps.Put(ctx, info.Principal.Tenant, lc.sessionID, m); err != nil {
		return nil, fmt.Errorf("store redaction map: %w", err)
	}
	out := append([]types.Message(nil), msgs...)
	offset := 0
	for i := range out {
		n := len(out[i].Blocks)
		out[i].Blocks = append([]types.Block(nil), redacted[offset:offset+n]...)
		offset += n
	}
	return out, nil
}

// specFor resolves one registered tool spec the way approvalsFor does:
// the lifecycle lookup first, then the run's carried tools.
func (lc *Lifecycle) specFor(r runtime.AgentRun, name string) types.ToolSpec {
	if lc.toolLookup != nil {
		if s, ok := lc.toolLookup(name); ok {
			return s
		}
	}
	for _, t := range r.Tools {
		if s := t.Spec(); s.Name == name {
			return s
		}
	}
	return types.ToolSpec{}
}

// approvalsFor builds the persisted approval request for the active ask,
// the head of the pending queue. A missing tool spec or an unresolvable
// policy refuses the suspension instead of persisting a partial approvals
// list.
func (lc *Lifecycle) approvalsFor(ctx context.Context, r runtime.AgentRun, st runtime.State) ([]checkpointApproval, error) {
	b, err := decodeNativeBackend(st)
	if err != nil {
		return nil, err
	}
	var taints []types.ArgTaint
	if b.Driver != nil && b.Driver.Batch != nil {
		taints = b.Driver.Batch.Taints
	}
	specs := make(map[string]types.ToolSpec, len(r.Tools))
	for _, t := range r.Tools {
		specs[t.Spec().Name] = t.Spec()
	}
	lookup := lc.toolLookup
	if lookup == nil {
		lookup = func(name string) (types.ToolSpec, bool) { s, ok := specs[name]; return s, ok }
	}
	aps := make([]checkpointApproval, 0, 1)
	if len(st.Pending) == 0 {
		return nil, nil
	}
	for _, call := range st.Pending[:1] {
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
		elig := permission.Eligibility{Quorum: pol.Quorum}
		if pol.Scope != "" {
			elig.Scopes = []string{pol.Scope}
		}
		if pol.SeparateFromOriginator {
			elig.ExcludeSubjects = []string{lc.originator.Subject}
		}
		aps = append(aps, checkpointApproval{
			Call:        call,
			Risk:        spec.Risk,
			Fingerprint: ToolFingerprint(spec, json.RawMessage(call.Args)),
			Taints:      taints,
			Reversible:  reversible,
			Eligible:    elig,
		})
	}
	return aps, nil
}

// reconcileUnknown enumerates the session's Unknown journal entries and
// reconciles each through the verifier: a settled entry's reconciled
// result returns to the durable batch record, an entry the verifier could
// not settle keeps its key in the uncertainty set. Arguments come from
// the entry the journal reserved, never from projected history.
func (lc *Lifecycle) reconcileUnknown(ctx context.Context, st runtime.State) (runtime.State, []types.CallKey, error) {
	if lc.verify == nil {
		return st, lc.callKeys(st), nil
	}
	if lc.journal == nil || lc.sessionID == "" {
		return st, lc.callKeys(st), fmt.Errorf("gohan: verifier wired without a journal to enumerate unknown entries")
	}
	keys := lc.callKeys(st)
	entries, err := lc.journal.Unknown(ctx, lc.sessionID)
	if err != nil {
		return st, keys, fmt.Errorf("enumerate unknown journal entries: %w", err)
	}
	wanted := make(map[string]bool, len(keys))
	for _, k := range keys {
		wanted[k.CallID] = true
	}
	// An errored or absent Verify leaves the entry uncertain with the
	// outcome the effect recorded; only the keys this drive holds are
	// reconciled.
	pending := make([]stores.Entry, 0, len(entries))
	for _, e := range entries {
		if wanted[e.Key] {
			pending = append(pending, e)
		}
	}
	settledKeys, _, err := reconcileEntries(ctx, lc.sessionID, lc.verify, pending, &st)
	if err != nil {
		return st, keys, err
	}
	if len(settledKeys) == 0 {
		return st, keys, nil
	}
	for _, k := range settledKeys {
		wanted[k.CallID] = false
	}
	rest := make([]types.CallKey, 0, len(keys))
	for _, k := range keys {
		if wanted[k.CallID] {
			rest = append(rest, k)
		}
	}
	return st, rest, nil
}

// handoffTerminal performs the single handoff transition: the
// HandoffContext persists, control moves to ControlHandoffRequested, the
// request is audited once, and the run closes with StopHandedOff and no
// resume token. A bound callback owns the whole session-side transition
// and fires exactly once; without one the lifecycle persists context,
// control and audit directly through the optional session ports.
func (lc *Lifecycle) handoffTerminal(ctx context.Context, st runtime.State, hctx types.HandoffContext) error {
	switch {
	case lc.handoff != nil:
		if err := lc.handoff(ctx, hctx); err != nil {
			return err
		}
	case lc.sessions != nil:
		if err := lc.sessions.UpdateSession(ctx, lc.sessionID, stores.SessionPatch{Handoff: &hctx}); err != nil {
			return err
		}
		if cw, ok := lc.sessions.(SessionControlWriter); ok {
			if err := cw.SetControl(ctx, lc.sessionID, stores.ControlHandoffRequested); err != nil {
				return err
			}
		}
		if a, ok := decisionAuditFrom(ctx); ok {
			if err := a.Append(ctx, stores.AuditRecord{
				Kind:      auditHandoffRequest,
				SessionID: lc.sessionID,
				RunID:     lc.lease.RunID,
				Flow:      lc.flow,
				Decision:  RequestHumanName,
			}); err != nil {
				return err
			}
		}
	}
	return lc.finishRun(ctx, st, stores.Finished, nil, types.StopHandedOff)
}

// finishTurn verifies every Uncertain entry, closes the run and returns
// the terminal events ending in Done. A Finish refused with
// ErrSignalsPending drains the mailbox, appends the steers to history and
// reports one more turn; when MaxTurns is already reached the run ends
// with Done{StopLimit} instead, and a pending cancel stops it with
// Done{cancelled}. The returned state carries the history version the
// drained steers advanced to, so the next step appends against it.
func (lc *Lifecycle) finishTurn(ctx context.Context, st runtime.State, terminal types.StopReason) (runtime.State, []types.Event, bool, error) {
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
	if len(unknown) > 0 {
		var err error
		st, unknown, err = lc.reconcileUnknown(ctx, st)
		if err != nil {
			return st, nil, false, err
		}
	}
	err := lc.finishRun(ctx, st, stores.Finished, unknown, terminal)
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
	lc.writeCacheEntry(ctx, st, terminal, unknown)
	lc.consolidate(ctx)
	return st, []types.Event{lc.done(st, unknown, terminal)}, false, nil
}

// writeCacheEntry applies the never-cache-failures gate before the
// terminal seam fires: only a clean completion with nothing uncertain and
// a cacheable finish writes an entry. A guard block or a refusal never
// reaches this path with a completed terminal reason.
func (lc *Lifecycle) writeCacheEntry(ctx context.Context, st runtime.State, terminal types.StopReason, uncertain []types.CallKey) {
	if lc.cacheWrite == nil || terminal != types.StopCompleted || len(uncertain) > 0 {
		return
	}
	switch lastFinishFrom(ctx) {
	case types.FinishMaxTokens, types.FinishRefusal:
		return
	}
	lc.cacheWrite(ctx, st)
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
		if lc.redactor != nil && len(lc.redactionPolicy.Detectors) > 0 {
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

// finishRun closes the run with the given terminal state. The terminal
// order is Publish → the terminal write → Release: the durable snapshot
// precedes the write, and the handle releases only after the write
// succeeded. A Finish refused with ErrSignalsPending keeps the run, the
// live handle and its heartbeat alive, so no release runs. A terminal
// publication failure records Failed without a successful result ref and
// never repeats the failing publication.
func (lc *Lifecycle) finishRun(ctx context.Context, st runtime.State, state stores.RunState, uncertain []types.CallKey, stop types.StopReason) error {
	if lc.runs == nil {
		return nil
	}
	if lc.workspaceLive() {
		if _, err := lc.handles.Publish(ctx, lc.sessionID, lc.sandboxOwner()); err != nil {
			lc.resultRef = ""
			if ferr := lc.finishStore(ctx, stores.Failed, uncertain, "", stop); ferr != nil {
				return fmt.Errorf("terminal workspace publish: %w (failed write: %w)", err, ferr)
			}
			return fmt.Errorf("terminal workspace publish: %w", err)
		}
	}
	err := lc.finishStore(ctx, state, uncertain, lc.resultRef, stop)
	if err != nil {
		return err
	}
	if lc.workspaceLive() {
		_ = lc.handles.Release(ctx, lc.sessionID, lc.sandboxOwner())
	}
	return nil
}

// finishStore performs the terminal store transition.
func (lc *Lifecycle) finishStore(ctx context.Context, state stores.RunState, uncertain []types.CallKey, resultRef string, stop types.StopReason) error {
	if fr, ok := lc.runs.(stores.FinishReasonRecorder); ok {
		return fr.FinishWithReason(ctx, lc.lease, state, uncertain, resultRef, stop)
	}
	return lc.runs.Finish(ctx, lc.lease, state, uncertain, resultRef)
}

func (lc *Lifecycle) callKeys(st runtime.State) []types.CallKey {
	if lc.uncertain == nil {
		return nil
	}
	return lc.uncertain(st)
}

// consolidate runs the Done seam after the run closed. A failure is
// logged and, while an audit port can still carry a record, audited; it
// never fails the terminal Done.
func (lc *Lifecycle) consolidate(ctx context.Context) {
	if lc.consolidator == nil {
		return
	}
	ri := types.RunInfo{SessionID: lc.sessionID, RunID: lc.lease.RunID, Principal: lc.originator}
	if err := lc.consolidator.Run(ctx, ri); err != nil {
		consolidateFailure(ctx, lc.consolidationAudit, ri, err)
	}
}

func (lc *Lifecycle) done(st runtime.State, uncertain []types.CallKey, reason types.StopReason) types.Event {
	d := types.Done{Reason: reason, Usage: st.Usage, Uncertain: uncertain}
	if lc.ledger != nil {
		d.Cost = lc.ledger.TreeCost()
		d.Usage.SandboxSeconds = lc.ledger.TreeSandboxSeconds()
	}
	return d
}
