package gohan

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// ShutdownIncomplete names the runs still in flight when the shutdown
// deadline passed. It satisfies errors.Is against
// types.ErrShutdownIncomplete.
type ShutdownIncomplete struct {
	RunIDs []string
}

func (e *ShutdownIncomplete) Error() string {
	return fmt.Sprintf("gohan: shutdown deadline passed with %d run(s) in flight", len(e.RunIDs))
}

func (e *ShutdownIncomplete) Is(target error) bool {
	return target == types.ErrShutdownIncomplete
}

// The run registry lives beside the Stack struct because build.go belongs to
// another chunk this wave: state is keyed by *Stack until the run entry
// point lands and the fields move into Stack. A run registers through
// beginRun before Runs.Start and clears through endRun at its safe point;
// Shutdown arms every registered preemption request and waits for the safe
// points to land.
type inflight struct {
	mu   sync.Mutex
	runs map[string]*Preemptor
}

type shutdownState struct {
	mu           sync.Mutex
	shuttingDown bool
}

var (
	inflightMu     sync.Mutex
	inflights      = map[*Stack]*inflight{}
	shutdownMu     sync.Mutex
	shutdownStates = map[*Stack]*shutdownState{}
)

func (s *Stack) inflightRuns() *inflight {
	inflightMu.Lock()
	defer inflightMu.Unlock()
	f, ok := inflights[s]
	if !ok {
		f = &inflight{runs: make(map[string]*Preemptor)}
		inflights[s] = f
	}
	return f
}

func (s *Stack) getShutdownState() *shutdownState {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()
	st, ok := shutdownStates[s]
	if !ok {
		st = &shutdownState{}
		shutdownStates[s] = st
	}
	return st
}

func (s *Stack) shuttingDown() bool {
	st := s.getShutdownState()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.shuttingDown
}

// beginRun admits a run the stack will drive and returns
// types.ErrShuttingDown once Shutdown has begun, before Runs.Start.
func (s *Stack) beginRun(runID string, pre *Preemptor) error {
	st := s.getShutdownState()
	st.mu.Lock()
	down := st.shuttingDown
	st.mu.Unlock()
	if down {
		return types.ErrShuttingDown
	}
	f := s.inflightRuns()
	f.mu.Lock()
	f.runs[runID] = pre
	f.mu.Unlock()
	return nil
}

// endRun clears a run whose suspension landed at its persisted safe point.
// A run the shutdown deadline cut short stays registered: Recover reclaims
// it as stale.
func (s *Stack) endRun(runID string) {
	f := s.inflightRuns()
	f.mu.Lock()
	delete(f.runs, runID)
	f.mu.Unlock()
}

// Shutdown refuses new runs with types.ErrShuttingDown, stops in-flight
// runs by arming their preemption requests and waits until every safe point
// has landed — the run persists its checkpoint and suspends itself, so the
// wait is the flush of pending writes. When the ctx deadline passes with
// runs still in flight it returns types.ErrShutdownIncomplete naming them.
// Calls after the first return nil.
func (s *Stack) Shutdown(ctx context.Context) error {
	st := s.getShutdownState()
	st.mu.Lock()
	if st.shuttingDown {
		st.mu.Unlock()
		return nil
	}
	st.shuttingDown = true
	st.mu.Unlock()

	f := s.inflightRuns()
	f.mu.Lock()
	for _, pre := range f.runs {
		pre.Preempt()
	}
	f.mu.Unlock()

	if ids := s.waitInflight(ctx, f); len(ids) > 0 {
		return &ShutdownIncomplete{RunIDs: ids}
	}
	inflightMu.Lock()
	delete(inflights, s)
	inflightMu.Unlock()
	return nil
}

func (s *Stack) waitInflight(ctx context.Context, f *inflight) []string {
	t := time.NewTicker(time.Millisecond)
	defer t.Stop()
	for {
		f.mu.Lock()
		if len(f.runs) == 0 {
			f.mu.Unlock()
			return nil
		}
		ids := make([]string, 0, len(f.runs))
		for id := range f.runs {
			ids = append(ids, id)
		}
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			slices.Sort(ids)
			return ids
		case <-t.C:
		}
	}
}
