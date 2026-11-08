package gohan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// errRecoverStoresRequired marks a Recover against a stack built without
// the runs store: there is nothing to reclaim from.
var errRecoverStoresRequired = fmt.Errorf("recover stack: %w", types.ErrRunNotActive)

// recoverStaleAfter is the heartbeat age Recover treats as a dead lease.
// It matches the lease TTL: a run whose lease expired is stale by
// definition, and a shorter window would race the live heartbeat.
const recoverStaleAfter = stores.LeaseTTL

// Recover reclaims runs whose lease expired and re-drives each from the
// last persisted turn. Completed journal calls replay their recorded
// result, reserved ones re-execute with their pinned key, and calls that
// never started execute normally. It is idempotent: of concurrent
// reapers exactly one reclaims a run, the rest skip it.
//
// The caller carries the harness principal the session log scopes its
// appends with; the reaper is exempt from ownership checks, not from the
// principal seam.
//
// A run whose flow has no registered recovery runtime cannot be re-run
// headlessly; it finishes Failed with Uncertain and gohan.run.abandoned
// records the abandonment. The session stays consistent for the next Send.
func (s *Stack) Recover(ctx context.Context, limit int) error {
	if s.stores.Runs == nil {
		return errRecoverStoresRequired
	}
	if limit < 1 {
		limit = 1
	}
	if err := s.recoverPreempted(ctx, limit); err != nil {
		return err
	}
	stale, err := s.stores.Runs.Stale(ctx, recoverStaleAfter, limit)
	if err != nil {
		return fmt.Errorf("recover run: %w", err)
	}
	for _, run := range orderTreeRootsFirst(stale) {
		if err := s.recoverRun(ctx, run); err != nil {
			return err
		}
	}
	// The sweep runs even when Stale returned no rows: cleanup records
	// outlive a run's stale listing, and zero reclaims still leave
	// resources to close.
	if s.sandboxHandles != nil {
		if err := s.sandboxHandles.Sweep(ctx, limit); err != nil {
			return fmt.Errorf("sandbox sweep: %w", err)
		}
	}
	return s.recoverConsumed(ctx, limit)
}

// recoverConsumed re-drives checkpoints whose token was consumed while the
// run is still Suspended — the crash interval between a client decision and
// the token-fenced resume lease. Exactly one driver wins the Resuming
// transition; a
// Finished run is never re-executed. A checkpoint store without the lister
// leaves this window to the stale pass, which covers only runs already
// marked Resuming.
func (s *Stack) recoverConsumed(ctx context.Context, limit int) error {
	rl, ok := s.stores.Checkpoints.(stores.ResumeReadyLister)
	if !ok {
		return nil
	}
	ready, err := rl.ResumeReady(ctx, limit)
	if err != nil {
		return fmt.Errorf("recover run: %w", err)
	}
	for _, cp := range ready {
		// The lease decides, not the reason: a live client holds it and
		// wins the Resuming transition; a dead client left the run
		// suspended with a consumed token, which recovery completes.
		if err := s.recoverConsumedRun(ctx, cp); err != nil {
			return err
		}
	}
	return nil
}

func (s *Stack) recoverConsumedRun(ctx context.Context, cp stores.Checkpoint) error {
	run, ok, err := s.runByID(ctx, cp.RunID)
	if err != nil || !ok || run.State != stores.Suspended {
		return err
	}
	if s.shuttingDown() {
		return types.ErrShuttingDown
	}
	cr, ok := s.stores.Runs.(stores.CheckpointRuns)
	if !ok {
		// The token-fenced lease is unsupported: recovery skips the
		// automatic consumed path and the stale pass owns the run.
		return nil
	}
	tok, err := cr.SuspensionToken(ctx, cp.RunID)
	if err != nil {
		// The run no longer waits on a token: a client or a competitor
		// moved it.
		return nil
	}
	lease, err := cr.ResumingCheckpoint(ctx, cp.RunID, tok, stores.LeaseTTL)
	if err != nil {
		// A racing driver won the run, or a newer suspension replaced
		// this token: driven exactly once either way.
		return nil
	}
	rt, ok := s.recovery[run.Flow]
	if !ok {
		return s.abandonRun(ctx, lease, run)
	}
	run.State = stores.Resuming
	st, rcp, rin, err := s.replayState(ctx, run, rt)
	if err != nil {
		return s.abandonRun(ctx, lease, run)
	}
	rctx, p, err := s.recoveryContext(ctx, run, st, rcp)
	if err != nil {
		return s.abandonWith(ctx, lease, run, err)
	}
	return s.driveRecovered(rctx, lease, run, rt, st, rcp, rin, p)
}

