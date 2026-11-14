package stores

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// RunCostRecorder is the optional Runs extension that records the spend a
// run charged before it finishes. A store without it keeps the row's Cost
// at zero; the tree total stays authoritative.
type RunCostRecorder interface {
	RecordCost(ctx context.Context, l Lease, cost float64) error
}

// RecordCost adds cost to the run's recorded spend. The lease stays live,
// so the run finishes through Finish as usual.
func (s *MemoryRuns) RecordCost(ctx context.Context, l Lease, cost float64) error {
	if cost <= 0 {
		return fmt.Errorf("record cost: cost %v: %w", cost, types.ErrInputInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.heldLocked(l)
	if err != nil {
		return err
	}
	rec.run.Cost += cost
	return s.encodeRunLocked(rec)
}
