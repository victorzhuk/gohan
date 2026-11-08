package gohan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// The execution lease and the suspension checkpoint are separate
// lifetimes: a conversation boundary must survive the deadline the
// execution happened to hold, so the driver hands the checkpoint over
// without an expiry and the store's TTL policy decides the lifetime.
func TestCheckpointLifetime(t *testing.T) {
	t.Run("stores.checkpoint-lifetime-independent", func(t *testing.T) {
		start := time.Unix(1700000000, 0)
		now := start
		clock := func() time.Time { return now }
		ctx := context.Background()

		runs := stores.NewMemoryRuns(
			stores.WithMemoryRunClock(clock),
			stores.WithMemoryRunInfo(types.RunInfoFrom),
		)
		cpTTL := time.Hour
		cps := stores.NewMemoryCheckpoints(
			stores.WithMemoryCheckpointClock(clock),
			stores.WithMemoryCheckpointTTL(cpTTL),
			stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom),
		)

		lease, err := runs.Start(ctx, stores.Run{RunID: "run-cb", SessionID: "s-cb"}, 10*time.Second)
		if err != nil {
			t.Fatalf("start run: %v", err)
		}
		// The heartbeat refresh carries the execution past its initial
		// lease deadline of start+10s.
		now = now.Add(5 * time.Second)
		hb, err := runs.Heartbeat(ctx, lease)
		if err != nil {
			t.Fatalf("heartbeat: %v", err)
		}
		if !hb.Expires.After(start.Add(10 * time.Second)) {
			t.Fatalf("refreshed lease expiry %v, want past the initial deadline %v", hb.Expires, start.Add(10*time.Second))
		}
		now = now.Add(7 * time.Second)
		suspendedAt := now

		rt := &lcRuntime{script: []lcStep{{status: runtime.SuspendedStatus}}}
		lc := NewLifecycle(
			WithLifecycleRuns(runs, hb),
			WithLifecycleSession("s-cb"),
			WithLifecycleFlow("chat"),
		)
		var saved stores.Checkpoint
		ag := runtime.AgentRun{Save: func(_ context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
			saved = cp
			return cps.Put(ctx, cp)
		}}
		var token types.ResumeToken
		for ev, err := range DriveLifecycle(ctx, lc, rt, ag) {
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
			if s, ok := ev.(types.Suspended); ok {
				token = s.Token
			}
		}
		if token == "" {
			t.Fatal("no suspension token")
		}
		if !saved.ExpiresAt.IsZero() {
			t.Fatalf("checkpoint expiry %v set by the driver, want the store policy to own the lifetime", saved.ExpiresAt)
		}

		live, err := cps.Peek(ctx, token)
		if err != nil {
			t.Fatalf("checkpoint unusable at suspend time: %v", err)
		}
		if !live.ExpiresAt.Equal(suspendedAt.Add(cpTTL)) {
			t.Fatalf("checkpoint expiry %v, want store clock at suspend plus ttl %v", live.ExpiresAt, suspendedAt.Add(cpTTL))
		}

		// Past the refreshed lease deadline (start+15s) the checkpoint
		// must still be resumable: the lease lifetime ended, the
		// checkpoint's own has not.
		now = start.Add(16 * time.Second)
		if _, err := cps.Peek(ctx, token); err != nil {
			t.Fatalf("checkpoint expired with the lease at %v: %v", now, err)
		}

		now = suspendedAt.Add(cpTTL)
		if _, err := cps.Consume(ctx, token, stores.ResumeInput{Verdict: stores.VerdictApprove}); !errors.Is(err, types.ErrTokenExpired) {
			t.Fatalf("Consume err = %v, want ErrTokenExpired at the checkpoint's own expiry %v", err, now)
		}
	})
}
