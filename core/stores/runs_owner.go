package stores

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// WithWorkspaceOwner validates the session's owner proof and runs write
// while the store lock stays held, so the check and the write are one
// atomic step. The proof requires the run the session currently drives,
// the run's own session id, a nonzero matching lease generation and a live
// unexpired lease; any failed check reports types.ErrRunNotActive. The
// callback MUST NOT take either store mutex, call another store or reach a
// provider: it only assigns fields.
func (s *MemoryRuns) WithWorkspaceOwner(ctx context.Context, sessionID string, owner types.WorkspaceOwner, write func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateOwnerLocked(sessionID, owner); err != nil {
		return err
	}
	return write()
}

// validateOwnerLocked checks the four owner-proof conditions against the
// run records. The store mutex must be held.
func (s *MemoryRuns) validateOwnerLocked(sessionID string, owner types.WorkspaceOwner) error {
	notActive := func(runID string) error {
		return fmt.Errorf("%w: run %s", types.ErrRunNotActive, runID)
	}
	runID, ok := s.onSess[sessionID]
	if !ok || runID != owner.RunID {
		return notActive(owner.RunID)
	}
	rec, ok := s.runs[owner.RunID]
	if !ok || rec.run.SessionID != sessionID {
		return notActive(owner.RunID)
	}
	if owner.LeaseGeneration == 0 || rec.lease.Generation != owner.LeaseGeneration {
		return notActive(owner.RunID)
	}
	if !rec.live || rec.expiredAt(s.now()) {
		return notActive(owner.RunID)
	}
	return nil
}