// orderTreeRootsFirst lists depth-zero runs before their children, so a
// tree replays over its reclaimed root (identity.nested-spans-and-recovery).
func orderTreeRootsFirst(runs []stores.Run) []stores.Run {
	roots := make([]stores.Run, 0, len(runs))
	children := make([]stores.Run, 0, len(runs))
	for _, r := range runs {
		if r.Depth == 0 {
			roots = append(roots, r)
		} else {
			children = append(children, r)
		}
	}
	return append(roots, children...)
}

func (s *Stack) recoverRun(ctx context.Context, run stores.Run) error {
	if s.shuttingDown() {
		return types.ErrShuttingDown
	}
	lease, err := s.stores.Runs.Reclaim(ctx, run, stores.LeaseTTL)
	if err != nil {
		// Another reaper won the reclaim or the lease came back live:
		// both leave the run driven exactly once.
		return nil
	}
	rt, ok := s.recovery[run.Flow]
	if !ok {
		return s.abandonRun(ctx, lease, run)
	}
	st, cp, in, err := s.replayState(ctx, run, rt)
	if err != nil {
		return s.abandonRun(ctx, lease, run)
	}
	rctx, p, err := s.recoveryContext(ctx, run, st, cp)
	if err != nil {
		return s.abandonWith(ctx, lease, run, err)
	}
	return s.driveRecovered(rctx, lease, run, rt, st, cp, in, p)
}

// reconcileJournalUnknowns reconciles every Unknown journal entry the
// session still holds before a resumed or recovered drive replays its
// state: one Reconcile call per entry, with the arguments and result the
// journal reserved. A settled entry's result is written into the state's
// durable batch record; an entry Verify cannot settle keeps its key in
// the returned uncertainty set. A journal enumeration error or an
// unbound audit log is returned with the entry left Unknown; every other
// Reconcile error leaves the entry Unknown and only its key uncertain.
func reconcileJournalUnknowns(ctx context.Context, sessionID string, j stores.Journal, v types.Verifier, st *runtime.State) ([]types.CallKey, error) {
	entries, err := j.Unknown(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("enumerate unknown journal entries: %w", err)
	}
	_, uncertain, err := reconcileEntries(ctx, sessionID, v, entries, st)
	return uncertain, err
}

// resumeDecided reports whether the recorded resume input carries an
// actual decision for the receipts: a plain or empty continuation has no
// approver to receipt, whether it was consumed or not.
func resumeDecided(in stores.ResumeInput) bool {
	return in.Approver != nil || len(in.Args) > 0 || len(in.Data) > 0 || in.Reason != ""
}

