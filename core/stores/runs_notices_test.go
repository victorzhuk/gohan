package stores

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestRunsNotices(t *testing.T) {
	base := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	clock := base

	newStore := func(t *testing.T) *MemoryRuns {
		t.Helper()
		return NewMemoryRuns(
			WithMemoryRunClock(func() time.Time { return clock }),
			WithMemoryRunInfo(func(context.Context) (types.RunInfo, bool) {
				return types.RunInfo{Principal: types.Principal{Tenant: "acme"}}, true
			}),
		)
	}
	start := func(t *testing.T, s *MemoryRuns) Lease {
		t.Helper()
		lease, err := s.Start(context.Background(), Run{
			SessionID:   "s-1",
			RunID:       "r-1",
			Flow:        "booking",
			Backend:     "openai",
			OperationID: "op-1",
		}, time.Minute)
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		return lease
	}

	t.Run("stores.notice-written-with-finish", func(t *testing.T) {
		s := newStore(t)
		lease := start(t, s)
		if err := s.Finish(context.Background(), lease, Finished, nil, "res://out"); err != nil {
			t.Fatalf("Finish() error = %v", err)
		}

		claimed, err := s.Notices(context.Background(), 10)
		if err != nil {
			t.Fatalf("Notices() error = %v", err)
		}
		if len(claimed) != 1 {
			t.Fatalf("Notices() returned %d notices, want 1", len(claimed))
		}
		n := claimed[0]
		if n.Kind != types.NoticeFinished || n.Tenant != "acme" ||
			n.SessionID != "s-1" || n.RunID != "r-1" || n.Reason != string(types.StopCompleted) || !n.At.Equal(base) {
			t.Fatalf("claimed notice = %+v", n)
		}

		clock = base.Add(noticeClaimTTL / 2)
		if again, err := s.Notices(context.Background(), 10); err != nil || len(again) != 0 {
			t.Fatalf("Notices() during claim = %v, %d notices, want none", err, len(again))
		}

		clock = base.Add(noticeClaimTTL)
		redelivered, err := s.Notices(context.Background(), 10)
		if err != nil {
			t.Fatalf("Notices() error = %v", err)
		}
		if len(redelivered) != 1 || redelivered[0].ID != n.ID {
			t.Fatalf("redelivery = %+v, want same notice id %q", redelivered, n.ID)
		}

		if err := s.AckNotice(context.Background(), n.ID); err != nil {
			t.Fatalf("AckNotice() error = %v", err)
		}
		clock = clock.Add(noticeClaimTTL)
		if rest, err := s.Notices(context.Background(), 10); err != nil || len(rest) != 0 {
			t.Fatalf("Notices() after ack = %v, %d notices, want none", err, len(rest))
		}
	})

	t.Run("streams.notice-thin-no-content", func(t *testing.T) {
		s := newStore(t)
		lease := start(t, s)
		if err := s.Suspend(context.Background(), lease, "cp_1"); err != nil {
			t.Fatalf("Suspend() error = %v", err)
		}
		claimed, err := s.Notices(context.Background(), 10)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("Notices() = %v, %d notices, want 1", err, len(claimed))
		}
		if claimed[0].Kind != types.NoticeSuspended {
			t.Fatalf("notice kind = %v, want suspended", claimed[0].Kind)
		}

		raw, err := json.Marshal(claimed[0])
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		want := []string{"at", "id", "kind", "reason", "run_id", "session_id", "tenant"}
		keys := make([]string, 0, len(body))
		for k := range body {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, want) {
			t.Fatalf("notice body keys = %v, want %v (raw: %s)", keys, want, raw)
		}
		if body["kind"] != "suspended" || body["tenant"] != "acme" ||
			body["session_id"] != "s-1" || body["run_id"] != "r-1" {
			t.Fatalf("notice body = %s", raw)
		}
	})
}

func TestNoticeReason(t *testing.T) {
	newStore := func(t *testing.T) *MemoryRuns {
		t.Helper()
		return NewMemoryRuns(
			WithMemoryRunClock(time.Now),
			WithMemoryRunInfo(func(context.Context) (types.RunInfo, bool) {
				return types.RunInfo{Principal: types.Principal{Tenant: "acme"}}, true
			}),
		)
	}
	start := func(t *testing.T, s *MemoryRuns) Lease {
		t.Helper()
		lease, err := s.Start(context.Background(), Run{SessionID: "s-1", RunID: "r-1"}, time.Minute)
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		return lease
	}
	claim := func(t *testing.T, s *MemoryRuns) types.RunNotice {
		t.Helper()
		claimed, err := s.Notices(context.Background(), 10)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("Notices() = %v, %d notices, want 1", err, len(claimed))
		}
		return claimed[0]
	}

	t.Run("suspension carries the suspend reason name", func(t *testing.T) {
		s := newStore(t)
		lease := start(t, s)
		if err := s.SuspendWithReason(context.Background(), lease, "cp_1", types.HumanApproval); err != nil {
			t.Fatalf("SuspendWithReason() error = %v", err)
		}
		n := claim(t, s)
		if n.Kind != types.NoticeSuspended || n.Reason != "HumanApproval" {
			t.Fatalf("notice = %+v, want suspended/HumanApproval", n)
		}
	})

	t.Run("suspension without a reason stays suspended", func(t *testing.T) {
		s := newStore(t)
		lease := start(t, s)
		if err := s.Suspend(context.Background(), lease, "cp_1"); err != nil {
			t.Fatalf("Suspend() error = %v", err)
		}
		if n := claim(t, s); n.Reason != string(types.StopSuspended) {
			t.Fatalf("reason = %q, want %q", n.Reason, types.StopSuspended)
		}
	})

	t.Run("cancel finishes as a cancelled notice", func(t *testing.T) {
		s := newStore(t)
		lease := start(t, s)
		if err := s.FinishWithReason(context.Background(), lease, Finished, nil, "", types.StopCancelled); err != nil {
			t.Fatalf("FinishWithReason() error = %v", err)
		}
		n := claim(t, s)
		if n.Kind != types.NoticeCancelled || n.Reason != string(types.StopCancelled) {
			t.Fatalf("notice = %+v, want cancelled/%s", n, types.StopCancelled)
		}
	})
}
