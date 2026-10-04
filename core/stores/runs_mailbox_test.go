package stores

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestRunsMailbox(t *testing.T) {
	t.Run("stores.signal-cancel-cross-pod", func(t *testing.T) {
		now := time.Unix(1700000000, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		ctx := context.Background()

		l, err := s.Start(ctx, Run{RunID: "run-m1", SessionID: "s1"}, time.Minute)
		if err != nil {
			t.Fatalf("Start err = %v", err)
		}

		// Pod A addresses the run by id only; pod B holds the lease.
		if err := s.Signal(ctx, "run-m1", Signal{Kind: SignalCancel}); err != nil {
			t.Fatalf("Signal cancel err = %v", err)
		}
		got, err := s.Drain(ctx, l)
		if err != nil {
			t.Fatalf("Drain err = %v", err)
		}
		if len(got) != 1 || got[0].Kind != SignalCancel {
			t.Fatalf("Drain = %+v, want one SignalCancel", got)
		}
		if !got[0].At.Equal(now) {
			t.Fatalf("signal At = %v, want store time %v", got[0].At, now)
		}

		// Drain removes; a second drain is empty.
		if got, err := s.Drain(ctx, l); err != nil || len(got) != 0 {
			t.Fatalf("second Drain = %+v, err = %v, want empty", got, err)
		}

		// A non-running run rejects signals; the holder-only rule rejects
		// a drain without a live lease.
		if err := s.Signal(ctx, "run-missing", Signal{Kind: SignalCancel}); !errors.Is(err, ErrRunNotFound) {
			t.Fatalf("Signal unknown run err = %v, want ErrRunNotFound", err)
		}
		if err := s.Finish(ctx, l, Finished, nil, ""); err != nil {
			t.Fatalf("Finish err = %v", err)
		}
		if err := s.Signal(ctx, "run-m1", Signal{Kind: SignalCancel}); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Signal finished run err = %v, want ErrRunNotActive", err)
		}
		if _, err := s.Drain(ctx, l); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Drain with closed lease err = %v, want ErrRunNotActive", err)
		}
	})

	t.Run("stores.mailbox-full", func(t *testing.T) {
		now := time.Unix(1700000000, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		ctx := context.Background()

		l, err := s.Start(ctx, Run{RunID: "run-m2", SessionID: "s2"}, time.Minute)
		if err != nil {
			t.Fatalf("Start err = %v", err)
		}

		for i := range MaxPendingSignals {
			msg := types.Message{}
			if err := s.Signal(ctx, "run-m2", Signal{Kind: SignalSteer, Message: msg}); err != nil {
				t.Fatalf("Signal %d err = %v", i, err)
			}
		}
		if err := s.Signal(ctx, "run-m2", Signal{Kind: SignalSteer}); !errors.Is(err, types.ErrMailboxFull) {
			t.Fatalf("eleventh Signal err = %v, want ErrMailboxFull", err)
		}

		got, err := s.Drain(ctx, l)
		if err != nil {
			t.Fatalf("Drain err = %v", err)
		}
		if len(got) != MaxPendingSignals {
			t.Fatalf("Drain len = %d, want %d", len(got), MaxPendingSignals)
		}
		for i, sig := range got {
			if sig.Kind != SignalSteer {
				t.Fatalf("signal %d kind = %v, want SignalSteer", i, sig.Kind)
			}
		}

		// A pending steer blocks Finish, a pending cancel does not; after
		// the drain Finish closes the run.
		if err := s.Signal(ctx, "run-m2", Signal{Kind: SignalCancel}); err != nil {
			t.Fatalf("Signal cancel err = %v", err)
		}
		if err := s.Signal(ctx, "run-m2", Signal{Kind: SignalSteer}); err != nil {
			t.Fatalf("Signal steer err = %v", err)
		}
		if err := s.Finish(ctx, l, Finished, nil, ""); !errors.Is(err, types.ErrSignalsPending) {
			t.Fatalf("Finish with steer pending err = %v, want ErrSignalsPending", err)
		}
		if _, err := s.Drain(ctx, l); err != nil || len(got) == 0 {
			t.Fatalf("Drain err = %v, n = %d", err, len(got))
		}
		if err := s.Signal(ctx, "run-m2", Signal{Kind: SignalCancel}); err != nil {
			t.Fatalf("Signal cancel err = %v", err)
		}
		if err := s.Finish(ctx, l, Finished, nil, ""); err != nil {
			t.Fatalf("Finish with cancel pending err = %v, want nil", err)
		}
	})
}