// driveRecovered re-drives one recovered run through the shared lifecycle:
// the lifecycle holds the lease, owns the heartbeat, suspension
// persistence, terminal store transitions and failure cleanup. When the
// drive ends and the run is still Running or Resuming, nothing recorded a
// terminal transition — the run is closed as failed instead of silently
// left open.
func (s *Stack) driveRecovered(ctx context.Context, lease stores.Lease, run stores.Run, rt runtime.Runtime, st runtime.State, cp *stores.Checkpoint, in stores.ResumeInput, p types.Principal) error {
	pre := NewPreemptor()
	if err := s.beginRun(run.RunID, pre); err != nil {
		return err
	}
	defer s.endRun(run.RunID)
	if s.stores.Checkpoints == nil {
		// The re-drive can reach another suspension; without the
		// checkpoint store it cannot park the run again.
		return s.abandonWith(ctx, lease, run, fmt.Errorf("recover run %s: no checkpoint store to suspend through", run.RunID))
	}
	// An approval delivered between the token consume and the resume
	// lease never passed the resume path that records its receipts. Record
	// them before the gate is wired, or the re-drive asks again for a call
	// the approver already granted.
	if cp != nil && in.Verdict == stores.VerdictApprove && resumeDecided(in) {
		if err := s.recoveryReceipts(ctx, cp, in, run, &st); err != nil {
			return s.abandonWith(ctx, lease, run, err)
		}
	}
	var unresolved []types.CallKey
	if s.verifier != nil && s.stores.Journal != nil && run.SessionID != "" {
		var rerr error
		unresolved, rerr = reconcileJournalUnknowns(ctx, run.SessionID, s.stores.Journal, s.verifier, &st)
		if rerr != nil {
			return s.abandonWith(ctx, lease, run, fmt.Errorf("recover run %s: %w", run.RunID, rerr))
		}
	}
	opts := []LifecycleOption{
		WithLifecycleRuns(s.stores.Runs, lease),
		WithLifecycleResumeState(st),
		WithLifecycleSession(run.SessionID),
		WithLifecycleFlow(run.Flow),
		WithLifecycleTelemetry(s.telemetry),
		WithLifecycleSandboxHandles(s.sandboxHandles),
	}
	// The restore runs before any tool executes, so it cannot race a tool
	// call. A refused restore abandons the run instead of re-driving it
	// against a workspace state the checkpoint does not match.
	if err := s.restoreWorkspace(ctx, run.SessionID, cp); err != nil {
		return s.abandonWith(ctx, lease, run, err)
	}
	if p.Subject != "" {
		opts = append(opts, WithLifecycleOriginator(p))
	}
	if s.approvalPolicy != nil {
		opts = append(opts, WithLifecycleApprovalPolicy(s.approvalPolicy))
	}
	if s.verifier != nil && s.stores.Journal != nil {
		opts = append(opts, WithLifecycleVerify(s.verifier), WithLifecycleJournal(s.stores.Journal))
		if len(unresolved) > 0 {
			opts = append(opts, WithLifecycleUncertain(func(runtime.State) []types.CallKey {
				return append([]types.CallKey(nil), unresolved...)
			}))
		}
	}
	if app := s.recoveryAppender(run); app != nil {
		opts = append(opts, WithLifecycleAppender(app))
	}
	lc := NewLifecycle(opts...)
	// A registered native flow re-drives through the governed path built
	// from its resolved configuration: a fresh runtime, the batch gate
	// and reservation seam, and a ledger seeded from what the run record
	// reports as spent. The re-drive can suspend again through the same
	// effects a live run uses.
	ag := runtime.AgentRun{Save: s.stores.Checkpoints.Put}
	if len(st.Pending) > 0 {
		ctx = withPendingReplay(ctx)
	}
	var driveErr error
	if _, ok := s.resolvedForeign(run.Flow); ok {
		// A registered foreign flow re-drives through the same governed
		// wiring a live run got: the context, the runtime and the ledger
		// foreignAgentRun returns stay the one ledger the run charges,
		// and the returned runtime is the accounting decorator.
		var hist stores.History
		if s.stores.SessionLog != nil {
			if h, err := s.stores.SessionLog.Load(ctx, run.SessionID); err == nil {
				hist = h
			}
		}
		if st.Foreign != nil {
			ctx = withForeignProgress(ctx, st.Foreign)
		}
		fctx, frt, fag, fled, ferr := s.foreignAgentRun(ctx, run.SessionID, run.Flow, hist, run.Cost, nil)
		if ferr != nil {
			return s.abandonWith(ctx, lease, run, fmt.Errorf("recover run %s: %w", run.RunID, ferr))
		}
		ctx = fctx
		fag.Save = s.stores.Checkpoints.Put
		ag, rt = fag, frt
		opts = append(opts, WithLifecycleLedger(fled))
		lc = NewLifecycle(opts...)
	} else if cfg, ok := s.resolvedNative(run.Flow); ok {
		// A registered native flow re-drives through the governed path built
		// from its resolved configuration: a fresh runtime, the batch gate
		// and reservation seam, and a ledger seeded from what the run record
		// reports as spent. The re-drive can suspend again through the same
		// effects a live run uses.
		ledger := chains.NewLimitsStateSeeded(run.Cost, chains.WithCostTelemetry(s.telemetry))
		ctx = chains.WithLimitsState(ctx, ledger)
		var hist stores.History
		if s.stores.SessionLog != nil {
			if h, err := s.stores.SessionLog.Load(ctx, run.SessionID); err == nil {
				hist = h
			}
		}
		tc := s.nativeTurnConfig(cfg)
		tc.gate = nativeBatchGate(cfg, hist)
		restored := false
		tc.reserve = func(ctx context.Context, n int) (context.Context, func(), error) {
			if !restored {
				restored = true
				if u, ok := admittedToolTotalFrom(ctx); ok && u > 0 {
					if _, _, err := ledger.ReserveBatch(ctx, types.RunLimits{}, u); err != nil {
						return ctx, func() {}, err
					}
				}
			}
			if n == 0 {
				return ctx, func() {}, nil
			}
			return ledger.ReserveBatch(ctx, cfg.limits, n)
		}
		native := nativeRun(tc, hist.Messages, nil)
		native.Model = cfg.model
		native.Tools = cfg.tools
		native.Assemble = tc.assemble
		native.History = hist
		native.Save = s.stores.Checkpoints.Put
		ag = native
		rt = runtime.NewNative()
		opts = append(opts, WithLifecycleLedger(ledger))
		lc = NewLifecycle(opts...)
	}
	var uncertain []types.CallKey
	rt = pre.Runtime(rt)
	if _, ok := types.RunInfoFrom(ctx); !ok {
		ctx = types.WithRunInfo(ctx, types.RunInfo{SessionID: run.SessionID, RunID: run.RunID, Flow: run.Flow})
	}
	// Completion writes ignore the reaper's cancellation but stay bounded:
	// a cancelled reaper still leaves the recovered tail and its terminal
	// boundary durable for the next attaching client.
	completion, cancelCompletion := context.WithTimeout(context.WithoutCancel(ctx), recoveredEventBound)
	defer cancelCompletion()
	tail, terr := eventLogTail(completion, s.stores.EventLog, run.RunID)
	if terr != nil {
		return s.abandonWith(ctx, lease, run, fmt.Errorf("recover run %s: %w", run.RunID, terr))
	}
	var gbBoundary bool
	for ev, err := range DriveLifecycle(ctx, lc, rt, ag) {
		if err != nil {
			var gb *types.GuardBlockedError
			if errors.As(err, &gb) {
				// A guard block ends the run at its own boundary, the
				// way the live relay records it.
				if gerr := s.recordRecovered(completion, run, types.GuardBlocked{Stage: gb.Stage, Reason: gb.Reason}); gerr == nil {
					if serr := s.recordRecovered(completion, run, types.Done{Reason: types.StopGuardBlocked}); serr == nil {
						gbBoundary = true
					}
				}
			}
			driveErr = fmt.Errorf("recover run %s: %w", run.RunID, err)
			break
		}
		if rerr := s.recordRecovered(completion, run, ev); rerr != nil {
			driveErr = fmt.Errorf("recover run %s: %w", run.RunID, rerr)
			break
		}
		tail++
		if d, ok := ev.(types.Done); ok && len(d.Uncertain) > 0 {
			uncertain = d.Uncertain
		}
	}
	if driveErr != nil && !gbBoundary {
		s.recordRecoveredTerminal(completion, run, tail, driveErr)
	}
	state, terminal := s.terminalRun(ctx, run.RunID)
	if !terminal {
		return s.abandonRun(ctx, lc.lease, run)
	}
	if driveErr == nil && len(uncertain) > 0 {
		return &types.UncertainOutcomeError{Uncertain: uncertain}
	}
	if state == stores.Finished {
		countRecovered(ctx, s.telemetry, run)
	}
	return driveErr
}

