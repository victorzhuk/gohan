package std

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type countingJournal struct {
	stores.Journal
	reserves  int
	completes int
}

func (c *countingJournal) Reserve(ctx context.Context, k types.CallKey, fp stores.Fingerprint) (stores.Entry, bool, error) {
	c.reserves++
	return c.Journal.Reserve(ctx, k, fp)
}

func (c *countingJournal) Complete(ctx context.Context, k types.CallKey, res types.ToolResult) error {
	c.completes++
	return c.Journal.Complete(ctx, k, res)
}

func bookingSpecs() map[string]types.ToolSpec {
	return map[string]types.ToolSpec{
		"create_booking": {Name: "create_booking", Effect: types.SideEffect},
		"get_booking":    {Name: "get_booking", Effect: types.ReadOnly},
	}
}

func lookupOf(specs map[string]types.ToolSpec) SpecLookup {
	return func(name string) (types.ToolSpec, bool) {
		s, ok := specs[name]
		return s, ok
	}
}

func toolCall(name, id, args string) types.ToolUse {
	return types.ToolUse{ID: id, Name: name, Args: jsontext.Value(args)}
}

func TestJournalIntent(t *testing.T) {
	t.Run("stores.late-commit", func(t *testing.T) {
		j := &countingJournal{Journal: stores.NewMemoryJournal()}
		calls := 0
		tool := chains.ToolFunc(func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			calls++
			if calls == 1 {
				return types.ToolResult{Outcome: types.Unknown, Error: &types.ToolError{Kind: types.OutcomeUnknown, Message: "timeout"}}, nil
			}
			return types.ToolResult{Outcome: types.Succeeded}, nil
		})
		step := Journal(j, WithJournalSpecs(lookupOf(bookingSpecs())))(tool)
		ctx := context.Background()
		first, err := step(ctx, toolCall("create_booking", "c1", `{"room":7}`))
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		if first.Outcome != types.Unknown {
			t.Fatalf("first result outcome = %v, want Unknown", first.Outcome)
		}
		second, err := step(ctx, toolCall("create_booking", "c2", `{"room":7}`))
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if second.Outcome != types.Succeeded {
			t.Fatalf("second result outcome = %v, want Succeeded", second.Outcome)
		}
		if calls != 2 {
			t.Fatalf("tool calls = %d, want 2", calls)
		}
		entries, err := j.ByFingerprint(ctx, "", CanonicalFingerprint("create_booking", json.RawMessage(`{"room":7}`)))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("entries = %d, want 1", len(entries))
		}
		if entries[0].Key != "c1" {
			t.Fatalf("pinned key = %q, want c1", entries[0].Key)
		}
		if entries[0].State != stores.Completed {
			t.Fatalf("entry state = %v, want Completed", entries[0].State)
		}
	})

	t.Run("stores.fingerprint-after-compaction", func(t *testing.T) {
		j := &countingJournal{Journal: stores.NewMemoryJournal()}
		calls := 0
		tool := chains.ToolFunc(func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			calls++
			return types.ToolResult{Outcome: types.Succeeded}, nil
		})
		step := Journal(j, WithJournalSpecs(lookupOf(bookingSpecs())))(tool)
		ctx := context.Background()
		if _, err := step(ctx, toolCall("create_booking", "c1", `{"room":7}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := step(ctx, toolCall("create_booking", "c2", `{"room":7}`)); err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatalf("tool calls = %d, want 2 (completed side effect is not replayed)", calls)
		}
		entries, _ := j.ByFingerprint(ctx, "", CanonicalFingerprint("create_booking", json.RawMessage(`{"room":7}`)))
		if len(entries) != 2 {
			t.Fatalf("entries = %d, want 2 under distinct keys", len(entries))
		}
	})

	t.Run("stores.crash-window", func(t *testing.T) {
		mem := stores.NewMemoryJournal()
		j := &countingJournal{Journal: mem}
		ctx := context.Background()
		fp := CanonicalFingerprint("create_booking", json.RawMessage(`{"room":7}`))
		if _, _, err := mem.Reserve(ctx, types.CallKey{CallID: "c1"}, fp); err != nil {
			t.Fatal(err)
		}
		var sawKey string
		tool := chains.ToolFunc(func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			k, ok := gohan.IdempotencyKey(ctx)
			if !ok {
				t.Error("idempotency key missing in retry ctx")
			}
			sawKey = k
			return types.ToolResult{Outcome: types.Succeeded}, nil
		})
		step := Journal(j, WithJournalSpecs(lookupOf(bookingSpecs())))(tool)
		if _, err := step(ctx, toolCall("create_booking", "c2", `{"room":7}`)); err != nil {
			t.Fatal(err)
		}
		if sawKey != "c1" {
			t.Fatalf("retry idempotency key = %q, want c1", sawKey)
		}
		entries, _ := mem.ByFingerprint(ctx, "", fp)
		if len(entries) != 1 || entries[0].Key != "c1" || entries[0].State != stores.Completed {
			t.Fatalf("entry = %+v, want c1 Completed", entries)
		}
	})

	t.Run("canonical fingerprint", func(t *testing.T) {
		a := CanonicalFingerprint("create_booking", json.RawMessage(`{"b":1,"a":2}`))
		b := CanonicalFingerprint("create_booking", json.RawMessage(`{"a":2, "b":1}`))
		if a != b {
			t.Fatalf("fingerprints differ across key order: %s vs %s", a, b)
		}
		if a == CanonicalFingerprint("create_booking", json.RawMessage(`{"a":3,"b":1}`)) {
			t.Fatal("different args produced the same fingerprint")
		}
	})
}
