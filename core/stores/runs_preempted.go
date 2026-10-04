package stores

import (
	"context"
	"sort"
)

// Preempted lists up to limit runs parked at a persisted safe point, in
// heartbeat order. The run row carries no suspend reason, so the list
// covers every Suspended run; the caller filters through the checkpoint
// when the reason matters.
func (s *MemoryRuns) Preempted(ctx context.Context, limit int) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var parked []Run
	for _, rec := range s.runs {
		if rec.run.State != Suspended {
			continue
		}
		parked = append(parked, cloneRun(rec.run))
	}
	sort.Slice(parked, func(i, j int) bool { return parked[i].Heartbeat.Before(parked[j].Heartbeat) })
	if len(parked) > limit {
		parked = parked[:limit]
	}
	return parked, nil
}