// recoveredEventBound bounds the completion writes of a recovered drive.
const recoveredEventBound = 30 * time.Second

// recordRecovered appends one recovered event to the run's log with live
// execution's redacted record-before-delivery semantics.
func (s *Stack) recordRecovered(ctx context.Context, run stores.Run, ev types.Event) error {
	if s.stores.EventLog == nil {
		return nil
	}
	red, pol := s.flowRedaction(run.Flow)
	stored, err := redactEventWith(ctx, ev, red, pol, s.stores.RedactionMaps)
	if err != nil {
		return fmt.Errorf("redact event: %w", err)
	}
	if err := s.stores.EventLog.Append(ctx, run.RunID, stores.Event{Payload: stored, Meta: types.EventMeta{RunID: run.RunID}}); err != nil {
		return fmt.Errorf("record event: %w", err)
	}
	return nil
}

// recordRecoveredTerminal appends the terminal failure record a failed
// recovered drive leaves behind; the run keeps the store's own terminal
// transition.
func (s *Stack) recordRecoveredTerminal(ctx context.Context, run stores.Run, tail int64, cause error) {
	te := types.TerminalFailure(cause, run.RunID, tail+2)
	_ = s.recordRecovered(ctx, run, te)
}

// eventLogTail reports the highest sequence the run's log holds.
func eventLogTail(ctx context.Context, log stores.EventLog, runID string) (int64, error) {
	if log == nil {
		return 0, nil
	}
	var tail int64
	for e, err := range log.Read(ctx, runID, 0) {
		if err != nil {
			return 0, fmt.Errorf("read event log tail: %w", err)
		}
		if e.Meta.Seq > tail {
			tail = e.Meta.Seq
		}
	}
	return tail, nil
}

