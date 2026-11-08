package stores

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestSessionExclusivity(t *testing.T) {
	t.Parallel()

	t.Run("stores.resume-session-exclusive", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1700000000, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		ctx := context.Background()

		l1, err := s.Start(ctx, Run{RunID: "run-a", SessionID: "s1"}, time.Minute)
		if err != nil {
			t.Fatalf("start run-a: %v", err)
		}
		if err := s.Suspend(ctx, l1, types.ResumeToken("tok-a")); err != nil {
			t.Fatalf("suspend run-a: %v", err)
		}

		l2, err := s.Start(ctx, Run{RunID: "run-b", SessionID: "s1"}, time.Minute)
		if err != nil {
			t.Fatalf("start run-b over suspended run-a: %v", err)
		}
		before, err := s.ByID(ctx, "run-b")
		if err != nil {
			t.Fatalf("read run-b: %v", err)
		}

		if _, err := s.Resuming(ctx, "run-a", time.Minute); !errors.Is(err, types.ErrRunActive) {
			t.Fatalf("Resuming over live owner err = %v, want ErrRunActive", err)
		}
		a, err := s.ByID(ctx, "run-a")
		if err != nil {
			t.Fatalf("read run-a: %v", err)
		}
		if a.State != Suspended {
			t.Fatalf("run-a state = %v, want untouched Suspended", a.State)
		}
		after, err := s.ByID(ctx, "run-b")
		if err != nil {
			t.Fatalf("read run-b: %v", err)
		}
		if !after.Heartbeat.Equal(before.Heartbeat) || after.State != Running {
			t.Fatalf("run-b mutated by refused resume: %+v", after)
		}

		if _, err := s.Heartbeat(ctx, l2); err != nil {
			t.Fatalf("newer owner lease unusable after refusal: %v", err)
		}
	})

	t.Run("stores.reclaim-session-exclusive", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1700000000, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		ctx := context.Background()

		if _, err := s.Start(ctx, Run{RunID: "run-a", SessionID: "s1"}, time.Second); err != nil {
			t.Fatalf("start run-a: %v", err)
		}
		now = now.Add(2 * time.Second)

		l2, err := s.Start(ctx, Run{RunID: "run-b", SessionID: "s1"}, time.Minute)
		if err != nil {
			t.Fatalf("start run-b over expired run-a: %v", err)
		}
		stale, err := s.ByID(ctx, "run-a")
		if err != nil {
			t.Fatalf("read run-a: %v", err)
		}

		if _, err := s.Reclaim(ctx, stale, time.Minute); !errors.Is(err, types.ErrRunActive) {
			t.Fatalf("Reclaim over live owner err = %v, want ErrRunActive", err)
		}
		a, err := s.ByID(ctx, "run-a")
		if err != nil {
			t.Fatalf("read run-a: %v", err)
		}
		if a.State != Running || !a.Heartbeat.Equal(stale.Heartbeat) {
			t.Fatalf("run-a mutated by refused reclaim: %+v", a)
		}
		if _, err := s.Heartbeat(ctx, l2); err != nil {
			t.Fatalf("newer owner lease unusable after refusal: %v", err)
		}
	})

	t.Run("stores.reclaim-state-refused", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1700000000, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		ctx := context.Background()

		l1, err := s.Start(ctx, Run{RunID: "run-a", SessionID: "s1"}, time.Minute)
		if err != nil {
			t.Fatalf("start run-a: %v", err)
		}
		if err := s.Suspend(ctx, l1, types.ResumeToken("tok-a")); err != nil {
			t.Fatalf("suspend run-a: %v", err)
		}
		l2, err := s.Start(ctx, Run{RunID: "run-c", SessionID: "s2"}, time.Minute)
		if err != nil {
			t.Fatalf("start run-c: %v", err)
		}
		if err := s.Finish(ctx, l2, Finished, nil, ""); err != nil {
			t.Fatalf("finish run-c: %v", err)
		}
		now = now.Add(2 * time.Minute)

		suspended, err := s.ByID(ctx, "run-a")
		if err != nil {
			t.Fatalf("read run-a: %v", err)
		}
		if _, err := s.Reclaim(ctx, suspended, time.Minute); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Reclaim of suspended run err = %v, want ErrRunNotActive", err)
		}
		finished, err := s.ByID(ctx, "run-c")
		if err != nil {
			t.Fatalf("read run-c: %v", err)
		}
		if _, err := s.Reclaim(ctx, finished, time.Minute); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Reclaim of finished run err = %v, want ErrRunNotActive", err)
		}
	})
}

