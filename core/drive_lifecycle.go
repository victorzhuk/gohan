package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"sync"

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
	redaction          redactionBundle
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