// flowRedaction returns the redaction seam the flow's resolved
// configuration carries; flows without one skip redaction.
func (s *Stack) flowRedaction(flow string) (types.Redactor, types.RedactionPolicy) {
	if cfg, ok := s.resolvedNative(flow); ok {
		return cfg.redactor, cfg.redactionPolicy
	}
	return nil, types.RedactionPolicy{}
}

// terminalRun reports the run's stored state when the store can answer it.
// A store without a by-id reader cannot verify the transition; the
// lifecycle's own ordering is trusted.
func (s *Stack) terminalRun(ctx context.Context, runID string) (stores.RunState, bool) {
	run, ok, err := s.runByID(ctx, runID)
	if err != nil || !ok {
		return 0, true
	}
	switch run.State {
	case stores.Finished, stores.Failed, stores.Suspended:
		return run.State, true
	}
	return run.State, false
}

func (s *Stack) runByID(ctx context.Context, runID string) (stores.Run, bool, error) {
	rf, ok := s.stores.Runs.(runFinder)
	if !ok {
		return stores.Run{}, false, nil
	}
	run, err := rf.ByID(ctx, runID)
	if err != nil {
		return stores.Run{}, false, nil
	}
	return run, true, nil
}

type runFinder interface {
	ByID(ctx context.Context, runID string) (stores.Run, error)
}

