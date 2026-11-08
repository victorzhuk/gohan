package stores

import (
	"context"
	"sort"

	"github.com/victorzhuk/gohan/core/types"
)

// Preempted lists up to limit runs parked at a persisted safe point, in
// heartbeat order. The reason recorded with SuspendWithReason decides: only
// actual preemptions are discoverable. Records without a reason carry no
// checkpoint evidence here, so they are never claimed as preempted. The
// reason filter runs before the limit.
func (s *MemoryRuns) Preempted(ctx context.Context, limit int) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var parked []Run
	for _, rec := range s.runs {
		if rec.run.State != Suspended || rec.reason != types.Preempted {
			continue
		}
		run, err := s.decodeRun(ctx, rec)
		if err != nil {
			return nil, err
		}
		parked = append(parked, run)
	}
	sort.Slice(parked, func(i, j int) bool { return parked[i].Heartbeat.Before(parked[j].Heartbeat) })
	if len(parked) > limit {
		parked = parked[:limit]
	}
	return parked, nil
}
