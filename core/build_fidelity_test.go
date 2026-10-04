package gohan

import (
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestBuildFidelity(t *testing.T) {
	t.Run("messages.fidelity-gate-at-build", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		p := profile("small", true)
		p.Caps.Fidelity = map[types.BlockKind]types.Fidelity{
			types.KindFile: types.Dropped,
		}
		profiles := map[string]types.ModelProfile{"small": p}
		req := FlowRequest{Name: "docs", Blocks: []types.BlockKind{types.KindFile}}
		err = s.CheckFidelity(req, profiles, "small")
		if !errors.Is(err, ErrFidelityDropped) {
			t.Fatalf("err = %v, want ErrFidelityDropped", err)
		}
		for _, part := range []string{"docs", "small", "file"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("err %q misses %q", err, part)
			}
		}

		req.AllowDrop = []types.BlockKind{types.KindFile}
		if err := s.CheckFidelity(req, profiles, "small"); err != nil {
			t.Fatalf("with AllowDrop: %v", err)
		}
	})

	t.Run("messages.reasoning-dropped-across-providers", func(t *testing.T) {
		history := []types.Message{
			{Role: types.RoleAssistant, Blocks: []types.Block{
				types.Reasoning{BlockBase: types.BlockBase{Seq: 1}, Text: "think", Provider: "anthropic"},
				types.Text{BlockBase: types.BlockBase{Seq: 2}, Text: "answer"},
			}},
		}
		got, drops := ProjectFidelity(history, "vllm", types.Caps{})
		if len(drops) != 1 || drops[0].Kind != types.KindReasoning || drops[0].Count != 1 {
			t.Fatalf("drops = %v, want one reasoning drop", drops)
		}
		if len(got) != 1 || len(got[0].Blocks) != 1 {
			t.Fatalf("projected = %+v, want the text block only", got)
		}
		if _, ok := got[0].Blocks[0].(types.Text); !ok {
			t.Errorf("surviving block = %T, want Text", got[0].Blocks[0])
		}

		same, drops := ProjectFidelity(history, "anthropic", types.Caps{})
		if drops != nil {
			t.Errorf("drops = %v, want none for the producing provider", drops)
		}
		if len(same[0].Blocks) != 2 {
			t.Errorf("blocks = %d, want reasoning intact for its own provider", len(same[0].Blocks))
		}
	})

	t.Run("model.undeclared-fidelity", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		p := profile("small", true)
		p.Caps.Fidelity = map[types.BlockKind]types.Fidelity{
			types.KindImage: types.Preserved,
		}
		profiles := map[string]types.ModelProfile{"small": p}
		err = s.CheckFidelity(FlowRequest{
			Name:   "docs",
			Blocks: []types.BlockKind{types.KindFile},
		}, profiles, "small")
		if !errors.Is(err, ErrFidelityUndeclared) {
			t.Fatalf("err = %v, want ErrFidelityUndeclared", err)
		}
		for _, part := range []string{"small", "file"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("err %q misses %q", err, part)
			}
		}
	})

	t.Run("build.opaque-compaction-fallback", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		primary := profile("big", true)
		primary.Caps.Compaction = types.CompactionOpaqueCap
		fallback := profile("small", true)
		profiles := map[string]types.ModelProfile{"big": primary, "small": fallback}
		err = s.CheckFidelity(FlowRequest{
			Name:            "agent",
			ProviderCompact: true,
			Fallback:        "small",
		}, profiles, "big")
		if !errors.Is(err, ErrOpaqueCompaction) {
			t.Fatalf("err = %v, want ErrOpaqueCompaction", err)
		}
		for _, part := range []string{"agent", "big", "provider_compact"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("err %q misses %q", err, part)
			}
		}

		textPrimary := profile("big", true)
		textPrimary.Caps.Compaction = types.CompactionTextCap
		if err := s.CheckFidelity(FlowRequest{
			Name:            "agent",
			ProviderCompact: true,
			Fallback:        "small",
		}, map[string]types.ModelProfile{"big": textPrimary, "small": fallback}, "big"); err != nil {
			t.Fatalf("text compaction across providers: %v", err)
		}
	})

	t.Run("compatible-matrix-passes", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		p := profile("small", true)
		p.Caps.Fidelity = map[types.BlockKind]types.Fidelity{
			types.KindImage: types.Preserved,
			types.KindFile:  types.Degraded,
		}
		err = s.CheckFidelity(FlowRequest{
			Name:      "docs",
			Blocks:    []types.BlockKind{types.KindImage, types.KindFile, types.KindToolUse, types.KindToolResult},
			AllowDrop: []types.BlockKind{},
		}, map[string]types.ModelProfile{"small": p}, "small")
		if err != nil {
			t.Fatalf("compatible matrix: %v", err)
		}
	})

	t.Run("projection-preserves-order", func(t *testing.T) {
		msg := types.Message{Role: types.RoleAssistant, Blocks: []types.Block{
			types.Text{BlockBase: types.BlockBase{Seq: 1}, Text: "a"},
			types.Reasoning{BlockBase: types.BlockBase{Seq: 2}, Provider: "other"},
			types.ToolUse{BlockBase: types.BlockBase{Seq: 3}, ID: "t1", Name: "search"},
			types.Text{BlockBase: types.BlockBase{Seq: 4}, Text: "b"},
		}}
		got, drops := ProjectFidelity([]types.Message{msg}, "target", types.Caps{})
		if len(drops) != 1 || drops[0].Kind != types.KindReasoning {
			t.Fatalf("drops = %v, want reasoning only", drops)
		}
		blocks := got[0].Blocks
		if len(blocks) != 3 {
			t.Fatalf("blocks = %d, want 3", len(blocks))
		}
		wantSeq := []int64{1, 3, 4}
		for i, b := range blocks {
			var seq int64
			switch v := b.(type) {
			case types.Text:
				seq = v.Seq
			case types.ToolUse:
				seq = v.Seq
			}
			if seq != wantSeq[i] {
				t.Errorf("block %d seq = %d, want %d", i, seq, wantSeq[i])
			}
		}
	})

	t.Run("allow-drop-governs-projection-drop", func(t *testing.T) {
		msg := types.Message{Role: types.RoleUser, Blocks: []types.Block{
			types.File{BlockBase: types.BlockBase{Seq: 1}, Name: "r.pdf"},
			types.Text{BlockBase: types.BlockBase{Seq: 2}, Text: "summarize"},
		}}
		caps := types.Caps{Fidelity: map[types.BlockKind]types.Fidelity{
			types.KindFile: types.Dropped,
		}}
		got, drops := ProjectFidelity([]types.Message{msg}, "target", caps)
		if len(drops) != 1 || drops[0].Kind != types.KindFile {
			t.Fatalf("drops = %v, want one file drop", drops)
		}
		if len(got[0].Blocks) != 1 {
			t.Fatalf("blocks = %d, want the text block only", len(got[0].Blocks))
		}
	})
}