// recoveryReceipts records the receipts an approval delivered straight to
// the checkpoint store earned: the resume Send records them after the
// consume, and a crash in that interval leaves the gate with no grant to
// consult. A HumanApproval envelope carries the approval-controlled calls
// with their recorded votes; a batch ask carries them as the state's
// pending calls, and the delivered approver is the vote. Recovery carries
// no scope context, so every receipt it records is explicitly unscoped:
// the resume Send owns the scoped receipt, and a scoped approval's
// authority lives in the grant store rather than in the receipt.
func (s *Stack) recoveryReceipts(ctx context.Context, cp *stores.Checkpoint, in stores.ResumeInput, run stores.Run, st *runtime.State) error {
	if s.stores.SessionLog == nil || len(cp.Data) == 0 {
		return nil
	}
	env, _, err := decodeCheckpoint(*cp, runtime.NewNative(), run.Flow)
	if err != nil {
		return fmt.Errorf("recover run %s: %w", run.RunID, err)
	}
	var rcpts []approvalReceipt
	switch {
	case len(env.Approvals) > 0:
		if in.Approver != nil {
			for i := range env.Approvals {
				if !approverRecorded(env.Approvals[i], *in.Approver) {
					env.Approvals[i].ApprovedBy = append(slices.Clone(env.Approvals[i].ApprovedBy), *in.Approver)
				}
			}
		}
		for _, ap := range env.Approvals {
			if len(ap.ApprovedBy) == 0 {
				continue
			}
			rcpts = append(rcpts, approvalReceipt{
				RunID:      run.RunID,
				Generation: env.Generation,
				CallID:     ap.Call.ID,
				Tool:       ap.Call.Name,
				Args:       slices.Clone(ap.Call.Args),
				Approvers:  slices.Clone(ap.ApprovedBy),
				Scoped:     false,
			})
		}
	case cp.Reason == types.AwaitingBatch:
		var apps []types.Principal
		if in.Approver != nil {
			apps = []types.Principal{*in.Approver}
		}
		for _, call := range env.State.Pending {
			rcpts = append(rcpts, approvalReceipt{
				RunID:      run.RunID,
				Generation: env.Generation,
				CallID:     call.ID,
				Tool:       call.Name,
				Args:       slices.Clone(call.Args),
				Approvers:  slices.Clone(apps),
				Scoped:     false,
			})
		}
	default:
		return nil
	}
	if len(rcpts) == 0 {
		return nil
	}
	h, err := s.stores.SessionLog.Load(ctx, cp.SessionID)
	if err != nil {
		return fmt.Errorf("recover run %s: %w", run.RunID, err)
	}
	ver, err := appendReceiptMessages(ctx, s.stores.SessionLog, cp.SessionID, h, rcpts)
	if err != nil {
		return fmt.Errorf("recover run %s: %w", run.RunID, err)
	}
	if ver != h.Version {
		st.HistoryVersion = ver
	}
	return nil
}

func (s *Stack) recoveryAppender(run stores.Run) HistoryAppender {
	if s.stores.SessionLog == nil {
		return nil
	}
	return AppendFunc(func(ctx context.Context, expected int64, msgs ...types.Message) (int64, error) {
		return s.stores.SessionLog.Append(ctx, run.SessionID, expected, msgs...)
	})
}

