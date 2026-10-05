package gohan

import (
	"context"
	"encoding/json"
	"fmt"

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
	return nil
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
	st, cp, in, err := s.replayState(ctx, run)
	if err != nil {
		return s.abandonRun(ctx, lease, run)
	}
	rctx := s.recoveryContext(ctx, run, st, cp)
	for _, err := range DriveResume(rctx, rt, runtime.AgentRun{}, st, in) {
		if err != nil {
			return s.abandonRun(ctx, lease, run)
		}
	}
	if err := s.stores.Runs.Finish(ctx, lease, stores.Finished, run.Uncertain, ""); err != nil {
		return fmt.Errorf("recover run: %w", err)
	}
	countRecovered(ctx, s.telemetry, run)
	return nil
}

// replayState reconstructs the state a crash left behind: a Resuming run
// continues from its checkpoint with the pending resume input applied, a
// Running one re-enters the step whose pending calls the session log
// already carries.
func (s *Stack) replayState(ctx context.Context, run stores.Run) (runtime.State, *stores.Checkpoint, stores.ResumeInput, error) {
	var st runtime.State
	if run.State == stores.Resuming {
		cp, in, err := s.stores.Checkpoints.PendingInput(ctx, run.RunID)
		if err != nil {
			return st, nil, in, fmt.Errorf("recover run: %w", err)
		}
		if len(cp.Data) > 0 {
			if uerr := json.Unmarshal(cp.Data, &st); uerr != nil {
				return st, nil, in, fmt.Errorf("recover run: decode checkpoint: %w", uerr)
			}
		}
		// The run continues as the originator before any history append
		// touches the session.
		ctx = WithPrincipal(ctx, cp.Originator)
		if aerr := applyResume(ctx, s.stores.SessionLog, cp.SessionID, &st, in); aerr != nil {
			return st, nil, in, fmt.Errorf("recover run: %w", aerr)
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

// recoveryContext re-issues the run's identity: the Resuming originator
// comes from the checkpoint and travels through the credential source
// before any tool executes (identity.credentials-on-recovery).
func (s *Stack) recoveryContext(ctx context.Context, run stores.Run, st runtime.State, cp *stores.Checkpoint) context.Context {
	info := types.RunInfo{
		Flow:        run.Flow,
		SessionID:   run.SessionID,
		RunID:       run.RunID,
		RootRunID:   run.RootRunID,
		ParentRunID: run.ParentRunID,
		Turn:        st.Turn,
		Depth:       run.Depth,
		Mode:        run.Mode,
	}
	ctx = types.WithRunInfo(ctx, info)
	if cp != nil {
		ctx = WithPrincipal(ctx, cp.Originator)
		if s.credentials != nil {
			if cred, cerr := s.credentials.Credentials(ctx, cp.Originator); cerr == nil {
				ctx = WithCredential(ctx, cred)
			}
		}
	}
	return ctx
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
