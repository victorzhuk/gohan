package stores

import (
	"context"
	"fmt"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// Heartbeat refreshes the run's lease by store time. A run whose lease has
// already expired rejects the refresh; the owner must re-acquire.
func (s *MemoryRuns) Heartbeat(ctx context.Context, l Lease) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.heldLocked(l)
	if err != nil {
		return Lease{}, err
	}
	now := s.now()

	rec.run.Heartbeat = now
	rec.lease = Lease{RunID: rec.run.RunID, Generation: l.Generation, Expires: now.Add(rec.ttl)}
	if err := s.encodeRunLocked(rec); err != nil {
		return Lease{}, err
	}
	return rec.lease, nil
}

// Reclaim takes a fresh lease on a run that Stale listed: stored Running
// or Resuming state whose lease expired. A terminal or suspended row
// refuses with ErrRunNotActive, and a live competitor in the session
// refuses with ErrRunActive without touching either run. Under the store
// lock the recheck is atomic, so of many concurrent reapers exactly one
// wins and the rest see a heartbeat that is no longer stale.
func (s *MemoryRuns) Reclaim(ctx context.Context, r Run, ttl time.Duration) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[r.RunID]
	if !ok {
		return Lease{}, fmt.Errorf("%w: run %s", ErrRunNotFound, r.RunID)
	}
	now := s.now()

	if (rec.run.State != Running && rec.run.State != Resuming) || !rec.expiredAt(now) {
		return Lease{}, fmt.Errorf("%w: run %s is %v", types.ErrRunNotActive, r.RunID, rec.run.State)
	}
	if owner, live := s.liveSessionOwnerLocked(rec.run.SessionID, now); live {
		return Lease{}, fmt.Errorf("%w: run %s", types.ErrRunActive, owner)
	}

	generation, err := s.nextGenerationLocked()
	if err != nil {
		return Lease{}, err
	}
	rec.ttl = ttl
	rec.lease = Lease{RunID: r.RunID, Generation: generation, Expires: now.Add(ttl)}
	rec.run.Heartbeat = now
	rec.live = true
	s.onSess[rec.run.SessionID] = rec.run.RunID
	if err := s.encodeRunLocked(rec); err != nil {
		return Lease{}, err
	}
	return rec.lease, nil
}

// resumeLeaseLocked flips a suspended record to Resuming and mints its
// lease generation. The caller already checked state, token and session
// ownership.
func (s *MemoryRuns) resumeLeaseLocked(rec *runRecord, runID string, now time.Time, ttl time.Duration) error {
	generation, err := s.nextGenerationLocked()
	if err != nil {
		return err
	}
	rec.run.State = Resuming
	rec.run.Heartbeat = now
	rec.ttl = ttl
	rec.lease = Lease{RunID: runID, Generation: generation, Expires: now.Add(ttl)}
	rec.live = true
	s.onSess[rec.run.SessionID] = runID
	return s.encodeRunLocked(rec)
}

var _ CheckpointRuns = (*MemoryRuns)(nil)

// SuspensionToken reports the token the run is currently suspended on; a
// run parked nowhere reports ErrTokenMismatch.
func (s *MemoryRuns) SuspensionToken(_ context.Context, runID string) (types.ResumeToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[runID]
	if !ok {
		return "", fmt.Errorf("%w: run %s", ErrRunNotFound, runID)
	}
	if rec.run.State != Suspended {
		return "", fmt.Errorf("%w: run %s is %v", types.ErrTokenMismatch, runID, rec.run.State)
	}
	return rec.token, nil
}

// ResumingCheckpoint mints a resume lease only for the token the run is
// currently suspended on, with exclusive session ownership. A mismatched
// token or a live competitor refuses without touching the newer suspension
// or either run.
func (s *MemoryRuns) ResumingCheckpoint(_ context.Context, runID string, t types.ResumeToken, ttl time.Duration) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[runID]
	if !ok {
		return Lease{}, fmt.Errorf("%w: run %s", ErrRunNotFound, runID)
	}
	if rec.run.State != Suspended {
		return Lease{}, fmt.Errorf("%w: run %s is %v", types.ErrRunNotActive, runID, rec.run.State)
	}
	if rec.token != t {
		return Lease{}, fmt.Errorf("%w: run %s waits on another token", types.ErrTokenMismatch, runID)
	}
	now := s.now()
	if owner, live := s.liveSessionOwnerLocked(rec.run.SessionID, now); live {
		return Lease{}, fmt.Errorf("%w: run %s", types.ErrRunActive, owner)
	}
	if err := s.resumeLeaseLocked(rec, runID, now, ttl); err != nil {
		return Lease{}, err
	}
	return rec.lease, nil
}
