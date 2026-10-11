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
		out := collapseCompactionHistory(messages)
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
		out := collapseCompactionHistory(messages)
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
		out := collapseCompactionHistory(messages)
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
		out := collapseCompactionHistory(messages)
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
		out := collapseCompactionHistory(messages)
		if len(out) != 2 || out[1].ID != "mpending" {
			t.Fatal("pending call after the boundary did not survive")
		}
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
		composed := collapseBeforeProjections(withProjections(stores.ContextPolicy{}, types.ModelProfile{Name: "p"}, inner.assemble))
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
		composed := collapseBeforeProjections(withProjections(policy, types.ModelProfile{Name: "p"}, inner.assemble))
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

// collapseProbe adapts a probe function to the stores.Projection seam.
type collapseProbe struct {
	fn func(stores.History) error
}

func (p collapseProbe) Project(_ context.Context, h stores.History, _ types.ModelProfile) (stores.History, error) {
	return h, p.fn(h)
}
