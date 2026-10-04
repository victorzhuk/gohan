package gohan

import (
	"context"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
)

// StreamHeartbeat keeps a run's lease fresh from a run-owned goroutine
// (runtime per-step rule 4; streams *Slow consumers* rule 1). It never
// runs on the consumer's goroutine, so a stalled consumer cannot let the
// lease expire.
type StreamHeartbeat struct {
	runs  stores.Runs
	every time.Duration

	mu    sync.Mutex
	lease stores.Lease
	err   error

	stop chan struct{}
	done chan struct{}
}

// StartStreamHeartbeat refreshes the lease every every until Stop or the
// ctx ends. A non-positive every falls back to stores.HeartbeatEvery.
func StartStreamHeartbeat(ctx context.Context, runs stores.Runs, lease stores.Lease, every time.Duration) *StreamHeartbeat {
	if every <= 0 {
		every = stores.HeartbeatEvery
	}
	h := &StreamHeartbeat{
		runs:  runs,
		every: every,
		lease: lease,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go h.loop(ctx)
	return h
}

func (h *StreamHeartbeat) loop(ctx context.Context) {
	defer close(h.done)
	ticker := time.NewTicker(h.every)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.mu.Lock()
			lease := h.lease
			h.mu.Unlock()
			next, err := h.runs.Heartbeat(ctx, lease)
			if err != nil {
				h.mu.Lock()
				h.err = err
				h.mu.Unlock()
				return
			}
			h.mu.Lock()
			h.lease = next
			h.mu.Unlock()
		}
	}
}

// Lease reports the freshest lease the heartbeat holds.
func (h *StreamHeartbeat) Lease() stores.Lease {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lease
}

// Err reports the first heartbeat failure, if any. A failed refresh ends
// the heartbeat: the lease is already lost and only a reclaim can resume.
func (h *StreamHeartbeat) Err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

// Stop ends the heartbeat and waits for its goroutine to exit, so the
// iterator returns with no helper left running. It returns the last lease
// held.
func (h *StreamHeartbeat) Stop() stores.Lease {
	close(h.stop)
	<-h.done
	return h.Lease()
}
