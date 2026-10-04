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

func TestStoreClock(t *testing.T) {
	ctx := context.Background()

	t.Run("stores.stale-by-store-clock", func(t *testing.T) {
		now := time.Unix(1700000000, 0)
		clock := now
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return clock }))
		if _, err := s.Start(ctx, Run{RunID: "run-1", SessionID: "s1"}, LeaseTTL); err != nil {
			t.Fatalf("Start err = %v", err)
		}

		// The heartbeat is 20 s old by the store clock; wall time has not
		// moved and the store never reads it.
		clock = now.Add(20 * time.Second)
		wall := time.Now()
		stale, err := s.Stale(ctx, 30*time.Second, 10)
		if err != nil || len(stale) != 0 {
			t.Fatalf("Stale err = %v, n = %d, want none at 20 s", err, len(stale))
		}
		if time.Since(wall) > time.Minute {
			t.Fatalf("wall clock moved %v during the call", time.Since(wall))
		}

		// Only the store clock makes the run stale.
		clock = now.Add(30 * time.Second)
		stale, err = s.Stale(ctx, 30*time.Second, 10)
		if err != nil || len(stale) != 1 || stale[0].RunID != "run-1" {
			t.Fatalf("Stale err = %v, n = %d, want run-1 at 30 s", err, len(stale))
		}
	})

	t.Run("stores.skewed-caller-cannot-reclaim", func(t *testing.T) {
		now := time.Unix(1700000000, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		lease, err := s.Start(ctx, Run{RunID: "run-1", SessionID: "s1"}, LeaseTTL)
		if err != nil {
			t.Fatalf("Start err = %v", err)
		}

		// The other pod's wall clock is 5 min ahead, but Stale and Reclaim
		// compare only against the store clock, which says the lease is live.
		if _, err := s.Heartbeat(ctx, lease); err != nil {
			t.Fatalf("Heartbeat err = %v", err)
		}
		stale, err := s.Stale(ctx, time.Second, 10)
		if err != nil || len(stale) != 0 {
			t.Fatalf("Stale err = %v, n = %d, want none while the lease is live", err, len(stale))
		}
		if _, err := s.Reclaim(ctx, Run{RunID: "run-1"}, time.Second); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Reclaim err = %v, want ErrRunNotActive for a live lease", err)
		}
	})

	t.Run("stores.checkpoint-expiry-store-clock", func(t *testing.T) {
		now := time.Unix(1700000000, 0)
		clock := now
		s := NewMemoryCheckpoints(WithMemoryCheckpointClock(func() time.Time { return clock }))
		tok, err := s.Put(ctx, Checkpoint{SessionID: "s1", ExpiresAt: now.Add(time.Minute)})
		if err != nil {
			t.Fatalf("Put err = %v", err)
		}

		// The caller's own clock is 10 min behind the store clock, yet
		// expiry is decided at the store.
		wallBehind := now.Add(-10 * time.Minute)
		if wallBehind.After(now) {
			t.Fatal("caller clock should be behind the store clock")
		}
		clock = now.Add(2 * time.Minute)
		if _, err := s.Consume(ctx, tok, ResumeInput{Verdict: VerdictApprove}); !errors.Is(err, types.ErrTokenExpired) {
			t.Fatalf("Consume err = %v, want ErrTokenExpired by store time", err)
		}
	})

	t.Run("stores.memstore-clock-jump", func(t *testing.T) {
		now := time.Unix(1700000000, 0)
		clock := now
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return clock }))
		if _, err := s.Start(ctx, Run{RunID: "run-1", SessionID: "s1"}, LeaseTTL); err != nil {
			t.Fatalf("Start err = %v", err)
		}

		stale, err := s.Stale(ctx, LeaseTTL, 10)
		if err != nil || len(stale) != 0 {
			t.Fatalf("Stale err = %v, n = %d, want none before the jump", err, len(stale))
		}

		// One jump of the memory clock past LeaseTTL without a heartbeat
		// moves staleness, lease expiry and reclaim together.
		clock = now.Add(LeaseTTL + time.Second)
		stale, err = s.Stale(ctx, LeaseTTL, 10)
		if err != nil || len(stale) != 1 || stale[0].RunID != "run-1" {
			t.Fatalf("Stale err = %v, n = %d, want run-1 after the jump", err, len(stale))
		}
		if s.SessionLeaseActive(ctx, "s1") {
			t.Fatal("lease should be expired after the clock jump")
		}
		lease, err := s.Reclaim(ctx, stale[0], LeaseTTL)
		if err != nil {
			t.Fatalf("Reclaim err = %v", err)
		}
		if !lease.Expires.Equal(clock.Add(LeaseTTL)) {
			t.Fatalf("Reclaim expires = %v, want %v by store time", lease.Expires, clock.Add(LeaseTTL))
		}
	})

	t.Run("heartbeat-at-threshold", func(t *testing.T) {
		now := time.Unix(1700000000, 0)
		clock := now
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return clock }))
		if _, err := s.Start(ctx, Run{RunID: "run-1", SessionID: "s1"}, time.Minute); err != nil {
			t.Fatalf("Start err = %v", err)
		}

		// A heartbeat exactly staleAfter old is stale: the cutoff is
		// inclusive, so the reaper reclaims at the boundary, not after it.
		clock = now.Add(30 * time.Second)
		stale, err := s.Stale(ctx, 30*time.Second, 10)
		if err != nil || len(stale) != 1 {
			t.Fatalf("Stale err = %v, n = %d, want run-1 exactly at the threshold", err, len(stale))
		}

		// A heartbeat one tick fresher than the threshold is not stale.
		clock = now.Add(30*time.Second - time.Millisecond)
		stale, err = s.Stale(ctx, 30*time.Second, 10)
		if err != nil || len(stale) != 0 {
			t.Fatalf("Stale err = %v, n = %d, want none just inside the threshold", err, len(stale))
		}
	})

	t.Run("reclaim-single-winner", func(t *testing.T) {
		now := time.Unix(1700000000, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		if _, err := s.Start(ctx, Run{RunID: "run-1", SessionID: "s1"}, time.Second); err != nil {
			t.Fatalf("Start err = %v", err)
		}
		now = now.Add(5 * time.Second)
		stale, err := s.Stale(ctx, time.Second, 10)
		if err != nil || len(stale) != 1 {
			t.Fatalf("Stale err = %v, n = %d, want one stale run", err, len(stale))
		}

		var wg sync.WaitGroup
		var wins, losers atomic.Int32
		gun := make(chan struct{})
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gun
				if _, err := s.Reclaim(ctx, stale[0], time.Second); err == nil {
					wins.Add(1)
				} else {
					losers.Add(1)
				}
			}()
		}
		close(gun)
		wg.Wait()
		if wins.Load() != 1 || losers.Load() != 7 {
			t.Fatalf("reclaim winners = %d, losers = %d, want 1 and 7", wins.Load(), losers.Load())
		}
	})
}
