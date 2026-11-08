package stores

import (
	"context"
	"sort"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// Preempted lists up to limit runs parked at a persisted safe point, in
// heartbeat order, ties broken by run id. The reason recorded with
// SuspendWithReason decides: only actual preemptions are discoverable.
// Records without a reason carry no checkpoint evidence here, so they are
// never claimed as preempted. The reason filter runs before the limit, and
// ranking works on the record's own fields so only the selected rows are
// decoded.
func (s *MemoryRuns) Preempted(ctx context.Context, limit int) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type parked struct {
		rec       *runRecord
		heartbeat time.Time
	}
	var cands []parked
	for _, rec := range s.runs {
		if rec.run.State != Suspended || rec.reason != types.Preempted {
			continue
		}
		cands = append(cands, parked{rec: rec, heartbeat: rec.run.Heartbeat})
	}
	sort.Slice(cands, func(i, j int) bool {
		if !cands[i].heartbeat.Equal(cands[j].heartbeat) {
			return cands[i].heartbeat.Before(cands[j].heartbeat)
		}
		return cands[i].rec.run.RunID < cands[j].rec.run.RunID
	})
	if limit < 0 {
		limit = 0
	}
	if len(cands) > limit {
		cands = cands[:limit]
	}
	out := make([]Run, 0, len(cands))
	for _, c := range cands {
		run, err := s.decodeRun(ctx, c.rec)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}
