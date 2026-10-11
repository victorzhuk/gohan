//go:build full

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func newRemediationRuns(t *testing.T, clk *advClock) (*postgres.Runs, *postgres.DB) {
	t.Helper()
	db := newDB(t, bindSchema("run_repair"), postgres.WithClock(clk.Now))
	return postgres.NewRuns(db, postgres.WithRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
		return types.RunInfo{Principal: bindPrincipal}, true
	})), db
}

func startRun(t *testing.T, ctx context.Context, s *postgres.Runs, runID, sessionID, operationID string, ttl time.Duration) stores.Lease {
	t.Helper()
	l, err := s.Start(ctx, stores.Run{RunID: runID, SessionID: sessionID, OperationID: operationID}, ttl)
	if err != nil {
		t.Fatalf("start run %s: %v", runID, err)
	}
	return l
}

func TestRunsRemediation(t *testing.T) {
	t.Run("stores.pg-operation-id-cross-session-race", func(t *testing.T) {
		ctx := context.Background()
		clk := &advClock{now: time.Now()}
		s, _ := newRemediationRuns(t, clk)
		const op = "op-cross-session"
		const runs = 8
		type result struct {
			lease stores.Lease
			err   error
		}
		results := make([]result, runs)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range runs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				l, err := s.Start(ctx, stores.Run{
					RunID:       "r-cross-" + string(rune('a'+i)),
					SessionID:   "s-cross-" + string(rune('a'+i)),
					OperationID: op,
				}, stores.LeaseTTL)
				results[i] = result{lease: l, err: err}
			}(i)
		}
		close(start)
		wg.Wait()
		var winners []string
		for i, r := range results {
			if r.err == nil {
				winners = append(winners, r.lease.RunID)
				continue
			}
			var dup stores.OperationExistsError
			if !errors.As(r.err, &dup) {
				t.Fatalf("loser %d err = %v, want OperationExistsError", i, r.err)
			}
			if len(winners) > 0 && dup.RunID != winners[0] {
				t.Fatalf("loser %d names run %s, want the recorded run %s", i, dup.RunID, winners[0])
			}
		}
		if len(winners) != 1 {
			t.Fatalf("operation %s started %d runs, want exactly one", op, len(winners))
		}
	})

	t.Run("stores.pg-run-for-session-active", func(t *testing.T) {
		ctx := context.Background()
		clk := &advClock{now: time.Now()}
		s, db := newRemediationRuns(t, clk)
		// A second pod builds its own handle without the run info source.
		other := postgres.NewRuns(newDB(t, db.Schema()))
		startRun(t, ctx, s, "r-live", "s-live", "", stores.LeaseTTL)
		run, err := other.RunForSession(ctx, "s-live")
		if err != nil {
			t.Fatalf("RunForSession: %v", err)
		}
		if run.RunID != "r-live" || run.State != stores.Running {
			t.Fatalf("RunForSession = (%s, %v), want (r-live, Running)", run.RunID, run.State)
		}
		if err := other.Signal(ctx, "r-live", stores.Signal{Kind: stores.SignalCancel}); err != nil {
			t.Fatalf("Signal on found run: %v", err)
		}
	})

	t.Run("stores.pg-run-for-session-none", func(t *testing.T) {
		ctx := context.Background()
		clk := &advClock{now: time.Now()}
		s, _ := newRemediationRuns(t, clk)
		startRun(t, ctx, s, "r-expired", "s-expired", "", stores.LeaseTTL)
		clk.Advance(stores.LeaseTTL + time.Second)
		if _, err := s.RunForSession(ctx, "s-expired"); !errors.Is(err, stores.ErrRunNotFound) {
			t.Fatalf("expired lease err = %v, want ErrRunNotFound", err)
		}
	})

	t.Run("stores.pg-run-for-session-ineligible", func(t *testing.T) {
		ctx := context.Background()
		clk := &advClock{now: time.Now()}
		s, _ := newRemediationRuns(t, clk)
		l := startRun(t, ctx, s, "r-term", "s-term", "", stores.LeaseTTL)
		if err := s.Finish(ctx, l, stores.Failed, nil, ""); err != nil {
			t.Fatalf("finish: %v", err)
		}
		if _, err := s.RunForSession(ctx, "s-term"); !errors.Is(err, stores.ErrRunNotFound) {
			t.Fatalf("terminal run err = %v, want ErrRunNotFound", err)
		}

		l = startRun(t, ctx, s, "r-susp", "s-susp", "", stores.LeaseTTL)
		if err := s.Suspend(ctx, l, types.ResumeToken("tok")); err != nil {
			t.Fatalf("suspend: %v", err)
		}
		if _, err := s.RunForSession(ctx, "s-susp"); !errors.Is(err, stores.ErrRunNotFound) {
			t.Fatalf("suspended run err = %v, want ErrRunNotFound", err)
		}
	})
}