// replayState reconstructs the state a crash left behind: a Resuming run
// continues from its checkpoint with the pending resume input applied, a
// Running one re-enters the step whose pending calls the session log
// already carries.
func (s *Stack) replayState(ctx context.Context, run stores.Run, rt runtime.Runtime) (runtime.State, *stores.Checkpoint, stores.ResumeInput, error) {
	var st runtime.State
	if run.State == stores.Resuming {
		cp, in, err := s.stores.Checkpoints.PendingInput(ctx, run.RunID)
		if err != nil {
			return st, nil, in, fmt.Errorf("recover run: %w", err)
		}
		if len(cp.Data) > 0 {
			_, decoded, derr := decodeCheckpoint(cp, rt, run.Flow)
			if derr != nil {
				return st, nil, in, fmt.Errorf("recover run: %w", derr)
			}
			st = decoded
		}
		// The run continues as the originator before any history append
		// touches the session.
		ctx = WithPrincipal(ctx, cp.Originator)
		// A Resuming run consumed its token before the lease, so the
		// recorded input replays even when it is an empty Continue(),
		// which applies nothing.
		if aerr := applyResume(ctx, s.stores.SessionLog, cp.SessionID, &st, in); aerr != nil {
			return st, nil, in, fmt.Errorf("recover run: %w", aerr)
		}
		// The consume that stranded the run is the decision the resume
		// lease never delivered: without the ready mark the replayed ask
		// suspends again instead of executing the call the approval paid
		// for.
		if cp.Reason == types.HumanApproval && in.Verdict == stores.VerdictApprove {
			if rerr := markAskReady(&st); rerr != nil {
				return st, nil, in, fmt.Errorf("recover run: %w", rerr)
			}
		}
		return st, &cp, in, nil
	}
	st = runtime.State{
		Turn: run.Turn,
		// The re-drive appends where the crash left the log: the pending
		// calls' results land after whatever the session already carries.
		HistoryVersion: s.sessionVersion(ctx, run.SessionID),
	}
	if _, ok := s.resolvedForeign(run.Flow); ok {
		// A foreign flow keeps its backend inside the runtime, not in
		// native bytes: Backend stays empty and the recovered drive
		// rebuilds admission accounting from the run's admission records.
		st.Pending = run.Pending
		return st, nil, stores.ResumeInput{}, nil
	}
	if _, ok := s.resolvedNative(run.Flow); ok {
		if err := s.reconstructNativeRunning(ctx, run, &st); err != nil {
			return st, nil, stores.ResumeInput{}, fmt.Errorf("recover run: %w", err)
		}
	} else {
		st.Pending = run.Pending
	}
	return st, nil, stores.ResumeInput{}, nil
}

// reconstructNativeRunning rebuilds the phase a crash left behind from the
// durable history instead of handing the drive an empty backend: the latest
// assistant turn names the batch, the recorded results name the calls that
// settled, and the unresolved remainder re-enters the batch phase as
// pending calls. The driver record carries the admitted total so the
// reservation seam restores it once; a turn with nothing unresolved
// re-enters the model phase.
func (s *Stack) reconstructNativeRunning(ctx context.Context, run stores.Run, st *runtime.State) error {
	if s.stores.SessionLog == nil {
		return nil
	}
	h, err := s.stores.SessionLog.Load(ctx, run.SessionID)
	if err != nil {
		// No readable history: the drive falls back to the record's own
		// state, and stored-authority resolution decides below whether
		// the re-drive may run at all.
		return nil
	}
	var calls []types.ToolUse
	for i := len(h.Messages) - 1; i >= 0 && len(calls) == 0; i-- {
		msg := h.Messages[i]
		if msg.Role != types.RoleAssistant {
			continue
		}
		for _, b := range msg.Blocks {
			if tu, ok := b.(types.ToolUse); ok {
				calls = append(calls, tu)
			}
		}
	}
	if len(calls) == 0 {
		enc, encErr := encodeNativeBackend(nativeBackend{Phase: "model"})
		if encErr != nil {
			return encErr
		}
		st.Backend = enc
		return nil
	}
	settled := map[string]bool{}
	for _, msg := range h.Messages {
		for _, b := range msg.Blocks {
			if tr, ok := b.(types.ToolResult); ok && tr.ID != "" {
				settled[tr.ID] = true
			}
		}
	}
	// The admitted total spans every batch the history carries, so a
	// re-entered drive restores all earlier admissions instead of only
	// the batch it re-enters with.
	admitted := 0
	seen := map[string]bool{}
	for _, msg := range h.Messages {
		if msg.Role != types.RoleAssistant {
			continue
		}
		for _, b := range msg.Blocks {
			if tu, ok := b.(types.ToolUse); ok && !seen[tu.ID] {
				seen[tu.ID] = true
				admitted++
			}
		}
	}
	var pending []types.ToolUse
	for _, c := range calls {
		if !settled[c.ID] {
			pending = append(pending, c)
		}
	}
	st.Pending = pending
	phase := "batch"
	if len(pending) == 0 {
		phase = "model"
	}
	enc, encErr := encodeNativeBackend(nativeBackend{
		Phase:  phase,
		Driver: &nativeProgress{Version: nativeProgressVersion, ToolCalls: admitted},
	})
	if encErr != nil {
		return encErr
	}
	st.Backend = enc
	return nil
}

