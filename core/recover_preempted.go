package gohan

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/stores"
)

// recoverPreempted resumes runs parked Preempted at a persisted safe point
// before the stale pass runs: a preempted run needs no staleness wait. The
// Checkpoints port mints tokens privately, so recovery takes the run to
// Resuming through the runs store; the checkpoint token stays unconsumed,
// and the client keeps the choice to resume it too.
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
	if err != nil || resumeConsumed(in) {
		// No checkpoint to re-drive, or the client consumed the token
		// and owns the resume.
		return nil
	}
	lease, err := s.stores.Runs.Resuming(ctx, run.RunID, stores.LeaseTTL)
	if err != nil {
		// A racing reaper or a client flipped the run first.
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
	rctx := s.recoveryContext(ctx, run, st, &cp)
	return s.driveRecovered(rctx, lease, run, rt, st)
}

// resumeConsumed reports whether a client decision is already recorded on
// the checkpoint. Continue() carries the zero input, so a preempted run
// nobody resumed reads as unconsumed.
func resumeConsumed(in stores.ResumeInput) bool {
	return in.Approver != nil || in.Verdict != 0 || len(in.Args) > 0 || len(in.Data) > 0 || in.Reason != ""
}
