// Package storetest holds conformance suites for store ports. Each suite
// drives an implementation through an injected factory and asserts the
// port contract, not any one implementation. The suite never imports a
// concrete store; implementations are bound from external tests.
package storetest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// JournalEntryState mirrors the port's entry states without naming a
// particular implementation's type.
type JournalEntryState int

const (
	JournalReserved JournalEntryState = iota
	JournalCompleted
)

// JournalEntry is the port-level shape of a journal record.
type JournalEntry struct {
	State       JournalEntryState
	Fingerprint string
	Key         string
	Result      types.ToolResult
}

// JournalStore is the Journal port as the conformance suite sees it.
type JournalStore interface {
	Reserve(ctx context.Context, k types.CallKey, fp string) (JournalEntry, bool, error)
	Complete(ctx context.Context, k types.CallKey, res types.ToolResult) error
	ByFingerprint(ctx context.Context, sessionID string, fp string) ([]JournalEntry, error)
}

// JournalFactory returns a fresh, empty journal whose replay window is
// replayTTL, judged by the store's own clock.
type JournalFactory func(t *testing.T, replayTTL time.Duration) JournalStore

// Journal runs the Journal conformance suite against the implementation
// produced by newJournal. It covers stores.concurrent-reserve,
// stores.journal-ttl and stores.replay-returns-recorded-result.
func Journal(t *testing.T, newJournal JournalFactory) {
	t.Run("stores.concurrent-reserve", func(t *testing.T) {
		j := newJournal(t, time.Hour)
		k := types.CallKey{SessionID: "s1", CallID: "c1"}

		const n = 10
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			created int
			errs    int
		)
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, ok, err := j.Reserve(context.Background(), k, "fp")
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs++
					return
				}
				if ok {
					created++
				}
			}()
		}
		wg.Wait()

		if errs != 0 {
			t.Fatalf("reserve errors: %d", errs)
		}
		if created != 1 {
			t.Fatalf("created=%d, want exactly 1", created)
		}
	})

	t.Run("stores.replay-returns-recorded-result", func(t *testing.T) {
		ctx := context.Background()
		j := newJournal(t, time.Hour)
		k := types.CallKey{SessionID: "s1", CallID: "c1"}

		e, created, err := j.Reserve(ctx, k, "fp")
		if err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		if !created {
			t.Fatal("first Reserve: created=false, want true")
		}
		if e.State != JournalReserved {
			t.Fatalf("first Reserve state=%v, want Reserved", e.State)
		}
		if e.Fingerprint != "fp" {
			t.Fatalf("first Reserve fingerprint=%q, want %q", e.Fingerprint, "fp")
		}

		res := types.ToolResult{ID: "r1", Content: []types.Block{
			types.Text{Text: "ok"},
		}}
		if err := j.Complete(ctx, k, res); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		// A caller-side mutation after Complete must not leak into the
		// stored result: replay returns what was recorded.
		res.Content[0] = types.Text{Text: "mutated"}

		e, created, err = j.Reserve(ctx, k, "fp")
		if err != nil {
			t.Fatalf("second Reserve: %v", err)
		}
		if created {
			t.Fatal("second Reserve: created=true, recorded result would be lost")
		}
		if e.State != JournalCompleted {
			t.Fatalf("replayed state=%v, want Completed", e.State)
		}
		if len(e.Result.Content) != 1 {
			t.Fatalf("replayed result = %+v, want one recorded block", e.Result)
		}
		txt, ok := e.Result.Content[0].(types.Text)
		if !ok || txt.Text != "ok" {
			t.Fatalf("replayed result = %+v, want the recorded one", e.Result)
		}
	})

	t.Run("stores.journal-ttl", func(t *testing.T) {
		ctx := context.Background()
		const ttl = 50 * time.Millisecond
		j := newJournal(t, ttl)
		k := types.CallKey{SessionID: "s1", CallID: "c1"}

		if _, _, err := j.Reserve(ctx, k, "fp"); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		if err := j.Complete(ctx, k, types.ToolResult{ID: "r1"}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		time.Sleep(2 * ttl)

		e, created, err := j.Reserve(ctx, k, "fp")
		if err != nil {
			t.Fatalf("Reserve after expiry: %v", err)
		}
		if !created {
			t.Fatal("Reserve after expiry: created=false, want a fresh entry")
		}
		if e.State != JournalReserved {
			t.Fatalf("state after expiry=%v, want Reserved", e.State)
		}

		entries, err := j.ByFingerprint(ctx, "s1", "fp")
		if err != nil {
			t.Fatalf("ByFingerprint: %v", err)
		}
		for _, en := range entries {
			if en.State == JournalCompleted && en.Key == k.CallID {
				t.Fatalf("expired entry still listed: %+v", en)
			}
		}
	})

	t.Run("by-fingerprint-scopes-to-session", func(t *testing.T) {
		ctx := context.Background()
		j := newJournal(t, time.Hour)
		k := types.CallKey{SessionID: "s1", CallID: "c1"}

		if _, _, err := j.Reserve(ctx, k, "fp"); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		if err := j.Complete(ctx, k, types.ToolResult{ID: "r1"}); err != nil {
			t.Fatalf("Complete: %v", err)
		}

		entries, err := j.ByFingerprint(ctx, "s1", "fp")
		if err != nil {
			t.Fatalf("ByFingerprint: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("len(entries)=%d, want 1", len(entries))
		}
		if entries[0].State != JournalCompleted || entries[0].Fingerprint != "fp" {
			t.Fatalf("entry = %+v, want the completed fp entry", entries[0])
		}

		entries, err = j.ByFingerprint(ctx, "s2", "fp")
		if err != nil {
			t.Fatalf("ByFingerprint other session: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("other session saw %d entries, want 0", len(entries))
		}
	})
}
