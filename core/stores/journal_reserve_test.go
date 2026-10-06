package stores

import (
	"context"
	"encoding/json"
	"errors"
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
				_, ok, err := j.Reserve(context.Background(), k, "create_booking", "fp", nil)
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
		e1, created, err := j.Reserve(context.Background(), k, "create_booking", "fp", nil)
		if err != nil || !created {
			t.Fatalf("first Reserve: created=%v err=%v", created, err)
		}
		e2, created, err := j.Reserve(context.Background(), k, "create_booking", "fp", nil)
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
		if _, _, err := j.Reserve(context.Background(), k, "create_booking", "fp", nil); err != nil {
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
		if _, _, err := j.Reserve(context.Background(), k, "create_booking", "fp", nil); err != nil {
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
		if _, _, err := j.Reserve(context.Background(), types.CallKey{SessionID: "s1", CallID: "c1"}, "create_booking", "fp", nil); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		entries, err := j.ByFingerprint(context.Background(), "s2", "fp")
		if err != nil || len(entries) != 0 {
			t.Fatalf("foreign session returned %d entries, err=%v", len(entries), err)
		}
	})

	t.Run("same key and fingerprint returns stored entry", func(t *testing.T) {
		j := NewMemoryJournal()
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		e1, _, err := j.Reserve(context.Background(), k, "create_booking", "fp", nil)
		if err != nil {
			t.Fatalf("first Reserve: %v", err)
		}
		e2, created, err := j.Reserve(context.Background(), k, "create_booking", "fp", nil)
		if err != nil || created {
			t.Fatalf("second Reserve: created=%v err=%v", created, err)
		}
		if e2.State != Reserved || e2.Fingerprint != e1.Fingerprint || e2.Key != e1.Key {
			t.Fatalf("second Reserve entry = %+v, want the stored entry", e2)
		}
	})

	t.Run("same key with different fingerprint is refused", func(t *testing.T) {
		j := NewMemoryJournal()
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		if _, _, err := j.Reserve(context.Background(), k, "create_booking", "fp", nil); err != nil {
			t.Fatalf("first Reserve: %v", err)
		}
		_, created, err := j.Reserve(context.Background(), k, "create_booking", "other", nil)
		if !errors.Is(err, ErrJournalFingerprintMismatch) {
			t.Fatalf("mismatched Reserve err = %v, want ErrJournalFingerprintMismatch", err)
		}
		if created {
			t.Fatal("mismatched Reserve created an entry")
		}
		// The refusal leaves the stored entry untouched.
		e, created, err := j.Reserve(context.Background(), k, "create_booking", "fp", nil)
		if err != nil || created {
			t.Fatalf("Reserve with original fingerprint: created=%v err=%v", created, err)
		}
		if e.Fingerprint != "fp" || e.State != Reserved {
			t.Fatalf("stored entry = %+v, want the original reserved entry", e)
		}
	})

	t.Run("complete without live reservation is refused", func(t *testing.T) {
		j := NewMemoryJournal()
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		if err := j.Complete(context.Background(), k, types.ToolResult{ID: "c1"}); !errors.Is(err, ErrJournalCompleteMissed) {
			t.Fatalf("Complete without Reserve err = %v, want ErrJournalCompleteMissed", err)
		}
	})

	t.Run("expired entry is replaced by store time", func(t *testing.T) {
		now := time.Unix(0, 0)
		j := NewMemoryJournal(WithMemoryJournalClock(func() time.Time { return now }), WithJournalTTL(time.Minute))
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		if _, created, err := j.Reserve(context.Background(), k, "create_booking", "fp", nil); err != nil || !created {
			t.Fatalf("first Reserve: created=%v err=%v", created, err)
		}
		now = now.Add(2 * time.Minute)
		_, created, err := j.Reserve(context.Background(), k, "create_booking", "fp", nil)
		if err != nil || !created {
			t.Fatalf("Reserve after expiry: created=%v err=%v", created, err)
		}
		entries, err := j.ByFingerprint(context.Background(), "s1", "fp")
		if err != nil || len(entries) != 1 {
			t.Fatalf("ByFingerprint after expiry: %d entries, err=%v", len(entries), err)
		}
	})
}

func TestJournalUnknown(t *testing.T) {
	j := NewMemoryJournal()
	ctx := context.Background()
	k := types.CallKey{SessionID: "s1", CallID: "c1"}
	if _, created, err := j.Reserve(ctx, k, "create_booking", "fp", json.RawMessage(`{"room":7}`)); err != nil || !created {
		t.Fatalf("Reserve: created=%v err=%v", created, err)
	}
	if err := j.Complete(ctx, k, types.ToolResult{Outcome: types.Unknown}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	done := types.CallKey{SessionID: "s1", CallID: "c2"}
	if _, created, err := j.Reserve(ctx, done, "send_fax", "fp2", nil); err != nil || !created {
		t.Fatalf("Reserve: created=%v err=%v", created, err)
	}
	if err := j.Complete(ctx, done, types.ToolResult{Outcome: types.Succeeded}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	entries, err := j.Unknown(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("Unknown: %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Tool != "create_booking" || e.Key != "c1" {
		t.Fatalf("entry = %+v, want tool create_booking and key c1", e)
	}
	if string(e.Args) != `{"room":7}` {
		t.Fatalf("Args = %s, want {\"room\":7}", e.Args)
	}
}
