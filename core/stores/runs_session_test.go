package stores

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestRunForSession(t *testing.T) {
	ctx := context.Background()
	owner := types.WithPrincipal(ctx, types.Principal{Tenant: "t-a", Subject: "u1"})

	t.Run("stores.run-for-session-cross-instance", func(t *testing.T) {
		// A store instance shared between processes answers a session
		// lookup started under a different caller: the row is the store's,
		// not the caller's.
		s := NewMemoryRuns()
		lease, err := s.Start(owner, Run{SessionID: "s1", RunID: "run-1", Flow: "chat"}, LeaseTTL)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if lease.RunID != "run-1" {
			t.Fatalf("lease run %q, want run-1", lease.RunID)
		}
		r, err := s.RunForSession(ctx, "s1")
		if err != nil {
			t.Fatalf("run for session: %v", err)
		}
		if r.RunID != "run-1" || r.State != Running {
			t.Fatalf("run = %+v, want run-1 Running", r)
		}

		// A resumed run is active too.
		if err := s.Suspend(owner, lease, types.ResumeToken("tok")); err != nil {
			t.Fatalf("suspend: %v", err)
		}
		if _, err := s.RunForSession(ctx, "s1"); !errors.Is(err, ErrRunNotFound) {
			t.Fatalf("suspended = %v, want ErrRunNotFound", err)
		}
		if _, err := s.Resuming(owner, "run-1", LeaseTTL); err != nil {
			t.Fatalf("resuming: %v", err)
		}
		r, err = s.RunForSession(ctx, "s1")
		if err != nil {
			t.Fatalf("resuming run for session: %v", err)
		}
		if r.State != Resuming {
			t.Fatalf("state = %v, want Resuming", r.State)
		}
	})

	t.Run("stores.run-for-session-absent", func(t *testing.T) {
		s := NewMemoryRuns()
		if _, err := s.RunForSession(ctx, "nope"); !errors.Is(err, ErrRunNotFound) {
			t.Fatalf("absent session = %v, want ErrRunNotFound", err)
		}
	})

	t.Run("stores.run-for-session-terminal", func(t *testing.T) {
		s := NewMemoryRuns()
		lease, err := s.Start(owner, Run{SessionID: "s1", RunID: "run-1", Flow: "chat"}, LeaseTTL)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if err := s.Finish(owner, lease, Finished, nil, ""); err != nil {
			t.Fatalf("finish: %v", err)
		}
		if _, err := s.RunForSession(ctx, "s1"); !errors.Is(err, ErrRunNotFound) {
			t.Fatalf("finished = %v, want ErrRunNotFound", err)
		}

		lease, err = s.Start(owner, Run{SessionID: "s2", RunID: "run-2", Flow: "chat"}, LeaseTTL)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if err := s.Finish(owner, lease, Failed, nil, ""); err != nil {
			t.Fatalf("fail: %v", err)
		}
		if _, err := s.RunForSession(ctx, "s2"); !errors.Is(err, ErrRunNotFound) {
			t.Fatalf("failed = %v, want ErrRunNotFound", err)
		}
	})

	t.Run("stores.run-for-session-expired", func(t *testing.T) {
		var now = time.Unix(0, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		if _, err := s.Start(owner, Run{SessionID: "s1", RunID: "run-1", Flow: "chat"}, LeaseTTL); err != nil {
			t.Fatalf("start: %v", err)
		}
		now = now.Add(2 * LeaseTTL)
		if _, err := s.RunForSession(ctx, "s1"); !errors.Is(err, ErrRunNotFound) {
			t.Fatalf("expired = %v, want ErrRunNotFound", err)
		}
	})
}

func TestRunActive(t *testing.T) {
	now := time.Unix(0, 0)
	live := Run{State: Running, Heartbeat: now}
	if !RunActive(live, now.Add(LeaseTTL-time.Second)) {
		t.Fatal("running run with a fresh heartbeat must be active")
	}
	if RunActive(live, now.Add(2*LeaseTTL)) {
		t.Fatal("running run past its lease must be inactive")
	}
	if RunActive(Run{State: Suspended, Heartbeat: now}, now) {
		t.Fatal("suspended run must be inactive")
	}
	if RunActive(Run{State: Finished, Heartbeat: now}, now) {
		t.Fatal("finished run must be inactive")
	}
}
