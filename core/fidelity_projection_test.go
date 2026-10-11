package gohan

import (
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestProjectFidelity(t *testing.T) {
	t.Run("foreign-reasoning-dropped", func(t *testing.T) {
		msgs := []types.Message{{
			Blocks: []types.Block{
				types.Text{Text: "keep"},
				types.Reasoning{Provider: "a"},
			},
		}}
		out, drops := ProjectFidelity(msgs, "b", types.Caps{})
		if len(out) != 1 || len(out[0].Blocks) != 1 {
			t.Fatalf("out = %+v, want the text kept and the foreign reasoning dropped", out)
		}
		if len(drops) != 1 || drops[0].Kind != types.KindReasoning || drops[0].Count != 1 {
			t.Fatalf("drops = %v, want one reasoning drop", drops)
		}
	})

	t.Run("own-reasoning-kept-without-drop", func(t *testing.T) {
		msgs := []types.Message{{Blocks: []types.Block{types.Reasoning{Provider: "a"}}}}
		out, drops := ProjectFidelity(msgs, "a", types.Caps{})
		if len(out) != 1 || len(out[0].Blocks) != 1 {
			t.Fatalf("out = %+v, want the producing provider's reasoning kept", out)
		}
		if drops != nil {
			t.Fatalf("drops = %v, want none", drops)
		}
	})

	t.Run("undropped-declared-kinds-kept", func(t *testing.T) {
		caps := types.Caps{Fidelity: map[types.BlockKind]types.Fidelity{types.KindImage: types.Preserved}}
		msgs := []types.Message{{Blocks: []types.Block{types.Image{}}}}
		out, drops := ProjectFidelity(msgs, "a", caps)
		if len(out) != 1 || len(out[0].Blocks) != 1 {
			t.Fatalf("out = %+v, want the declared preserved image kept", out)
		}
		if drops != nil {
			t.Fatalf("drops = %v, want none", drops)
		}
	})
}

// TestProjectFidelityKeepsBoundOpaque pins the shared opaque-compaction
// binding rule inside the projection filter: an opaque block survives only
// for the endpoint recorded in Compaction.Model, a text block everywhere.
func TestProjectFidelityKeepsBoundOpaque(t *testing.T) {
	msgs := []types.Message{{Blocks: []types.Block{
		types.Compaction{Kind: types.CompactionOpaque, Model: "a"},
		types.Compaction{Kind: types.CompactionText, Model: "a"},
	}}}

	same, drops := ProjectFidelity(msgs, "a", types.Caps{})
	if len(same) != 1 || len(same[0].Blocks) != 2 {
		t.Fatalf("out = %+v, want both blocks kept for the producing endpoint", same)
	}
	if drops != nil {
		t.Fatalf("drops = %v, want none for the producing endpoint", drops)
	}

	foreign, drops := ProjectFidelity(msgs, "b", types.Caps{})
	if len(foreign) != 1 || len(foreign[0].Blocks) != 1 {
		t.Fatalf("out = %+v, want only the text block kept for the other endpoint", foreign)
	}
	if _, ok := foreign[0].Blocks[0].(types.Compaction); !ok {
		t.Fatal("the surviving block is not the text compaction")
	}
	if len(drops) != 1 || drops[0].Kind != types.KindCompaction || drops[0].Count != 1 {
		t.Fatalf("drops = %v, want one compaction drop", drops)
	}
}
