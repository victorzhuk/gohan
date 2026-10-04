package stores

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestCheckpointConsume(t *testing.T) {
	t.Parallel()

	seed := func(t *testing.T, s *MemoryCheckpoints) types.ResumeToken {
		t.Helper()
		id, err := s.Put(t.Context(), Checkpoint{
			SessionID: "s-1",
			Flow:      "booking",
			Reason:    types.HumanApproval,
			Originator: types.Principal{
				Subject: "user-1",
				Tenant:  "acme",
				Scopes:  []string{"session:write"},
			},
			Data:      []byte("state"),
			ExpiresAt: time.Now().Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		return id
	}

	t.Run("stores.concurrent-consume", func(t *testing.T) {
		t.Parallel()
		s := NewMemoryCheckpoints()
		id := seed(t, s)

		const callers = 10
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, callers)
		for i := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = s.Consume(t.Context(), id, ResumeInput{Verdict: VerdictApprove})
			}()
		}
		close(start)
		wg.Wait()

		winners := 0
		for _, err := range errs {
			switch {
			case err == nil:
				winners++
			case !errors.Is(err, types.ErrTokenConsumed):
				t.Fatalf("loser error: %v", err)
			}
		}
		if winners != 1 {
			t.Fatalf("winners = %d, want 1", winners)
		}
	})

	t.Run("suspension.token-reuse", func(t *testing.T) {
		t.Parallel()
		s := NewMemoryCheckpoints()
		id := seed(t, s)

		if _, err := s.Consume(t.Context(), id, ResumeInput{Verdict: VerdictApprove}); err != nil {
			t.Fatalf("first Consume: %v", err)
		}
		_, err := s.Consume(t.Context(), id, ResumeInput{Verdict: VerdictApprove})
		if !errors.Is(err, types.ErrTokenConsumed) {
			t.Fatalf("second Consume error = %v, want ErrTokenConsumed", err)
		}
		// The decision is recorded once: the reused token reaches nothing.
		if _, _, err := s.PendingInput(t.Context(), "missing"); err == nil {
			t.Fatal("PendingInput for an unindexed run returned no error")
		}
	})

	t.Run("expired", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1_000_000, 0)
		s := NewMemoryCheckpoints(WithMemoryCheckpointClock(func() time.Time { return now }))

		now = now.Add(2 * time.Hour)
		id, err := s.Put(t.Context(), Checkpoint{
			SessionID:  "s-1",
			Reason:     types.HumanApproval,
			Originator: types.Principal{Subject: "user-1", Tenant: "acme"},
			ExpiresAt:  now.Add(-time.Hour),
		})
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		// The caller's clock is behind the store's; the store's decides.
		_, err = s.Consume(t.Context(), id, ResumeInput{Verdict: VerdictApprove})
		if !errors.Is(err, types.ErrTokenExpired) {
			t.Fatalf("expired Consume error = %v, want ErrTokenExpired", err)
		}
	})

	t.Run("pending-input", func(t *testing.T) {
		t.Parallel()
		p := types.Principal{Subject: "user-1", Tenant: "acme"}
		s := NewMemoryCheckpoints(WithMemoryCheckpointRunInfo(func(context.Context) (types.RunInfo, bool) {
			return types.RunInfo{RunID: "run-9"}, true
		}))
		id, err := s.Put(t.Context(), Checkpoint{
			SessionID:  "s-1",
			Reason:     types.HumanApproval,
			Originator: p,
			ExpiresAt:  time.Now().Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("Put: %v", err)
		}

		if _, _, err := s.PendingInput(t.Context(), "run-9"); err != nil {
			t.Fatalf("PendingInput before Consume: %v", err)
		}
		in := ResumeInput{Verdict: VerdictEdit, Approver: &p}
		if _, err := s.Consume(t.Context(), id, in); err != nil {
			t.Fatalf("Consume: %v", err)
		}
		_, got, err := s.PendingInput(t.Context(), "run-9")
		if err != nil {
			t.Fatalf("PendingInput after Consume: %v", err)
		}
		if got.Verdict != VerdictEdit || got.Approver != &p {
			t.Fatalf("PendingInput input = %+v, want the consumed input", got)
		}
		if _, _, err := s.PendingInput(t.Context(), "run-other"); err == nil {
			t.Fatal("PendingInput for an unknown run returned no error")
		}
	})
}
