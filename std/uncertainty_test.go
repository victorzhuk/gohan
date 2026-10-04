package std

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestUncertainty(t *testing.T) {
	ctx := context.Background()

	t.Run("stores.read-back-offered", func(t *testing.T) {
		j := stores.NewMemoryJournal()
		specs := map[string]types.ToolSpec{
			"create_booking": {Name: "create_booking", Effect: types.SideEffect, ReadBack: "get_booking"},
		}
		tool := chains.ToolFunc(func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			return types.ToolResult{Outcome: types.Unknown}, nil
		})
		step := Journal(j, WithJournalSpecs(lookupOf(specs)))(tool)
		res, err := step(ctx, toolCall("create_booking", "c1", `{"room":7}`))
		if err != nil {
			t.Fatal(err)
		}
		var text string
		for _, b := range res.Content {
			if tb, ok := b.(types.Text); ok {
				text += tb.Text
			}
		}
		if !strings.Contains(text, "get_booking") {
			t.Fatalf("result text = %q, want it to name get_booking", text)
		}
		if res.Outcome != types.Unknown {
			t.Fatalf("outcome = %v, want Unknown", res.Outcome)
		}
		entries, _ := j.ByFingerprint(ctx, "", CanonicalFingerprint("create_booking", json.RawMessage(`{"room":7}`)))
		if len(entries) != 1 || entries[0].Result.Outcome != types.Unknown {
			t.Fatalf("journal = %+v, want one Unknown entry", entries)
		}
	})

	t.Run("stores.uncertainty-surfaced", func(t *testing.T) {
		entries := []stores.Entry{
			{Key: "c1", Result: types.ToolResult{Outcome: types.Unknown}},
			{Key: "c2", Result: types.ToolResult{Outcome: types.Succeeded}},
			{Key: "c3", Result: types.ToolResult{Outcome: types.Unknown}},
		}
		keys := UncertainKeys("s1", entries)
		want := []types.CallKey{{SessionID: "s1", CallID: "c1"}, {SessionID: "s1", CallID: "c3"}}
		if !slices.Equal(keys, want) {
			t.Fatalf("keys = %v, want %v", keys, want)
		}
		err := NewUncertainOutcomeError("partial answer", keys...)
		got, ok := errors.AsType[*UncertainOutcomeError](err)
		if !ok {
			t.Fatalf("err type = %T, want *UncertainOutcomeError", err)
		}
		if !slices.Equal(got.Keys, want) {
			t.Fatalf("error keys = %v, want %v", got.Keys, want)
		}
		if got.Out != "partial answer" {
			t.Fatalf("error out = %v, want the partial output", got.Out)
		}
		done := types.Done{Uncertain: got.Keys}
		if len(done.Uncertain) != 2 || done.Uncertain[0].CallID != "c1" {
			t.Fatalf("Done.Uncertain = %v, want c1 and c3", done.Uncertain)
		}
	})
}
