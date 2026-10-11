package gohan

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// compactionSummary is the single text block a test summary carries; the
// marker text names the block so assertions stay readable.
func compactionSummary(marker string) []types.Block {
	return []types.Block{types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}}, Text: marker}}
}

// compactionMessage builds one assistant message carrying one Compaction
// block that covers the first covers messages of the log.
func compactionMessage(covers int64, marker string, extra ...types.Block) types.Message {
	blocks := make([]types.Block, 0, 1+len(extra))
	blocks = append(blocks, types.Compaction{
		BlockBase:  types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}},
		CoversUpTo: covers,
		Kind:       types.CompactionText,
		Summary:    compactionSummary(marker),
	})
	blocks = append(blocks, extra...)
	return types.Message{ID: "c" + marker, Role: types.RoleAssistant, Blocks: blocks}
}

func textMessage(marker string) types.Message {
	return types.Message{ID: "m" + marker, Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: marker}}}
}

// opaqueCompactionMessage builds one assistant message carrying one
// CompactionOpaque block bound to the named endpoint.
func opaqueCompactionMessage(covers int64, model string) types.Message {
	return types.Message{ID: "mopaque", Role: types.RoleAssistant, Blocks: []types.Block{
		types.Compaction{Kind: types.CompactionOpaque, CoversUpTo: covers, Model: model, Opaque: types.Raw{Provider: model, Value: "item"}},
	}}
}

func findText(messages []types.Message, marker string) bool {
	for _, msg := range messages {
		for _, b := range msg.Blocks {
			if t, ok := b.(types.Text); ok && t.Text == marker {
				return true
			}
		}
	}
	return false
}

func countSummary(messages []types.Message, marker string) int {
	n := 0
	for _, msg := range messages {
		for _, b := range msg.Blocks {
			if t, ok := b.(types.Text); ok && t.Text == marker {
				n++
			}
		}
	}
	return n
}

