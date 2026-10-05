package stores

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestRunsLeasesStaleGeneration(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
	ctx := context.Background()

	l, err := s.Start(ctx, Run{RunID: "run-sg1", SessionID: "s-sg1"}, time.Second)
	if err != nil {
		t.Fatalf("Start err = %v", err)
	}

	// Past the ttl by store time, the lease is a stale generation: all
	// four lease-holder methods refuse it and the run stays in place.
	now = now.Add(2 * time.Second)
	if _, err := s.Heartbeat(ctx, l); !errors.Is(err, types.ErrRunNotActive) {
		t.Fatalf("Heartbeat err = %v, want ErrRunNotActive", err)
	}
	if err := s.Finish(ctx, l, Finished, nil, ""); !errors.Is(err, types.ErrRunNotActive) {
		t.Fatalf("Finish err = %v, want ErrRunNotActive", err)
	}
	if err := s.Suspend(ctx, l, "tok-sg1"); !errors.Is(err, types.ErrRunNotActive) {
		t.Fatalf("Suspend err = %v, want ErrRunNotActive", err)
	}
	if _, err := s.Drain(ctx, l); !errors.Is(err, types.ErrRunNotActive) {
		t.Fatalf("Drain err = %v, want ErrRunNotActive", err)
	}

	l2, err := s.Reclaim(ctx, Run{RunID: "run-sg1", SessionID: "s-sg1"}, time.Minute)
	if err != nil {
		t.Fatalf("Reclaim err = %v", err)
	}

	// must not let the previous generation regain ownership.
	oldGeneration := l
	oldGeneration.Expires = l2.Expires
	if _, err := s.Heartbeat(ctx, oldGeneration); !errors.Is(err, types.ErrRunNotActive) {
		t.Fatalf("Heartbeat with stale generation and current expiry err = %v, want ErrRunNotActive", err)
	}
	if _, err := s.Drain(ctx, oldGeneration); !errors.Is(err, types.ErrRunNotActive) {
		t.Fatalf("Drain with stale generation and current expiry err = %v, want ErrRunNotActive", err)
	}
	if l2.Generation == l.Generation || l.Generation == 0 || l2.Generation == 0 {
		t.Fatalf("lease generations = %d then %d, want distinct nonzero values", l.Generation, l2.Generation)
	}
	if _, err := s.Heartbeat(ctx, l2); err != nil {
		t.Fatalf("Heartbeat on reclaimed lease err = %v", err)
	}
	if err := s.Finish(ctx, l2, Finished, nil, ""); err != nil {
		t.Fatalf("Finish on reclaimed lease err = %v", err)
	}

}

func TestHeartbeatRefreshesStaleness(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
	ctx := context.Background()

	l, err := s.Start(ctx, Run{RunID: "run-hb1", SessionID: "s-hb1"}, LeaseTTL)
	if err != nil {
		t.Fatalf("Start err = %v", err)
	}

	now = now.Add(HeartbeatEvery)
	next, err := s.Heartbeat(ctx, l)
	if err != nil {
		t.Fatalf("Heartbeat err = %v", err)
	}
	if next.Generation != l.Generation {
		t.Fatalf("Heartbeat generation = %d, want same %d", next.Generation, l.Generation)
	}
	if !next.Expires.After(l.Expires) {
		t.Fatalf("Heartbeat expiry %v not after %v", next.Expires, l.Expires)
	}

	stale, err := s.Stale(ctx, LeaseTTL, 10)
	if err != nil {
		t.Fatalf("Stale err = %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("Stale listed %d runs after heartbeat, want 0", len(stale))
	}
	if err := s.Finish(ctx, next, Finished, nil, ""); err != nil {
		t.Fatalf("Finish on refreshed lease err = %v", err)
	}
}

func TestRunsLeases(t *testing.T) {
	t.Run("stores.lease-exclusivity", func(t *testing.T) {
		now := time.Unix(1700000000, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		ctx := context.Background()

		var wg sync.WaitGroup
		var wins atomic.Int32
		var activeErrs atomic.Int32
		start := make(chan struct{})
		for i := range 2 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				_, err := s.Start(ctx, Run{RunID: "run-s1-1", SessionID: "s1"}, time.Second)
				switch {
				case err == nil:
					wins.Add(1)
				case errors.Is(err, types.ErrRunActive):
					activeErrs.Add(1)
				default:
					t.Errorf("pod %d: unexpected err = %v", i, err)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		if wins.Load() != 1 || activeErrs.Load() != 1 {
			t.Fatalf("wins = %d, ErrRunActive = %d, want 1 and 1", wins.Load(), activeErrs.Load())
		}
		if !s.SessionLeaseActive(ctx, "s1") {
			t.Fatal("session lease should be active")
		}

		// store-time expiry frees the session; wall clock never moved
		now = now.Add(2 * time.Second)
		if s.SessionLeaseActive(ctx, "s1") {
			t.Fatal("lease should be expired by store time")
		}
		if _, err := s.Start(ctx, Run{RunID: "run-s1-3", SessionID: "s1"}, time.Second); err != nil {
			t.Fatalf("Start after store-time expiry err = %v", err)
		}
	})

	t.Run("stores.reclaim-race", func(t *testing.T) {
		now := time.Unix(1700000000, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		ctx := context.Background()

		l, err := s.Start(ctx, Run{RunID: "run-s1", SessionID: "s1"}, time.Second)
		if err != nil {
			t.Fatalf("Start err = %v", err)
		}
		if _, err := s.Heartbeat(ctx, l); err != nil {
			t.Fatalf("Heartbeat err = %v", err)
		}
		if got := (s.runs["run-s1"].lease.Expires); !got.Equal(now.Add(time.Second)) {
			t.Fatalf("heartbeat expires = %v, want %v by store time", got, now.Add(time.Second))
		}

		now = now.Add(5 * time.Second)
		stale, err := s.Stale(ctx, time.Second, 10)
		if err != nil || len(stale) != 1 {
			t.Fatalf("Stale err = %v, n = %d, want 1 stale run", err, len(stale))
		}

		var wg sync.WaitGroup
		var wins atomic.Int32
		var losers atomic.Int32
		gun := make(chan struct{})
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gun
				lease, err := s.Reclaim(ctx, stale[0], time.Second)
				if err == nil {
					if lease.RunID != "run-s1" {
						t.Errorf("winner lease RunID = %s", lease.RunID)
					}
					wins.Add(1)
				} else if errors.Is(err, types.ErrRunNotActive) {
					losers.Add(1)
				} else {
					t.Errorf("Reclaim err = %v", err)
				}
			}()
		}
		close(gun)
		wg.Wait()
		if wins.Load() != 1 || losers.Load() != 9 {
			t.Fatalf("reclaim winners = %d, losers = %d, want 1 and 9", wins.Load(), losers.Load())
		}
	})
}
