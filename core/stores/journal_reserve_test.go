package stores

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestJournalReserve(t *testing.T) {
	t.Run("stores.concurrent-reserve", func(t *testing.T) {
		j := NewMemoryJournal()
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		const n = 10

		var (
			wg       sync.WaitGroup
			mu       sync.Mutex
			created  int
			firstErr error
		)
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, ok, err := j.Reserve(context.Background(), k, "fp")
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				if ok {
					mu.Lock()
					created++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		if firstErr != nil {
			t.Fatalf("Reserve: %v", firstErr)
		}
		if created != 1 {
			t.Fatalf("created = %d, want 1", created)
		}
	})

	t.Run("second reserve returns existing entry", func(t *testing.T) {
		j := NewMemoryJournal()
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		e1, created, err := j.Reserve(context.Background(), k, "fp")
		if err != nil || !created {
			t.Fatalf("first Reserve: created=%v err=%v", created, err)
		}
		e2, created, err := j.Reserve(context.Background(), k, "fp")
		if err != nil || created {
			t.Fatalf("second Reserve: created=%v err=%v", created, err)
		}
		if e2.State != Reserved || e2.Fingerprint != e1.Fingerprint || e2.Key != e1.Key {
			t.Fatalf("existing entry = %+v, want the reserved entry", e2)
		}
	})

	t.Run("complete records result and moves state", func(t *testing.T) {
		j := NewMemoryJournal()
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		if _, _, err := j.Reserve(context.Background(), k, "fp"); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		res := types.ToolResult{ID: "c1"}
		if err := j.Complete(context.Background(), k, res); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		entries, err := j.ByFingerprint(context.Background(), "s1", "fp")
		if err != nil || len(entries) != 1 {
			t.Fatalf("ByFingerprint: %d entries, err=%v", len(entries), err)
		}
		if entries[0].State != Completed {
			t.Fatalf("state = %v, want Completed", entries[0].State)
		}
	})

	t.Run("complete copies the result", func(t *testing.T) {
		j := NewMemoryJournal()
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		if _, _, err := j.Reserve(context.Background(), k, "fp"); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		res := types.ToolResult{ID: "c1", Error: &types.ToolError{Message: "boom"}}
		if err := j.Complete(context.Background(), k, res); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		res.Error.Message = "changed"
		entries, _ := j.ByFingerprint(context.Background(), "s1", "fp")
		if entries[0].Result.Error == nil || entries[0].Result.Error.Message != "boom" {
			t.Fatalf("stored result aliased the caller's ToolError: %+v", entries[0].Result)
		}
	})

	t.Run("byfingerprint scoped to session", func(t *testing.T) {
		j := NewMemoryJournal()
		if _, _, err := j.Reserve(context.Background(), types.CallKey{SessionID: "s1", CallID: "c1"}, "fp"); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		entries, err := j.ByFingerprint(context.Background(), "s2", "fp")
		if err != nil || len(entries) != 0 {
			t.Fatalf("foreign session returned %d entries, err=%v", len(entries), err)
		}
	})

	t.Run("expired entry is replaced by store time", func(t *testing.T) {
		now := time.Unix(0, 0)
		j := NewMemoryJournal(WithMemoryJournalClock(func() time.Time { return now }), WithJournalTTL(time.Minute))
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		if _, created, err := j.Reserve(context.Background(), k, "fp"); err != nil || !created {
			t.Fatalf("first Reserve: created=%v err=%v", created, err)
		}
		now = now.Add(2 * time.Minute)
		_, created, err := j.Reserve(context.Background(), k, "fp")
		if err != nil || !created {
			t.Fatalf("Reserve after expiry: created=%v err=%v", created, err)
		}
		entries, err := j.ByFingerprint(context.Background(), "s1", "fp")
		if err != nil || len(entries) != 1 {
			t.Fatalf("ByFingerprint after expiry: %d entries, err=%v", len(entries), err)
		}
	})
}
