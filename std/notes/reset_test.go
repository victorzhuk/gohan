package notes

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	stdcontext "github.com/victorzhuk/gohan/std/context"
)

// wordEstimator counts one token per word so a window can be sized in
// words; deterministic and independent of any provider tokenizer.
type wordEstimator struct{}

func (wordEstimator) Estimate(req types.ModelRequest, _ types.Caps) int {
	total := 0
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			if txt, ok := b.(types.Text); ok {
				total += len(strings.Fields(txt.Text))
			}
		}
	}
	return total
}

func testProfile(window int) types.ModelProfile {
	return types.ModelProfile{Name: "m", ContextWindow: window}
}

func turn(words int) types.Message {
	return types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: strings.Repeat("filler ", words)}}}
}

func writeNote(t *testing.T, n *Notes, ri types.RunInfo, text string) {
	t.Helper()
	ctx := types.WithRunInfo(context.Background(), ri)
	raw, err := json.Marshal(writeArgs{Notes: text})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	res, err := n.Call(ctx, raw)
	if err != nil {
		t.Fatalf("notes_write: %v", err)
	}
	if res.Outcome != types.Succeeded {
		t.Fatalf("notes_write outcome = %d, want succeeded", res.Outcome)
	}
}

func slotText(t *testing.T, n *Notes, ri types.RunInfo) string {
	t.Helper()
	blocks, err := n.Provide(context.Background(), ri)
	if err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("session slot blocks = %d, want 1", len(blocks))
	}
	txt, ok := blocks[0].(types.Text)
	if !ok {
		t.Fatalf("slot block is %T, want Text", blocks[0])
	}
	return txt.Text
}

// TestNotesSurviveReset drives the real path: a note written in one run is
// still in the assembled session slot of a new run whose history was
// truncated, because the notes live in the store, not in the history.
func TestNotesSurviveReset(t *testing.T) {
	const note = "NOTE booking confirmed, retry budget spent"

	store := &stores.MemoryNotes{}
	n, err := New(store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first := types.RunInfo{
		SessionID: "s1",
		RunID:     "r1",
		Principal: types.Principal{Subject: "u1", Tenant: "acme"},
	}

	t.Run("no notes before a write", func(t *testing.T) {
		blocks, err := n.Provide(context.Background(), first)
		if err != nil {
			t.Fatalf("Provide: %v", err)
		}
		if len(blocks) != 0 {
			t.Fatalf("empty store contributed %d blocks, want 0", len(blocks))
		}
	})

	t.Run("tools.notes-survive-reset", func(t *testing.T) {
		writeNote(t, n, first, note)

		// Reset the run: a new run in the same session starts with a
		// history that carries the note only in a now-truncated turn.
		second := first
		second.RunID = "r2"
		second.Turn = 1

		h := stores.History{Version: 1, Messages: []types.Message{
			turn(100),
			{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: note}}},
			turn(100),
		}}
		tr := stdcontext.NewTruncate(stdcontext.WithTruncateEstimator(wordEstimator{}))
		projected, err := tr.Project(context.Background(), h, testProfile(150))
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if len(projected.Messages) >= len(h.Messages) {
			t.Fatalf("projection truncated nothing: %d messages of %d", len(projected.Messages), len(h.Messages))
		}

		if got := slotText(t, n, second); got != note {
			t.Fatalf("session slot = %q, want %q", got, note)
		}
	})

	t.Run("truncation without a reset does not lose notes", func(t *testing.T) {
		h := stores.History{Version: 1, Messages: []types.Message{turn(100), turn(100), turn(100)}}
		tr := stdcontext.NewTruncate(stdcontext.WithTruncateEstimator(wordEstimator{}))
		projected, err := tr.Project(context.Background(), h, testProfile(150))
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if len(projected.Messages) >= len(h.Messages) {
			t.Fatalf("projection truncated nothing: %d messages of %d", len(projected.Messages), len(h.Messages))
		}
		if got := slotText(t, n, first); got != note {
			t.Fatalf("session slot after truncation = %q, want %q", got, note)
		}
	})
}
