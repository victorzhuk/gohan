package gohan

import (
	"context"
	"fmt"
	"slices"

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
	return s.recoverConsumed(ctx, limit)
}

// recoverConsumed re-drives checkpoints whose token was consumed while the
// run is still Suspended — the crash interval between a client decision and
// Runs.Resuming. Exactly one driver wins the Resuming transition; a
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
	rt, ok := s.recovery[run.Flow]
	if !ok {
		lease, lerr := s.stores.Runs.Resuming(ctx, run.RunID, stores.LeaseTTL)
		if lerr != nil {
			return nil
		}
		return s.abandonRun(ctx, lease, run)
	}
	lease, err := s.stores.Runs.Resuming(ctx, run.RunID, stores.LeaseTTL)
	if err != nil {
		// A racing driver won the run or it finished: driven exactly once.
		return nil
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

// driveRecovered re-drives one recovered run through the shared lifecycle:
// the lifecycle holds the lease, owns the heartbeat, suspension
// persistence, terminal store transitions and failure cleanup. When the
// drive ends and the run is still Running or Resuming, nothing recorded a
// terminal transition — the run is closed as failed instead of silently
// left open.
func (s *Stack) driveRecovered(ctx context.Context, lease stores.Lease, run stores.Run, rt runtime.Runtime, st runtime.State, cp *stores.Checkpoint, in stores.ResumeInput, p types.Principal) error {
	if s.stores.Checkpoints == nil {
		// The re-drive can reach another suspension; without the
		// checkpoint store it cannot park the run again.
		return s.abandonWith(ctx, lease, run, fmt.Errorf("recover run %s: no checkpoint store to suspend through", run.RunID))
	}
	// An approval delivered between the token consume and Runs.Resuming
	// never passed the resume path that records its receipts. Record them
	// before the gate is wired, or the re-drive asks again for a call the
	// approver already granted.
	if cp != nil && in.Verdict == stores.VerdictApprove && resumeConsumed(in) {
		if err := s.recoveryReceipts(ctx, cp, in, run, &st); err != nil {
			return s.abandonWith(ctx, lease, run, err)
		}
	}
	opts := []LifecycleOption{
		WithLifecycleRuns(s.stores.Runs, lease),
		WithLifecycleResumeState(st),
		WithLifecycleSession(run.SessionID),
		WithLifecycleFlow(run.Flow),
		WithLifecycleTelemetry(s.telemetry),
	}
	if p.Subject != "" {
		opts = append(opts, WithLifecycleOriginator(p))
	}
	if s.approvalPolicy != nil {
		opts = append(opts, WithLifecycleApprovalPolicy(s.approvalPolicy))
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
	if cfg, ok := s.resolvedNative(run.Flow); ok {
		ledger := chains.NewLimitsStateSeeded(run.Cost)
		ctx = chains.WithLimitsState(ctx, ledger)
		var hist stores.History
		if s.stores.SessionLog != nil {
			if h, err := s.stores.SessionLog.Load(ctx, run.SessionID); err == nil {
				hist = h
			}
		}
		tc := s.nativeTurnConfig(cfg)
		tc.gate = nativeBatchGate(cfg, hist)
		tc.reserve = func(ctx context.Context, n int) (context.Context, func(), error) {
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
	for _, err := range DriveLifecycle(ctx, lc, rt, ag) {
		if err != nil {
			break
		}
	}
	state, terminal := s.terminalRun(ctx, run.RunID)
	if !terminal {
		return s.abandonRun(ctx, lc.lease, run)
	}
	if state == stores.Finished {
		countRecovered(ctx, s.telemetry, run)
	}
	return nil
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
// pending calls, and the delivered approver is the vote.
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
	var msgs []types.Message
	seen := map[string]bool{}
	for _, msg := range h.Messages {
		if raw, ok := msg.Meta[ApprovalReceiptKey]; ok {
			rc, derr := decodeReceipt(raw)
			if derr != nil {
				return derr
			}
			seen[receiptDedupKey(rc)] = true
		}
	}
	for _, rc := range rcpts {
		if seen[receiptDedupKey(rc)] {
			continue
		}
		msg, merr := approvalReceiptMessage(rc)
		if merr != nil {
			return merr
		}
		msgs = append(msgs, msg)
	}
	if len(msgs) == 0 {
		return nil
	}
	ver, err := s.stores.SessionLog.Append(ctx, cp.SessionID, h.Version, msgs...)
	if err != nil {
		return fmt.Errorf("recover run %s: %w", run.RunID, err)
	}
	st.HistoryVersion = ver
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
		if resumeConsumed(in) {
			if aerr := applyResume(ctx, s.stores.SessionLog, cp.SessionID, &st, in); aerr != nil {
				return st, nil, in, fmt.Errorf("recover run: %w", aerr)
			}
		}
		return st, &cp, in, nil
	}
	return runtime.State{
		Turn:    run.Turn,
		Pending: run.Pending,
		// The re-drive appends where the crash left the log: the pending
		// calls' results land after whatever the session already carries.
		HistoryVersion: s.sessionVersion(ctx, run.SessionID),
	}, nil, stores.ResumeInput{}, nil
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
		ctx = types.WithRunInfo(ctx, types.RunInfo{
			Flow: run.Flow, SessionID: run.SessionID, RunID: run.RunID,
			RootRunID: run.RootRunID, ParentRunID: run.ParentRunID,
			Turn: st.Turn, Depth: run.Depth, Mode: run.Mode,
		})
		return ctx, p, nil
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