func TestCollapseCompactionHistory(t *testing.T) {
	t.Run("context.compaction-single-summary", func(t *testing.T) {
		messages := []types.Message{
			textMessage("u1"),
			textMessage("u2"),
			compactionMessage(2, "old"),
			textMessage("u3"),
			compactionMessage(3, "new"),
		}
		out := collapseCompactionHistory(messages, "p")
		if len(out) != 2 {
			t.Fatalf("len(out) = %d, want the head summary and u3", len(out))
		}
		if countSummary(out, "new") != 1 {
			t.Fatalf("newest summary renders %d times, want exactly once at the head", countSummary(out, "new"))
		}
		if countSummary(out, "old") != 0 {
			t.Fatal("superseded summary still rendered")
		}
		if !findText(out, "u3") {
			t.Fatal("retained message u3 dropped")
		}
		if findText(out, "u1") || findText(out, "u2") {
			t.Fatal("covered raw message survived the collapse")
		}
		if out[0].Role != types.RoleSystem {
			t.Fatalf("head role = %q, want system", out[0].Role)
		}
	})

	t.Run("covered-messages-dropped-newest-selected-by-position", func(t *testing.T) {
		messages := []types.Message{
			textMessage("u1"),
			compactionMessage(1, "old"),
			textMessage("u2"),
			compactionMessage(2, "new"),
			textMessage("u3"),
		}
		out := collapseCompactionHistory(messages, "p")
		if findText(out, "u1") {
			t.Fatal("covered raw message survived the collapse")
		}
		if !findText(out, "u2") || !findText(out, "u3") {
			t.Fatal("message outside the covered prefix dropped")
		}
		if countSummary(out, "old") != 0 {
			t.Fatal("superseded summary still rendered")
		}
		if countSummary(out, "new") != 1 {
			t.Fatal("newest summary not rendered exactly once")
		}
	})

	t.Run("unrelated-blocks-and-metadata-kept", func(t *testing.T) {
		retained := textMessage("u2")
		retained.Meta = map[string]any{"k": "v"}
		messages := []types.Message{
			textMessage("u1"),
			compactionMessage(1, "only", types.Text{Text: "beside"}),
			retained,
		}
		out := collapseCompactionHistory(messages, "p")
		if len(out) != 3 {
			t.Fatalf("len(out) = %d, want the head summary, the beside message and the retained message", len(out))
		}
		last := out[1]
		if last.ID != "conly" || len(last.Blocks) != 1 {
			t.Fatalf("beside message = %+v, want the compaction-free message with its unrelated block kept in place", last)
		}
		keptMsg := out[2]
		if keptMsg.ID != "mu2" || len(keptMsg.Meta) != 1 || keptMsg.Meta["k"] != "v" {
			t.Fatalf("retained message metadata = %+v, want it preserved", keptMsg.Meta)
		}
		if len(keptMsg.Blocks) != 1 {
			t.Fatalf("retained blocks = %d, want 1", len(keptMsg.Blocks))
		}
	})

	t.Run("no-compaction-block-returns-unchanged", func(t *testing.T) {
		messages := []types.Message{textMessage("u1"), textMessage("u2")}
		out := collapseCompactionHistory(messages, "p")
		if len(out) != 2 || out[0].ID != "mu1" {
			t.Fatal("plain log changed by the collapse")
		}
	})

	t.Run("pending-tool-call-after-boundary-survives", func(t *testing.T) {
		pending := types.Message{ID: "mpending", Role: types.RoleAssistant, Blocks: []types.Block{
			types.ToolUse{ID: "call-1", Name: "notes_write"},
		}}
		messages := []types.Message{
			textMessage("u1"),
			compactionMessage(1, "new"),
			pending,
		}
		out := collapseCompactionHistory(messages, "p")
		if len(out) != 2 || out[1].ID != "mpending" {
			t.Fatal("pending call after the boundary did not survive")
		}
	})

	t.Run("context.opaque-bound-to-model", func(t *testing.T) {
		t.Run("opaque-bound-elsewhere-drops-and-refits", func(t *testing.T) {
			messages := []types.Message{
				textMessage("u1"),
				opaqueCompactionMessage(1, "a"),
				textMessage("u2"),
			}
			out := collapseCompactionHistory(messages, "b")
			if len(out) != 2 {
				t.Fatalf("len(out) = %d, want the covered messages visible with no head summary", len(out))
			}
			if out[0].Role == types.RoleSystem {
				t.Fatal("a foreign opaque block rendered a summary head")
			}
			if !findText(out, "u1") || !findText(out, "u2") {
				t.Fatal("covered messages did not stay visible for the re-fit")
			}
			for _, msg := range out {
				for _, b := range msg.Blocks {
					if _, ok := b.(types.Compaction); ok {
						t.Fatal("a compaction block survived the foreign-endpoint drop")
					}
				}
			}
		})

		t.Run("opaque-bound-to-target-collapses", func(t *testing.T) {
			messages := []types.Message{
				textMessage("u1"),
				opaqueCompactionMessage(1, "a"),
				textMessage("u2"),
			}
			out := collapseCompactionHistory(messages, "a")
			if len(out) != 2 {
				t.Fatalf("len(out) = %d, want the head and u2 for the block's own endpoint", len(out))
			}
			if findText(out, "u1") {
				t.Fatal("covered message survived the block's own endpoint collapse")
			}
		})

		t.Run("text-compaction-keeps-collapse", func(t *testing.T) {
			messages := []types.Message{
				textMessage("u1"),
				compactionMessage(1, "new"),
				textMessage("u2"),
			}
			out := collapseCompactionHistory(messages, "other-endpoint")
			if len(out) != 2 || countSummary(out, "new") != 1 {
				t.Fatal("a text block is not model-bound: it must collapse for any target")
			}
		})
	})
}

// collapseAssembler records the history the composed assembly receives, so
// the composition order of collapse and projections is observable without
// running a provider.
type collapseAssembler struct {
	history []types.Message
}

func (a *collapseAssembler) assemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	a.history = in.History
	return types.ModelRequest{}, nil
}