func TestCheckpointResumePort(t *testing.T) {
	t.Parallel()

	t.Run("stores.optional-checkpoint-resume-port", func(t *testing.T) {
		t.Parallel()
		if _, ok := any(NewMemoryRuns()).(CheckpointRuns); !ok {
			t.Fatal("memory runs store does not implement CheckpointRuns")
		}
		if _, ok := any(NewMemoryRuns()).(Runs); !ok {
			t.Fatal("memory runs store lost the frozen run port")
		}

		now := time.Unix(1700000000, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		ctx := context.Background()
		l, err := s.Start(ctx, Run{RunID: "run-p", SessionID: "s1"}, time.Minute)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		tok := types.ResumeToken("tok-p")
		if err := s.Suspend(ctx, l, tok); err != nil {
			t.Fatalf("suspend: %v", err)
		}
		got, err := s.SuspensionToken(ctx, "run-p")
		if err != nil || got != tok {
			t.Fatalf("SuspensionToken = %s, %v; want %s, nil", got, err, tok)
		}
	})

	t.Run("stores.resume-current-token-fenced", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1700000000, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		ctx := context.Background()

		l, err := s.Start(ctx, Run{RunID: "run-f", SessionID: "s1"}, time.Minute)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		current := types.ResumeToken("tok-new")
		if err := s.Suspend(ctx, l, current); err != nil {
			t.Fatalf("suspend: %v", err)
		}

		if _, err := s.ResumingCheckpoint(ctx, "run-f", "tok-old", time.Minute); !errors.Is(err, types.ErrTokenMismatch) {
			t.Fatalf("ResumingCheckpoint with stale token err = %v, want ErrTokenMismatch", err)
		}
		got, err := s.SuspensionToken(ctx, "run-f")
		if err != nil || got != current {
			t.Fatalf("SuspensionToken after mismatch = %s, %v; want %s untouched", got, err, current)
		}
		a, err := s.ByID(ctx, "run-f")
		if err != nil {
			t.Fatalf("read run-f: %v", err)
		}
		if a.State != Suspended {
			t.Fatalf("run-f state = %v after mismatch, want Suspended", a.State)
		}

		l2, err := s.ResumingCheckpoint(ctx, "run-f", current, time.Minute)
		if err != nil {
			t.Fatalf("ResumingCheckpoint with current token: %v", err)
		}
		if l2.RunID != "run-f" || l2.Generation == 0 {
			t.Fatalf("lease = %+v, want minted generation for run-f", l2)
		}
	})
}

