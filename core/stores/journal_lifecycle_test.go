package stores

import (
	"context"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestJournalLifecycle(t *testing.T) {
	t.Run("stores.journal-ttl", func(t *testing.T) {
		now := time.Unix(1_800_000_000, 0).UTC()
		j := NewMemoryJournal(
			WithMemoryJournalClock(func() time.Time { return now }),
			WithJournalTTL(DefaultJournalTTL),
		)
		ctx := context.Background()
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		res := types.ToolResult{Outcome: types.Succeeded}

		if _, created, err := j.Reserve(ctx, k, "fp"); err != nil || !created {
			t.Fatalf("Reserve: created=%v err=%v", created, err)
		}
		if err := j.Complete(ctx, k, res); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		entries, err := j.ByFingerprint(ctx, "s1", "fp")
		if err != nil || len(entries) != 1 {
			t.Fatalf("before expiry: entries=%d err=%v", len(entries), err)
		}

		now = now.Add(25 * time.Hour)

		if entries, err = j.ByFingerprint(ctx, "s1", "fp"); err != nil {
			t.Fatalf("ByFingerprint: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("after 25h: entries = %+v, want purged", entries)
		}
		if _, created, err := j.Reserve(ctx, k, "fp"); err != nil || !created {
			t.Fatalf("Reserve after expiry: created=%v err=%v", created, err)
		}
	})

	t.Run("stores.replay-returns-recorded-result", func(t *testing.T) {
		now := time.Unix(1_800_000_000, 0).UTC()
		j := NewMemoryJournal(WithMemoryJournalClock(func() time.Time { return now }))
		ctx := context.Background()
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		res := types.ToolResult{Outcome: types.Succeeded}

		if _, created, err := j.Reserve(ctx, k, "fp"); err != nil || !created {
			t.Fatalf("Reserve: created=%v err=%v", created, err)
		}
		if err := j.Complete(ctx, k, res); err != nil {
			t.Fatalf("Complete: %v", err)
		}

		now = now.Add(time.Hour)

		entries, err := j.ByFingerprint(ctx, "s1", "fp")
		if err != nil {
			t.Fatalf("ByFingerprint: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("entries = %d, want 1", len(entries))
		}
		e := entries[0]
		if e.State != Completed {
			t.Fatalf("State = %v, want Completed", e.State)
		}
		if e.Result.Outcome != res.Outcome {
			t.Fatalf("Result.Outcome = %v, want %v", e.Result.Outcome, res.Outcome)
		}

		got, created, err := j.Reserve(ctx, k, "fp")
		if err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		if created {
			t.Fatal("Reserve reported created for a completed entry, want existing replayed")
		}
		if got.State != Completed || got.Result.Outcome != res.Outcome {
			t.Fatalf("replayed entry = %+v, want the recorded result unchanged", got)
		}
	})
}
