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
	rec.lease = Lease{RunID: rec.run.RunID, Expires: now.Add(rec.ttl)}
	return rec.lease, nil
}

// Reclaim takes a fresh lease on a run that Stale listed: state Running or
// Resuming with a heartbeat older than the caller's staleAfter. Under the
// store lock the recheck is atomic, so of many concurrent reapers exactly
// one wins and the rest see a heartbeat that is no longer stale.
func (s *MemoryRuns) Reclaim(ctx context.Context, r Run, ttl time.Duration) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[r.RunID]
	if !ok {
		return Lease{}, fmt.Errorf("%w: run %s", ErrRunNotFound, r.RunID)
	}
	now := s.now()
	if rec.run.State != Running && rec.run.State != Resuming {
		return Lease{}, fmt.Errorf("%w: run %s is %v", types.ErrRunNotActive, r.RunID, rec.run.State)
	}
	if !rec.expiredAt(now) {
		return Lease{}, fmt.Errorf("%w: run %s lease is live", types.ErrRunNotActive, r.RunID)
	}
	rec.ttl = ttl
	rec.lease = Lease{RunID: r.RunID, Expires: now.Add(ttl)}
	rec.run.Heartbeat = now
	rec.live = true
	return rec.lease, nil
}