// sessionVersion reports the history version a replay appends at; a
// session with no history yet starts at zero.
func (s *Stack) sessionVersion(ctx context.Context, sessionID string) int64 {
	if s.stores.SessionLog == nil {
		return 0
	}
	h, err := s.stores.SessionLog.Load(ctx, sessionID)
	if err != nil {
		return 0
	}
	return h.Version
}

// recoveryContext re-issues the run's identity from stored state: the
// checkpoint's originator, or the session owner when no checkpoint
// survived. The reaper's ambient principal never becomes the driver, and
// the credential resolution happens before any tool executes; its failure
// stops the recovery.
func (s *Stack) recoveryContext(ctx context.Context, run stores.Run, st runtime.State, cp *stores.Checkpoint) (context.Context, types.Principal, error) {
	var p types.Principal
	resolved := false
	if cp != nil {
		p = cp.Originator
		resolved = true
	} else if s.stores.SessionLog != nil {
		// A session row the log cannot produce leaves no stored owner;
		// the reaper's ambient principal must not fill the gap.
		if h, err := s.stores.SessionLog.Load(ctx, run.SessionID); err == nil {
			p = types.Principal{Tenant: h.Owner.Tenant, Subject: h.Owner.Subject}
			resolved = true
		}
	}
	if !resolved {
		// No stored authority to drive with: the reaper's ambient
		// principal and credentials must never fill the gap.
		return ctx, p, fmt.Errorf("recover run %s: stored authority unresolved: %w", run.RunID, types.ErrSessionForbidden)
	}
	ctx = types.WithPrincipal(ctx, p)
	ctx = types.WithRunInfo(ctx, types.RunInfo{
		Flow:        run.Flow,
		SessionID:   run.SessionID,
		RunID:       run.RunID,
		RootRunID:   run.RootRunID,
		ParentRunID: run.ParentRunID,
		Turn:        st.Turn,
		Depth:       run.Depth,
		Mode:        run.Mode,
		Principal:   p,
	})
	if s.credentials != nil {
		cred, err := s.credentials.Credentials(ctx, p)
		if err != nil {
			return ctx, p, fmt.Errorf("recover run %s: resolve credentials: %w", run.RunID, err)
		}
		ctx = WithCredential(ctx, cred)
	}
	return ctx, p, nil
}

// abandonWith closes the run as Failed and reports the reason recovery
// stopped driving it.
func (s *Stack) abandonWith(ctx context.Context, lease stores.Lease, run stores.Run, cause error) error {
	if err := s.abandonRun(ctx, lease, run); err != nil {
		return err
	}
	return cause
}

// abandonRun closes a run that cannot be re-driven headlessly as Failed
// with Uncertain, leaving the session consistent for the next Send.
// gohan.run.abandoned records the abandonment.
func (s *Stack) abandonRun(ctx context.Context, lease stores.Lease, run stores.Run) error {
	countAbandoned(ctx, s.telemetry, run)
	if err := s.stores.Runs.Finish(ctx, lease, stores.Failed, run.Uncertain, ""); err != nil {
		return fmt.Errorf("recover run: %w", err)
	}
	return nil
}
