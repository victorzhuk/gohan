package stores

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestJournalEntryAttempts(t *testing.T) {
	ctx := context.Background()
	j := NewMemoryJournal()
	k := types.CallKey{SessionID: "s1", CallID: "m1"}
	fp := Fingerprint("model-fp")
	if _, created, err := j.Reserve(ctx, k, "model", fp, nil); err != nil || !created {
		t.Fatalf("Reserve: created=%v err=%v", created, err)
	}
	if err := j.CompleteModel(ctx, k, types.ToolResult{}, ModelCompletion{Attempts: 2, Endpoint: "fast"}); err != nil {
		t.Fatalf("CompleteModel: %v", err)
	}
	entries, err := j.ByFingerprint(ctx, "s1", fp)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ByFingerprint: %v %v", entries, err)
	}
	e := entries[0]
	if e.Attempts != 2 || e.Endpoint != "fast" {
		t.Fatalf("attempts = %d endpoint = %q, want 2 and fast", e.Attempts, e.Endpoint)
	}
	// The stored envelope round-trips the new fields.
	rec := j
	raw, err := rec.StoredRecord(ctx, "journal/s1/m1")
	if err != nil {
		t.Fatal(err)
	}
	var env RecordEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	entry, err := DecodeRecord[Entry](ctx, rec.schemas, env)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Attempts != 2 || entry.Endpoint != "fast" {
		t.Fatalf("decoded attempts = %d endpoint = %q, want 2 and fast", entry.Attempts, entry.Endpoint)
	}
	t.Run("tool-entry-zero-valued", func(t *testing.T) {
		k := types.CallKey{SessionID: "s1", CallID: "t1"}
		if _, created, err := j.Reserve(ctx, k, "create_booking", Fingerprint("fp-t1"), nil); err != nil || !created {
			t.Fatalf("Reserve: created=%v err=%v", created, err)
		}
		if err := j.Complete(ctx, k, types.ToolResult{Outcome: types.Succeeded}); err != nil {
			t.Fatal(err)
		}
		entries, err := j.ByFingerprint(ctx, "s1", Fingerprint("fp-t1"))
		if err != nil || len(entries) != 1 {
			t.Fatalf("ByFingerprint: %v %v", entries, err)
		}
		if entries[0].Attempts != 0 || entries[0].Endpoint != "" {
			t.Fatalf("tool entry attempts = %d endpoint = %q, want zero values", entries[0].Attempts, entries[0].Endpoint)
		}
	})
}
