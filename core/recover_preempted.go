package gohan

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// recoverPreempted resumes runs parked Preempted at a persisted safe point
// before the stale pass runs: a preempted run needs no staleness wait. The
// token-fenced surface decides both halves: the checkpoint's own reason
// picks what recovery may consume, and the lease binds to the exact token
// the run is suspended on.
func (s *Stack) recoverPreempted(ctx context.Context, limit int) error {
	pl, ok := s.stores.Runs.(stores.PreemptedLister)
	if !ok {
		return nil
	}
	parked, err := pl.Preempted(ctx, limit)
	if err != nil {
		return fmt.Errorf("recover run: %w", err)
	}
	for _, run := range orderTreeRootsFirst(parked) {
		if err := s.recoverPreemptedRun(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

func (s *Stack) recoverPreemptedRun(ctx context.Context, run stores.Run) error {
	cp, in, err := s.stores.Checkpoints.PendingInput(ctx, run.RunID)
	if err != nil {
		// No checkpoint to re-drive.
		return nil
	}
	cr, ok := s.stores.Runs.(stores.CheckpointRuns)
	if !ok {
		// The token-fenced lease is unsupported: recovery skips the
		// automatic preempted path and leaves the suspension to the
		// client.
		return nil
	}
	tok, err := cr.SuspensionToken(ctx, run.RunID)
	if err != nil {
		// The run no longer waits on a token: a client or a competitor
		// moved it.
		return nil
	}
	if cp.Reason != types.Preempted {
		// Recovery never decides a suspension it did not cause: a human
		// approval or any other wait keeps its token for its own resume
		// path.
		return nil
	}
	resumer, ok := s.stores.Checkpoints.(stores.CheckpointResumer)
	if !ok {
		return nil
	}
	if _, cerr := resumer.ConsumeIf(ctx, tok, cp, stores.ResumeInput{}); cerr != nil {
		// The decision is already recorded or the token expired: the
		// consumed-token pass completes it exactly once.
		return nil
	}
	lease, err := cr.ResumingCheckpoint(ctx, run.RunID, tok, stores.LeaseTTL)
	if err != nil {
		// A racing reaper or client flipped the run, or a newer
		// suspension replaced this token: the recorded consumption stays
		// recoverable, and the newer suspension stays untouched.
		return nil
	}
	rt, ok := s.recovery[run.Flow]
	if !ok {
		return s.abandonRun(ctx, lease, run)
	}
	run.State = stores.Resuming
	st, _, _, err := s.replayState(ctx, run, rt)
	if err != nil {
		return s.abandonRun(ctx, lease, run)
	}
	rctx, p, err := s.recoveryContext(ctx, run, st, &cp)
	if err != nil {
		return s.abandonWith(ctx, lease, run, err)
	}
	return s.driveRecovered(rctx, lease, run, rt, st, &cp, in, p)
}
