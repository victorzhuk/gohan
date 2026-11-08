package gohan_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

// legacyJournalFingerprint reproduces the pre-fix journal fingerprint from
// its own float64 decoding, independent of the current fingerprint
// functions, so the scenario proves the stored legacy string rather than
// the new code.
func legacyJournalFingerprint(t *testing.T, tool, args string) stores.Fingerprint {
	t.Helper()
	var rounded map[string]any
	if err := json.Unmarshal([]byte(args), &rounded); err != nil {
		t.Fatalf("fixture args are not a JSON object: %v", err)
	}
	b, err := json.Marshal(rounded)
	if err != nil {
		t.Fatalf("fixture args cannot be re-encoded: %v", err)
	}
	sum := sha256.Sum256([]byte(tool + "\n" + string(b)))
	return stores.Fingerprint(hex.EncodeToString(sum[:]))
}

func TestCallAuthorityScenariosJournal(t *testing.T) {
	ctx := context.Background()
	args92 := json.RawMessage(`{"n":9007199254740992}`)
	args93 := json.RawMessage(`{"n":9007199254740993}`)

	t.Run("tools.legacy-intent-exact-match", func(t *testing.T) {
		legacy92 := legacyJournalFingerprint(t, "wipe", `{"n":9007199254740992}`)
		if legacyJournalFingerprint(t, "wipe", `{"n":9007199254740993}`) != legacy92 {
			t.Fatal("legacy encodings differ: the fixture must reproduce the rounded legacy format the journal entry was stored under")
		}

		mem := stores.NewMemoryJournal()
		if _, _, err := mem.Reserve(ctx, types.CallKey{CallID: "c1"}, "wipe", legacy92, args92); err != nil {
			t.Fatalf("seed reserved candidate: %v", err)
		}
		sawKey := ""
		tool := chains.ToolFunc(func(ctx context.Context, _ types.ToolUse) (types.ToolResult, error) {
			if k, ok := gohan.IdempotencyKey(ctx); ok {
				sawKey = k
			}
			return types.ToolResult{Outcome: types.Succeeded}, nil
		})
		res, err := std.Journal(mem)(tool)(ctx, types.ToolUse{ID: "c2", Name: "wipe", Args: args92})
		if err != nil {
			t.Fatalf("exact-match candidate call: %v", err)
		}
		if sawKey != "c1" {
			t.Fatalf("replay idempotency key = %q, want c1: the candidate's complete stored arguments match the call exactly, so its original reserved key is preserved", sawKey)
		}
		if res.ID != "c1" {
			t.Fatalf("result id = %q, want c1: the replayed call records under the reserved entry identity", res.ID)
		}
		entries, err := mem.ByFingerprint(ctx, "", legacy92)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Key != "c1" || entries[0].State != stores.Completed || entries[0].Fingerprint != legacy92 {
			t.Fatalf("journal entries = %+v, want the single original entry completed under key c1 with its stored legacy fingerprint: journal history must not be rewritten", entries)
		}

		calls := 0
		counting := chains.ToolFunc(func(_ context.Context, _ types.ToolUse) (types.ToolResult, error) {
			calls++
			return types.ToolResult{Outcome: types.Succeeded}, nil
		})

		mem = stores.NewMemoryJournal()
		if _, _, err := mem.Reserve(ctx, types.CallKey{CallID: "c1"}, "wipe", legacy92, args92); err != nil {
			t.Fatalf("seed reserved candidate: %v", err)
		}
		refused, err := std.Journal(mem)(counting)(ctx, types.ToolUse{ID: "c2", Name: "wipe", Args: args93})
		if err == nil {
			t.Fatalf("unequal stored arguments let the call run: result %+v, want refusal: the candidate's evidence differs and must not authorize a new effect", refused)
		}
		if calls != 0 {
			t.Fatalf("tool executions under the refusal = %d, want 0", calls)
		}
		entries, err = mem.ByFingerprint(ctx, "", legacy92)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Key != "c1" || entries[0].State != stores.Reserved {
			t.Fatalf("journal entries = %+v, want the untouched reserved candidate c1", entries)
		}

		mem = stores.NewMemoryJournal()
		if _, _, err := mem.Reserve(ctx, types.CallKey{CallID: "c1"}, "wipe", legacy92, nil); err != nil {
			t.Fatalf("seed candidate without evidence: %v", err)
		}
		refused, err = std.Journal(mem)(counting)(ctx, types.ToolUse{ID: "c2", Name: "wipe", Args: args92})
		if err == nil {
			t.Fatalf("missing stored evidence let the call run: result %+v, want refusal: absent arguments cannot prove the call", refused)
		}
		if calls != 0 {
			t.Fatalf("tool executions under the refusal = %d, want 0", calls)
		}

		mem = stores.NewMemoryJournal()
		if _, _, err := mem.Reserve(ctx, types.CallKey{CallID: "c1"}, "wipe", legacy92, args92); err != nil {
			t.Fatalf("seed completed candidate: %v", err)
		}
		if err := mem.Complete(ctx, types.CallKey{CallID: "c1"}, types.ToolResult{ID: "c1", Outcome: types.Succeeded}); err != nil {
			t.Fatalf("seed completion: %v", err)
		}
		if _, err := std.Journal(mem)(counting)(ctx, types.ToolUse{ID: "c2", Name: "wipe", Args: args92}); err != nil {
			t.Fatalf("call after a completed success: %v, want a fresh execution: a completed success must not suppress a genuinely new call", err)
		}
		if calls != 1 {
			t.Fatalf("tool executions after the completed success = %d, want 1", calls)
		}
		entries, err = mem.ByFingerprint(ctx, "", legacy92)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Key != "c1" || entries[0].Fingerprint != legacy92 {
			t.Fatalf("journal entries = %+v, want the completed success untouched under its original key and fingerprint", entries)
		}
		newer, err := mem.ByFingerprint(ctx, "", std.CanonicalFingerprint("wipe", args92))
		if err != nil {
			t.Fatal(err)
		}
		if len(newer) != 1 || newer[0].Key != "c2" {
			t.Fatalf("new entries = %+v, want one fresh entry under its own key c2 and the exact fingerprint", newer)
		}
	})
}
