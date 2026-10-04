package stores

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func newTestRuns(t *testing.T, ttl time.Duration) (*MemoryRuns, *time.Time, context.Context) {
	t.Helper()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	s := NewMemoryRuns(
		WithMemoryRunClock(func() time.Time { return now }),
		WithMemoryRunInfo(func(context.Context) (types.RunInfo, bool) {
			return types.RunInfo{Principal: types.Principal{Tenant: "t1"}}, true
		}),
	)
	return s, &now, context.Background()
}

func startRun(s *MemoryRuns, ctx context.Context, sessionID, opID string) (Lease, error) {
	return s.Start(ctx, Run{
		SessionID:   sessionID,
		RunID:       "run-" + sessionID,
		OperationID: opID,
		Mode:        types.Primary,
	}, 30*time.Second)
}

func runState(t *testing.T, s *MemoryRuns, ctx context.Context, opID string) Run {
	t.Helper()
	r, err := s.ByOperation(ctx, "t1", opID)
	if err != nil {
		t.Fatalf("ByOperation: %v", err)
	}
	return r
}

func TestRunsState(t *testing.T) {
	t.Run("start-mints-lease-and-second-start-refused", func(t *testing.T) {
		s, now, ctx := newTestRuns(t, 30*time.Second)
		l, err := startRun(s, ctx, "s1", "op1")
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if l.RunID != "run-s1" || !l.Expires.Equal(now.Add(30*time.Second)) {
			t.Fatalf("lease = %+v, want run-s1 expiring at store now+30s", l)
		}
		r := runState(t, s, ctx, "op1")
		if r.State != Running || !r.StartedAt.Equal(*now) || !r.Heartbeat.Equal(*now) {
			t.Fatalf("run = %+v, want Running started at store time", r)
		}
		if _, err := startRun(s, ctx, "s1", "op2"); !errors.Is(err, types.ErrRunActive) {
			t.Fatalf("second Start err = %v, want ErrRunActive", err)
		}
		after := runState(t, s, ctx, "op1")
		if after.State != r.State || !after.StartedAt.Equal(r.StartedAt) || !after.Heartbeat.Equal(r.Heartbeat) {
			t.Fatalf("run changed by refused Start: %+v -> %+v", r, after)
		}
	})

	// The recovery scenario's THEN names Invoke (row 23.1) and the model
	// port (18.6), which do not exist yet. Here: a second Start for a
	// session whose run is still leased refuses immediately and nothing
	// changes.
	t.Run("recovery.no-double-run", func(t *testing.T) {
		s, _, ctx := newTestRuns(t, 30*time.Second)
		if _, err := startRun(s, ctx, "s1", "op1"); err != nil {
			t.Fatalf("Start: %v", err)
		}
		before := runState(t, s, ctx, "op1")
		_, err := startRun(s, ctx, "s1", "op1")
		if !errors.Is(err, types.ErrRunActive) {
			t.Fatalf("second Start err = %v, want ErrRunActive", err)
		}
		if after := runState(t, s, ctx, "op1"); after.State != before.State || !after.StartedAt.Equal(before.StartedAt) {
			t.Fatalf("state changed on refused Start: %+v -> %+v", before, after)
		}
		if s.SessionLeaseActive(ctx, "s1") != true {
			t.Fatal("session lease reported inactive while run holds one")
		}
	})

	t.Run("duplicate-operation-records-existing-run", func(t *testing.T) {
		s, _, ctx := newTestRuns(t, 30*time.Second)
		if _, err := startRun(s, ctx, "s1", "op1"); err != nil {
			t.Fatalf("Start: %v", err)
		}
		_, err := startRun(s, ctx, "s2", "op1")
		var dup OperationExistsError
		if !errors.As(err, &dup) || dup.RunID != "run-s1" {
			t.Fatalf("err = %v, want OperationExistsError{run-s1}", err)
		}
		if !errors.Is(err, types.ErrOperationExists) {
			t.Fatalf("err = %v, want errors.Is types.ErrOperationExists", err)
		}
		if r := runState(t, s, ctx, "op1"); r.SessionID != "s1" {
			t.Fatalf("ByOperation returned session %q, want s1", r.SessionID)
		}
		if _, err := s.ByOperation(ctx, "t1", "missing"); !errors.Is(err, ErrRunNotFound) {
			t.Fatalf("ByOperation err = %v, want ErrRunNotFound", err)
		}
	})

	t.Run("finish-closes-run-and-releases-lease", func(t *testing.T) {
		s, _, ctx := newTestRuns(t, 30*time.Second)
		l, _ := startRun(s, ctx, "s1", "op1")
		if err := s.Finish(ctx, l, Finished, nil, "ref-1"); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		r := runState(t, s, ctx, "op1")
		if r.State != Finished || r.ResultRef != "ref-1" {
			t.Fatalf("run = %+v, want Finished with ref-1", r)
		}
		if s.SessionLeaseActive(ctx, "s1") {
			t.Fatal("lease still active after Finish")
		}
		if _, err := s.Start(ctx, Run{SessionID: "s1", RunID: "run-s1b", OperationID: "op2", Mode: types.Primary}, 30*time.Second); err != nil {
			t.Fatalf("Start after Finish: %v", err)
		}
		if err := s.Finish(ctx, l, Failed, nil, ""); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("reused lease Finish err = %v, want ErrRunNotActive", err)
		}
	})

	t.Run("expired-lease-frees-session", func(t *testing.T) {
		s, now, ctx := newTestRuns(t, 30*time.Second)
		l, _ := startRun(s, ctx, "s1", "op1")
		*now = now.Add(31 * time.Second)
		if err := s.Finish(ctx, l, Failed, nil, ""); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Finish with expired lease err = %v, want ErrRunNotActive", err)
		}
		if s.SessionLeaseActive(ctx, "s1") {
			t.Fatal("session lease active after expiry")
		}
		if _, err := startRun(s, ctx, "s1", "op2"); err != nil {
			t.Fatalf("Start after expiry: %v", err)
		}
	})

	t.Run("suspend-resuming-transitions", func(t *testing.T) {
		s, now, ctx := newTestRuns(t, 30*time.Second)
		l, _ := startRun(s, ctx, "s1", "op1")
		tok := types.ResumeToken("tok-1")
		if err := s.Suspend(ctx, l, tok); err != nil {
			t.Fatalf("Suspend: %v", err)
		}
		if r := runState(t, s, ctx, "op1"); r.State != Suspended {
			t.Fatalf("state = %v, want Suspended", r.State)
		}
		if s.SessionLeaseActive(ctx, "s1") {
			t.Fatal("suspended run holds a lease")
		}
		rl, err := s.Resuming(ctx, "run-s1", 15*time.Second)
		if err != nil {
			t.Fatalf("Resuming: %v", err)
		}
		if !rl.Expires.Equal(now.Add(15 * time.Second)) {
			t.Fatalf("lease expires %v, want store now+15s", rl.Expires)
		}
		if r := runState(t, s, ctx, "op1"); r.State != Resuming {
			t.Fatalf("state = %v, want Resuming", r.State)
		}
		if _, err := s.Resuming(ctx, "run-s1", 15*time.Second); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("Resuming non-suspended err = %v, want ErrRunNotActive", err)
		}
		if _, err := s.Resuming(ctx, "run-nope", time.Second); !errors.Is(err, ErrRunNotFound) {
			t.Fatalf("Resuming unknown err = %v, want ErrRunNotFound", err)
		}
	})

	t.Run("stale-lists-running-and-resuming-by-store-clock", func(t *testing.T) {
		s, now, ctx := newTestRuns(t, time.Minute)
		l1, _ := startRun(s, ctx, "s1", "op1")
		l2, err := startRun(s, ctx, "s2", "op2")
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := s.Suspend(ctx, l2, "tok-2"); err != nil {
			t.Fatalf("Suspend: %v", err)
		}
		if _, err := s.Resuming(ctx, "run-s2", time.Minute); err != nil {
			t.Fatalf("Resuming: %v", err)
		}
		*now = now.Add(time.Second)
		if _, err := startRun(s, ctx, "s3", "op3"); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := s.Finish(ctx, l1, Finished, nil, ""); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		*now = now.Add(2 * time.Minute)
		stale, err := s.Stale(ctx, time.Minute, 10)
		if err != nil {
			t.Fatalf("Stale: %v", err)
		}
		if len(stale) != 2 || stale[0].RunID != "run-s2" || stale[1].RunID != "run-s3" {
			t.Fatalf("stale = %+v, want [run-s2 run-s3] in heartbeat order", stale)
		}
		few, err := s.Stale(ctx, time.Minute, 1)
		if err != nil || len(few) != 1 || few[0].RunID != "run-s2" {
			t.Fatalf("limited stale = %+v err=%v, want [run-s2]", few, err)
		}
	})
}
