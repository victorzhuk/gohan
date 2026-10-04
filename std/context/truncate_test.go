package context

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// textEstimator charges a fixed cost per message plus one token per byte of
// Text block content, so tests can size histories exactly.
type textEstimator struct{}

func (textEstimator) Estimate(req types.ModelRequest, _ types.Caps) int {
	n := 10 * len(req.Messages)
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			if t, ok := b.(types.Text); ok {
				n += len(t.Text)
			}
		}
	}
	return n
}

func textTurn(n int) []types.Message {
	content := strings.Repeat("a", n)
	return []types.Message{
		{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: content}}},
		{Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: "ok " + content}}},
	}
}

func toolTurn(id string) []types.Message {
	return []types.Message{
		{Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: id, Name: "search"}}},
		{Role: types.RoleUser, Blocks: []types.Block{types.ToolResult{ID: id, Outcome: types.Succeeded}}},
	}
}

func history(msgs ...[]types.Message) stores.History {
	var h stores.History
	for _, turn := range msgs {
		h.Messages = append(h.Messages, turn...)
	}
	h.Version = 7
	return h
}

func profile(window int) types.ModelProfile {
	return types.ModelProfile{Name: "m", ContextWindow: window}
}

func TestTruncate(t *testing.T) {
	t.Run("assembly.truncate-policy", func(t *testing.T) {
		h := history(textTurn(100), textTurn(100), textTurn(100))
		before := len(h.Messages)
		out, err := NewTruncate(WithTruncateEstimator(textEstimator{})).Project(context.Background(), h, profile(200))
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Messages) >= before {
			t.Fatalf("projection kept %d of %d messages, want a strict subset", len(out.Messages), before)
		}
		if got := len(out.Messages); got == 0 || got%2 != 0 {
			t.Fatalf("kept %d messages; whole turns come in pairs", got)
		}
		if h.Version != 7 {
			t.Fatalf("Version changed to %d", h.Version)
		}
	})

	t.Run("model.context-re-fit-on-fallback", func(t *testing.T) {
		tr := NewTruncate(WithTruncateEstimator(textEstimator{}))
		h := history(textTurn(100), textTurn(100), textTurn(100), textTurn(100))
		primary, err := tr.Project(context.Background(), h, profile(700))
		if err != nil {
			t.Fatal(err)
		}
		fallback, err := tr.Project(context.Background(), h, profile(200))
		if err != nil {
			t.Fatal(err)
		}
		if len(fallback.Messages) >= len(primary.Messages) {
			t.Fatalf("fallback kept %d messages, primary kept %d; fallback must re-fit tighter", len(fallback.Messages), len(primary.Messages))
		}
	})

	t.Run("whole-turns-survive", func(t *testing.T) {
		h := history(textTurn(100), textTurn(100), toolTurn("t1"), textTurn(100))
		out, err := NewTruncate(WithTruncateEstimator(textEstimator{})).Project(context.Background(), h, profile(300))
		if err != nil {
			t.Fatal(err)
		}
		seenUse, seenResult := false, false
		for _, m := range out.Messages {
			for _, b := range m.Blocks {
				switch b.(type) {
				case types.ToolUse:
					seenUse = true
				case types.ToolResult:
					seenResult = true
				}
			}
		}
		if seenUse != seenResult {
			t.Fatalf("tool pair split: use=%v result=%v", seenUse, seenResult)
		}
	})

	t.Run("prefix-intact", func(t *testing.T) {
		first := textTurn(100)
		h := history(first, textTurn(100), textTurn(100))
		out, err := NewTruncate(WithTruncateEstimator(textEstimator{})).Project(context.Background(), h, profile(200))
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Messages) < len(first) {
			t.Fatal("cached-prefix anchor turn was dropped")
		}
		for i, m := range first {
			if !reflect.DeepEqual(out.Messages[i], m) {
				t.Fatalf("prefix message %d altered", i)
			}
		}
	})

	t.Run("over-budget-history-shrinks", func(t *testing.T) {
		h := history(textTurn(100), textTurn(100), textTurn(100))
		out, err := NewTruncate(WithTruncateEstimator(textEstimator{})).Project(context.Background(), h, profile(200))
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Messages) == len(h.Messages) {
			t.Fatal("over-budget history was not shrunk")
		}
	})

	t.Run("smaller-profile-drops-more", func(t *testing.T) {
		tr := NewTruncate(WithTruncateEstimator(textEstimator{}))
		h := history(textTurn(100), textTurn(100), textTurn(100), textTurn(100))
		big, err := tr.Project(context.Background(), h, profile(600))
		if err != nil {
			t.Fatal(err)
		}
		small, err := tr.Project(context.Background(), h, profile(200))
		if err != nil {
			t.Fatal(err)
		}
		if len(small.Messages) >= len(big.Messages) {
			t.Fatalf("smaller window kept %d, larger kept %d", len(small.Messages), len(big.Messages))
		}
	})

	t.Run("session-log-unchanged", func(t *testing.T) {
		h := history(textTurn(100), textTurn(100))
		snapshot := append([]types.Message(nil), h.Messages...)
		out, err := NewTruncate(WithTruncateEstimator(textEstimator{})).Project(context.Background(), h, profile(100))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(h.Messages, snapshot) {
			t.Fatal("projection mutated the session log's messages")
		}
		if out.Version != h.Version || len(out.Messages) == len(h.Messages) {
			t.Fatalf("projected view wrong: version %d, %d of %d messages", out.Version, len(out.Messages), len(h.Messages))
		}
		bare, err := NewTruncate().Project(context.Background(), h, profile(100))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(bare.Messages, h.Messages) {
			t.Fatal("projection without an estimator must return the history unchanged")
		}
	})

	t.Run("pending-tool-use-kept", func(t *testing.T) {
		h := history(textTurn(100), textTurn(100), []types.Message{
			{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}}},
			{Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: "t9", Name: "search"}}},
		})
		out, err := NewTruncate(WithTruncateEstimator(textEstimator{})).Project(context.Background(), h, profile(200))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range out.Messages {
			for _, b := range m.Blocks {
				if use, ok := b.(types.ToolUse); ok && use.ID == "t9" {
					raised, err := NewTruncate(WithTruncateEstimator(textEstimator{}), WithTruncateRatio(2.5)).Project(context.Background(), h, profile(200))
					if err != nil {
						t.Fatal(err)
					}
					if len(raised.Messages) <= len(out.Messages) {
						t.Fatalf("raised ratio kept %d, default kept %d", len(raised.Messages), len(out.Messages))
					}
					return
				}
			}
		}
		t.Fatal("pending ToolUse turn was dropped")
	})
}
