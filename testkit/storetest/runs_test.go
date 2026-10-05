package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type fakeRun struct {
	row      RunRow
	lease    Lease
	ttl      time.Duration
	expires  time.Time
	live     bool
	signals  []Signal
	noticeID string
}

type fakeRuns struct {
	mu         sync.Mutex
	now        time.Time
	runs       map[string]*fakeRun
	onSess     map[string]string
	byOp       map[string]string
	generation uint64
	seq        int
}

type fakeRunsClock struct {
	s *fakeRuns
}

func (c fakeRunsClock) Advance(d time.Duration) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	c.s.now = c.s.now.Add(d)
}

func newFakeRuns(_ *testing.T) (RunStore, RunClock) {
	s := &fakeRuns{
		now:    time.Now(),
		runs:   map[string]*fakeRun{},
		onSess: map[string]string{},
		byOp:   map[string]string{},
	}
	return s, fakeRunsClock{s}
}

func (s *fakeRuns) expired(rec *fakeRun) bool {
	return !rec.expires.IsZero() && !s.now.Before(rec.expires)
}

func (s *fakeRuns) nextGenerationLocked() uint64 {
	s.generation++
	return s.generation
}

func (s *fakeRuns) held(l Lease) (*fakeRun, error) {
	rec, ok := s.runs[l.RunID]
	if !ok || l.Generation == 0 || rec.lease.Generation != l.Generation || !rec.live || s.expired(rec) {
		return nil, fmt.Errorf("%w: run %s", types.ErrRunNotActive, l.RunID)
	}
	return rec, nil
}

func (s *fakeRuns) Start(_ context.Context, r RunRow, ttl time.Duration) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if r.OperationID != "" {
		if _, ok := s.byOp[r.OperationID]; ok {
			return Lease{}, fmt.Errorf("%w: %s", types.ErrOperationExists, r.OperationID)
		}
	}
	if prev, ok := s.onSess[r.SessionID]; ok {
		if rec := s.runs[prev]; rec.live && !s.expired(rec) {
			return Lease{}, fmt.Errorf("%w: session %s", types.ErrRunActive, r.SessionID)
		}
	}
	r.State = RunRunning
	r.Heartbeat = s.now
	generation := s.nextGenerationLocked()
	rec := &fakeRun{row: r, lease: Lease{RunID: r.RunID, Generation: generation}, ttl: ttl, expires: s.now.Add(ttl), live: true}
	s.runs[r.RunID] = rec
	s.onSess[r.SessionID] = r.RunID
	if r.OperationID != "" {
		s.byOp[r.OperationID] = r.RunID
	}
	return rec.lease, nil
}

func (s *fakeRuns) Heartbeat(_ context.Context, l Lease) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.held(l)
	if err != nil {
		return Lease{}, err
	}
	rec.expires = s.now.Add(rec.ttl)
	return rec.lease, nil
}

func (s *fakeRuns) Suspend(_ context.Context, l Lease, _ types.ResumeToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.held(l)
	if err != nil {
		return err
	}
	rec.row.State = RunSuspended
	rec.live = false
	rec.expires = time.Time{}
	s.notice(rec)
	return nil
}

func (s *fakeRuns) Resuming(_ context.Context, runID string, ttl time.Duration) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.runs[runID]
	if !ok {
		return Lease{}, fmt.Errorf("no run %s", runID)
	}
	if rec.row.State != RunSuspended {
		return Lease{}, fmt.Errorf("%w: run %s is %d", types.ErrRunNotActive, runID, rec.row.State)
	}
	rec.row.State = RunResuming
	rec.ttl = ttl
	rec.lease = Lease{RunID: runID, Generation: s.nextGenerationLocked()}
	rec.expires = s.now.Add(ttl)
	rec.live = true
	return rec.lease, nil
}

func (s *fakeRuns) Finish(_ context.Context, l Lease, state RunState, uncertain []types.CallKey, resultRef string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.held(l)
	if err != nil {
		return err
	}
	rec.row.State = state
	rec.row.Uncertain = append([]types.CallKey(nil), uncertain...)
	rec.row.ResultRef = resultRef
	rec.live = false
	rec.expires = time.Time{}
	s.notice(rec)
	return nil
}

func (s *fakeRuns) ByOperation(_ context.Context, _, operationID string) (RunRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	runID, ok := s.byOp[operationID]
	if !ok {
		return RunRow{}, fmt.Errorf("no run for operation %s", operationID)
	}
	return s.runs[runID].row, nil
}

func (s *fakeRuns) Stale(_ context.Context, staleAfter time.Duration, limit int) ([]RunRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []RunRow
	for _, rec := range s.runs {
		if rec.row.State != RunRunning && rec.row.State != RunResuming {
			continue
		}
		if rec.row.Heartbeat.After(s.now.Add(-staleAfter)) {
			continue
		}
		out = append(out, rec.row)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (s *fakeRuns) Reclaim(_ context.Context, r RunRow, ttl time.Duration) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.runs[r.RunID]
	if !ok {
		return Lease{}, fmt.Errorf("no run %s", r.RunID)
	}
	if (rec.row.State != RunRunning && rec.row.State != RunResuming) || !s.expired(rec) {
		return Lease{}, fmt.Errorf("%w: run %s not reclaimable", types.ErrRunNotActive, r.RunID)
	}
	rec.ttl = ttl
	rec.lease = Lease{RunID: r.RunID, Generation: s.nextGenerationLocked()}
	rec.expires = s.now.Add(ttl)
	rec.row.Heartbeat = s.now
	rec.live = true
	return rec.lease, nil
}

func (s *fakeRuns) Signal(_ context.Context, runID string, sig Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.runs[runID]
	if !ok || rec.row.State != RunRunning {
		return fmt.Errorf("%w: run %s not running", types.ErrRunNotActive, runID)
	}
	if len(rec.signals) >= maxPendingSignals {
		return fmt.Errorf("%w: run %s", types.ErrMailboxFull, runID)
	}
	rec.signals = append(rec.signals, sig)
	return nil
}

func (s *fakeRuns) Drain(_ context.Context, l Lease) ([]Signal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.held(l)
	if err != nil {
		return nil, err
	}
	pending := rec.signals
	rec.signals = nil
	return pending, nil
}

func (s *fakeRuns) notice(rec *fakeRun) {
	s.seq++
	rec.noticeID = fmt.Sprintf("nt-%d", s.seq)
}

func (s *fakeRuns) Notices(_ context.Context, limit int) ([]types.RunNotice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []types.RunNotice
	for _, rec := range s.runs {
		if rec.noticeID == "" || len(out) == limit {
			continue
		}
		out = append(out, types.RunNotice{
			ID:        rec.noticeID,
			SessionID: rec.row.SessionID,
			RunID:     rec.row.RunID,
		})
	}
	return out, nil
}

func (s *fakeRuns) AckNotice(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, rec := range s.runs {
		if rec.noticeID == id {
			rec.noticeID = ""
			return nil
		}
	}
	return fmt.Errorf("no notice %s", id)
}

func TestStoretestRuns(t *testing.T) {
	Runs(t, newFakeRuns)
}