func TestCollapseCompositionWithoutProjections(t *testing.T) {
	t.Run("context.compaction-collapse-without-projections", func(t *testing.T) {
		var inner collapseAssembler
		composed := collapseBeforeProjections(types.ModelProfile{Name: "p"}, withProjections(stores.ContextPolicy{}, types.ModelProfile{Name: "p"}, inner.assemble))
		in := types.AssembleInput{History: []types.Message{
			textMessage("u1"),
			compactionMessage(1, "new"),
			textMessage("u2"),
		}}
		if _, err := composed(context.Background(), in); err != nil {
			t.Fatalf("composed assembly: %v", err)
		}
		if len(inner.history) != 2 {
			t.Fatalf("history len = %d, want the head summary and u2: %+v", len(inner.history), inner.history)
		}
		if countSummary(inner.history, "new") != 1 {
			t.Fatal("empty projection list skipped the collapse")
		}
	})

	t.Run("collapse-runs-before-declared-projections", func(t *testing.T) {
		saw := errors.New("saw")
		marker := map[string]bool{}
		proj := collapseProbe{fn: func(h stores.History) error {
			if findText(h.Messages, "covered") {
				return saw
			}
			marker["clean"] = true
			return nil
		}}
		policy := stores.ContextPolicy{Projections: []stores.Projection{proj}}
		var inner collapseAssembler
		composed := collapseBeforeProjections(types.ModelProfile{Name: "p"}, withProjections(policy, types.ModelProfile{Name: "p"}, inner.assemble))
		in := types.AssembleInput{History: []types.Message{
			textMessage("covered"),
			compactionMessage(1, "new"),
		}}
		if _, err := composed(context.Background(), in); err != nil {
			t.Fatalf("composed assembly: %v", err)
		}
		if !marker["clean"] {
			t.Fatal("projection saw a message the collapse should have hidden")
		}
	})
}

// TestCollapseTargetsAssemblingProfile pins the D4 contract: the collapse
// target is the endpoint that assembles the request. An explicit profile
// wins; a caller that omits it — the foreign backend runtime does —
// assembles for the flow's own endpoint, so the resolved profile is the
// target.
func TestCollapseTargetsAssemblingProfile(t *testing.T) {
	foreignOpaqueLog := func() []types.Message {
		return []types.Message{
			textMessage("u1"),
			opaqueCompactionMessage(1, "a"),
			textMessage("u2"),
		}
	}

	t.Run("explicit-profile-is-the-target", func(t *testing.T) {
		var inner collapseAssembler
		composed := collapseBeforeProjections(types.ModelProfile{Name: "b"}, inner.assemble)
		in := types.AssembleInput{Profile: types.ModelProfile{Name: "a"}, History: foreignOpaqueLog()}
		if _, err := composed(context.Background(), in); err != nil {
			t.Fatalf("composed assembly: %v", err)
		}
		if len(inner.history) != 2 {
			t.Fatalf("history len = %d, want the block's own endpoint to collapse", len(inner.history))
		}
	})

	t.Run("omitted-profile-falls-back-to-the-flow-endpoint", func(t *testing.T) {
		var inner collapseAssembler
		composed := collapseBeforeProjections(types.ModelProfile{Name: "b"}, inner.assemble)
		in := types.AssembleInput{History: foreignOpaqueLog()}
		if _, err := composed(context.Background(), in); err != nil {
			t.Fatalf("composed assembly: %v", err)
		}
		if len(inner.history) != 2 {
			t.Fatalf("history len = %d, want the flow endpoint to drop the foreign block", len(inner.history))
		}
		if countSummary(inner.history, "") != 0 && inner.history[0].Role == types.RoleSystem {
			t.Fatal("a foreign opaque block rendered a summary head")
		}
	})
}

// collapseProbe adapts a probe function to the stores.Projection seam.
type collapseProbe struct {
	fn func(stores.History) error
}

func (p collapseProbe) Project(_ context.Context, h stores.History, _ types.ModelProfile) (stores.History, error) {
	return h, p.fn(h)
}