func TestRecoveryDiscovery(t *testing.T) {
	t.Parallel()

	t.Run("stores.resume-ready-filter-before-limit", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1700000000, 0)
		clock := func() time.Time { return now }
		runs := NewMemoryRuns(WithMemoryRunClock(clock))
		cps := NewMemoryCheckpoints(WithMemoryCheckpointClock(clock), WithMemoryCheckpointRuns(runs))
		ctx := context.Background()

		l1, err := runs.Start(ctx, Run{RunID: "run-a", SessionID: "s1"}, time.Minute)
		if err != nil {
			t.Fatalf("start run-a: %v", err)
		}
		t1, err := cps.Put(ctx, Checkpoint{RunID: "run-a", SessionID: "s1"})
		if err != nil {
			t.Fatalf("put run-a checkpoint: %v", err)
		}
		if err := runs.Suspend(ctx, l1, t1); err != nil {
			t.Fatalf("suspend run-a: %v", err)
		}
		if _, err := cps.Consume(ctx, t1, ResumeInput{}); err != nil {
			t.Fatalf("consume run-a checkpoint: %v", err)
		}
		lr, err := runs.ResumingCheckpoint(ctx, "run-a", t1, time.Minute)
		if err != nil {
			t.Fatalf("resume run-a: %v", err)
		}
		if err := runs.Finish(ctx, lr, Finished, nil, ""); err != nil {
			t.Fatalf("finish run-a: %v", err)
		}

		l2, err := runs.Start(ctx, Run{RunID: "run-b", SessionID: "s2"}, time.Minute)
		if err != nil {
			t.Fatalf("start run-b: %v", err)
		}
		t2, err := cps.Put(ctx, Checkpoint{RunID: "run-b", SessionID: "s2"})
		if err != nil {
			t.Fatalf("put run-b checkpoint: %v", err)
		}
		if err := runs.Suspend(ctx, l2, t2); err != nil {
			t.Fatalf("suspend run-b: %v", err)
		}
		if _, err := cps.Consume(ctx, t2, ResumeInput{}); err != nil {
			t.Fatalf("consume run-b checkpoint: %v", err)
		}

		got, err := cps.ResumeReady(ctx, 1)
		if err != nil {
			t.Fatalf("ResumeReady: %v", err)
		}
		if len(got) != 1 || got[0].RunID != "run-b" {
			t.Fatalf("ResumeReady = %+v, want only run-b's checkpoint", got)
		}

		unbound := NewMemoryCheckpoints(WithMemoryCheckpointClock(clock))
		if _, err := unbound.ResumeReady(ctx, 1); !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("unbound discovery err = %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("stores.preempted-filter-before-limit", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1700000000, 0)
		s := NewMemoryRuns(WithMemoryRunClock(func() time.Time { return now }))
		ctx := context.Background()

		l1, err := s.Start(ctx, Run{RunID: "run-h", SessionID: "s1"}, time.Minute)
		if err != nil {
			t.Fatalf("start run-h: %v", err)
		}
		if err := s.SuspendWithReason(ctx, l1, types.ResumeToken("tok-h"), types.HumanApproval); err != nil {
			t.Fatalf("suspend run-h: %v", err)
		}
		l2, err := s.Start(ctx, Run{RunID: "run-p", SessionID: "s2"}, time.Minute)
		if err != nil {
			t.Fatalf("start run-p: %v", err)
		}
		if err := s.SuspendWithReason(ctx, l2, types.ResumeToken("tok-p"), types.Preempted); err != nil {
			t.Fatalf("suspend run-p: %v", err)
		}

		got, err := s.Preempted(ctx, 1)
		if err != nil {
			t.Fatalf("Preempted: %v", err)
		}
		if len(got) != 1 || got[0].RunID != "run-p" {
			t.Fatalf("Preempted = %+v, want only the preempted run under limit one", got)
		}
		if got[0].State != Suspended {
			t.Fatalf("preempted run state = %v, want Suspended", got[0].State)
		}
	})
}

func TestCheckpointLifetimePolicy(t *testing.T) {
	t.Parallel()

	t.Run("stores.checkpoint-lifetime-policy", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1700000000, 0)
		clock := func() time.Time { return now }
		ctx := context.Background()

		explicit := NewMemoryCheckpoints(WithMemoryCheckpointClock(clock))
		exp := now.Add(time.Minute)
		t1, err := explicit.Put(ctx, Checkpoint{ExpiresAt: exp})
		if err != nil {
			t.Fatalf("put explicit expiry: %v", err)
		}
		cp1, err := explicit.Peek(ctx, t1)
		if err != nil {
			t.Fatalf("peek explicit expiry: %v", err)
		}
		if !cp1.ExpiresAt.Equal(exp) {
			t.Fatalf("explicit expiry = %v, want %v", cp1.ExpiresAt, exp)
		}

		timed := NewMemoryCheckpoints(WithMemoryCheckpointClock(clock), WithMemoryCheckpointTTL(5*time.Minute))
		t2, err := timed.Put(ctx, Checkpoint{})
		if err != nil {
			t.Fatalf("put with ttl: %v", err)
		}
		cp2, err := timed.Peek(ctx, t2)
		if err != nil {
			t.Fatalf("peek with ttl: %v", err)
		}
		if want := now.Add(5 * time.Minute); !cp2.ExpiresAt.Equal(want) {
			t.Fatalf("ttl expiry = %v, want store clock %v", cp2.ExpiresAt, want)
		}

		plain := NewMemoryCheckpoints(WithMemoryCheckpointClock(clock))
		t3, err := plain.Put(ctx, Checkpoint{})
		if err != nil {
			t.Fatalf("put without policy: %v", err)
		}
		cp3, err := plain.Peek(ctx, t3)
		if err != nil {
			t.Fatalf("peek without policy: %v", err)
		}
		if !cp3.ExpiresAt.IsZero() {
			t.Fatalf("expiry without policy = %v, want unset", cp3.ExpiresAt)
		}

		negative := NewMemoryCheckpoints(WithMemoryCheckpointClock(clock), WithMemoryCheckpointTTL(-time.Second))
		if _, err := negative.Put(ctx, Checkpoint{}); err == nil {
			t.Fatal("negative ttl accepted, want refusal before any write")
		}
	})
}
