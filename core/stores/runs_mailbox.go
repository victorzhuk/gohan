package stores

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// Signal appends one mailbox entry for a run whose state is Running. The
// store clock stamps At, so the persisted instant is authoritative. A
// non-running run fails with types.ErrRunNotActive; a mailbox already at
// MaxPendingSignals fails with types.ErrMailboxFull.
func (s *MemoryRuns) Signal(ctx context.Context, runID string, sig Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[runID]
	if !ok {
		return fmt.Errorf("%w: run %s", ErrRunNotFound, runID)
	}
	if rec.run.State != Running {
		return fmt.Errorf("%w: run %s is %v", types.ErrRunNotActive, runID, rec.run.State)
	}
	if len(rec.signals) >= MaxPendingSignals {
		return fmt.Errorf("%w: run %s", types.ErrMailboxFull, runID)
	}
	sig.At = s.now()
	rec.signals = append(rec.signals, sig)
	return nil
}

// Drain returns and removes the run's pending signals in arrival order.
// Only the current lease holder may drain; an expired or foreign lease
// fails with types.ErrRunNotActive and the signals stay pending.
func (s *MemoryRuns) Drain(ctx context.Context, l Lease) ([]Signal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.heldLocked(l)
	if err != nil {
		return nil, err
	}
	pending := rec.signals
	rec.signals = nil
	return pending, nil
}
