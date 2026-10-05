package gohan

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
)

func TestStreamHeartbeat(t *testing.T) {
	t.Run("streams.heartbeat-independent-of-consumer", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := stores.NewMemoryRuns()
			ctx := context.Background()
			started, err := s.Start(ctx, stores.Run{SessionID: "s1", RunID: "r1"}, stores.LeaseTTL)
			if err != nil {
				t.Fatalf("Start err = %v", err)
			}
			hb := StartStreamHeartbeat(ctx, s, started, 0)

			// The consumer blocks inside the iterator for twice the lease
			// TTL; the heartbeat runs on its own goroutine meanwhile.
			time.Sleep(2 * stores.LeaseTTL)
			synctest.Wait()

			stale, err := s.Stale(ctx, stores.LeaseTTL, 10)
			if err != nil {
				t.Fatalf("Stale err = %v", err)
			}
			if len(stale) != 0 {
				t.Fatalf("Recover would reclaim %d runs, want 0", len(stale))
			}
			if fresh := hb.Lease(); !fresh.Expires.After(started.Expires) {
				t.Fatalf("lease not refreshed: started %v, current %v", started.Expires, fresh.Expires)
			}

			last := hb.Stop()
			if hb.Err() != nil {
				t.Fatalf("heartbeat err = %v", hb.Err())
			}
			if !last.Expires.After(started.Expires) {
				t.Fatalf("stop lost the refreshed lease: %v", last.Expires)
			}
			live, err := s.ByID(ctx, "r1")
			if err != nil {
				t.Fatalf("ByID err = %v", err)
			}
			if live.State != stores.Running {
				t.Fatalf("run state = %v, want Running", live.State)
			}
		})
	})

	t.Run("heartbeat failure surfaces to the driver", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			base := stores.NewMemoryRuns()
			boom := errors.New("gohan: store closed")
			s := &failingHeartbeatRuns{MemoryRuns: base, err: boom}
			ctx := context.Background()
			started, err := base.Start(ctx, stores.Run{SessionID: "s2", RunID: "r2"}, stores.LeaseTTL)
			if err != nil {
				t.Fatalf("Start err = %v", err)
			}
			hb := StartStreamHeartbeat(ctx, s, started, 0)
			time.Sleep(stores.HeartbeatEvery)
			synctest.Wait()

			hb.Stop()
			if !errors.Is(hb.Err(), boom) {
				t.Fatalf("Err = %v, want %v", hb.Err(), boom)
			}
		})
	})
}

type failingHeartbeatRuns struct {
	*stores.MemoryRuns
	err error
}

func (f *failingHeartbeatRuns) Heartbeat(ctx context.Context, l stores.Lease) (stores.Lease, error) {
	return stores.Lease{}, f.err
}
