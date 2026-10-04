package context

import (
	"context"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// notesProvider is the SlotSession notes provider the harness injects: the
// notes never enter history, so a projection over History cannot touch them.
type notesProvider struct {
	notes string
}

func (notesProvider) Slot() types.ContextSlot { return types.SlotSession }

func (p notesProvider) Provide(_ context.Context, _ types.RunInfo) ([]types.Block, error) {
	return []types.Block{types.Text{Text: p.notes}}, nil
}

func TestTruncateKeepsNotes(t *testing.T) {
	t.Run("working-state.notes-survive-compaction", func(t *testing.T) {
		const notes = "NOTE booking confirmed, retry budget spent"
		provider := notesProvider{notes: notes}

		// Two large turns with a window that only fits the newer one force
		// the oldest droppable turn out of the view.
		h := history(textTurn(100), textTurn(100))
		tr := NewTruncate(WithTruncateEstimator(textEstimator{}))
		projected, err := tr.Project(context.Background(), h, profile(300))
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if len(projected.Messages) >= len(h.Messages) {
			t.Fatalf("projection truncated nothing: %d messages of %d", len(projected.Messages), len(h.Messages))
		}

		blocks, err := provider.Provide(context.Background(), types.RunInfo{SessionID: "s1"})
		if err != nil {
			t.Fatalf("Provide after projection: %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("notes blocks = %d, want 1", len(blocks))
		}
		text, ok := blocks[0].(types.Text)
		if !ok || text.Text != notes {
			t.Fatalf("notes provider lost notes after projection: %v", blocks[0])
		}

		for i, m := range projected.Messages {
			for _, b := range m.Blocks {
				if txt, ok := b.(types.Text); ok && strings.Contains(txt.Text, notes) {
					t.Errorf("projected message %d carries note text", i)
				}
			}
		}
	})
}
