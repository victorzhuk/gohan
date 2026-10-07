package context

import (
	"context"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// resultEstimator is textEstimator plus the Text blocks inside a
// ToolResult's content, so cleared results carry measurable cost.
type resultEstimator struct{}

func (resultEstimator) Estimate(req types.ModelRequest, _ types.Caps) int {
	n := 10 * len(req.Messages)
	var walk func(blocks []types.Block)
	walk = func(blocks []types.Block) {
		for _, b := range blocks {
			switch v := b.(type) {
			case types.Text:
				n += len(v.Text)
			case types.ToolResult:
				walk(v.Content)
			}
		}
	}
	for _, m := range req.Messages {
		walk(m.Blocks)
	}
	return n
}

// namedToolTurn pairs one use with one result carrying n bytes of text.
func namedToolTurn(id, name string, n int) []types.Message {
	return []types.Message{
		{Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: id, Name: name}}},
		{Role: types.RoleUser, Blocks: []types.Block{
			types.ToolResult{ID: id, Outcome: types.Succeeded, Content: []types.Block{types.Text{Text: strings.Repeat("r", n)}}},
		}},
	}
}

func toolHistory(ids ...string) stores.History {
	var h stores.History
	for _, id := range ids {
		h.Messages = append(h.Messages, namedToolTurn(id, "search", 100)...)
	}
	h.Version = 7
	return h
}

func clearProjection(keep, clearAtLeast int, opts ...ClearToolResultsOption) ClearToolResults {
	opts = append([]ClearToolResultsOption{
		WithClearEstimator(resultEstimator{}),
	}, opts...)
	c := NewClearToolResults(opts...)
	c.Keep = keep
	c.ClearAtLeast = clearAtLeast
	return c
}

func useBlocks(h stores.History) []types.ToolUse {
	var uses []types.ToolUse
	for _, m := range h.Messages {
		for _, b := range m.Blocks {
			if u, ok := b.(types.ToolUse); ok {
				uses = append(uses, u)
			}
		}
	}
	return uses
}

func resultBlocks(h stores.History) []types.ToolResult {
	var results []types.ToolResult
	for _, m := range h.Messages {
		for _, b := range m.Blocks {
			if r, ok := b.(types.ToolResult); ok {
				results = append(results, r)
			}
		}
	}
	return results
}

func isPlaceholder(r types.ToolResult) bool {
	if len(r.Content) != 1 {
		return false
	}
	t, ok := r.Content[0].(types.Text)
	return ok && t.Text == clearedToolResultText
}

func TestClearToolResults(t *testing.T) {
	t.Run("context.keep-last-n", func(t *testing.T) {
		in := toolHistory("t1", "t2", "t3", "t4", "t5", "t6")
		out, err := clearProjection(3, 100).Project(context.Background(), in, profile(10_000))
		if err != nil {
			t.Fatal(err)
		}
		if out.Version != in.Version || len(out.Messages) != len(in.Messages) {
			t.Fatalf("version = %d, messages = %d; want %d, %d", out.Version, len(out.Messages), in.Version, len(in.Messages))
		}
		uses := useBlocks(out)
		if len(uses) != 6 {
			t.Fatalf("tool uses = %d, want 6", len(uses))
		}
		results := resultBlocks(out)
		if len(results) != 6 {
			t.Fatalf("tool results = %d, want 6", len(results))
		}
		for i, r := range results {
			wantCleared := i < 3
			if got := isPlaceholder(r); got != wantCleared {
				t.Fatalf("result %d cleared = %v, want %v", i, got, wantCleared)
			}
		}
	})

	t.Run("context.notes-excluded", func(t *testing.T) {
		in := stores.History{Version: 7}
		for _, id := range []string{"t1", "t2", "t3", "t4", "t5"} {
			in.Messages = append(in.Messages, namedToolTurn(id, "search", 100)...)
		}
		in.Messages = append(in.Messages, namedToolTurn("n1", "notes_read", 100)...)
		// Six uses, notes result oldest-window candidate but excluded; give
		// the window a fifth-element edge by keeping only three.
		out, err := clearProjection(3, 100).Project(context.Background(), in, profile(10_000))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range resultBlocks(out) {
			if r.ID != "n1" {
				continue
			}
			if isPlaceholder(r) {
				t.Fatal("notes_read result was cleared")
			}
		}
		// An explicit Exclude naming notes_read changes nothing.
		c := clearProjection(3, 100)
		c.Exclude = []string{"notes_read"}
		out2, err := c.Project(context.Background(), in, profile(10_000))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range resultBlocks(out2) {
			if r.ID == "n1" && isPlaceholder(r) {
				t.Fatal("explicitly excluded notes_read result was cleared")
			}
		}
	})

	t.Run("context.clear-at-least", func(t *testing.T) {
		in := toolHistory("t1", "t2", "t3", "t4", "t5", "t6")
		// Three clearing candidates: 100 bytes of text each become the
		// placeholder, so the positive difference is fixed by the estimator.
		c := clearProjection(3, 10000)
		out, err := c.Project(context.Background(), in, profile(10_000))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range resultBlocks(out) {
			if isPlaceholder(r) {
				t.Fatal("below-threshold firing cleared a result")
			}
		}
		if len(out.Messages) != len(in.Messages) {
			t.Fatal("below-threshold firing changed the history")
		}
		orig := types.ContextBudget(profile(10_000), types.ModelRequest{Messages: in.Messages}, resultEstimator{}, 0).Estimated
		c2 := clearProjection(3, orig/4) // under the savings
		out2, err := c2.Project(context.Background(), in, profile(10_000))
		if err != nil {
			t.Fatal(err)
		}
		cleared := 0
		for _, r := range resultBlocks(out2) {
			if isPlaceholder(r) {
				cleared++
			}
		}
		if cleared == 0 {
			t.Fatal("firing above ClearAtLeast cleared nothing")
		}
	})
}

func TestClearToolResultsPlaceholderKeepsIdentity(t *testing.T) {
	in := toolHistory("t1", "t2", "t3", "t4")
	errKind := types.Permanent
	r := types.ToolResult{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "search"}},
		ID:        "t1",
		Outcome:   types.Failed,
		Error:     &types.ToolError{Kind: errKind, Message: "boom"},
		Ref:       "ref-1",
		Content:   []types.Block{types.Text{Text: strings.Repeat("x", 200)}},
	}
	in.Messages[1].Blocks = []types.Block{r}
	out, err := clearProjection(2, 1).Project(context.Background(), in, profile(10_000))
	if err != nil {
		t.Fatal(err)
	}
	var got types.ToolResult
	found := false
	for _, res := range resultBlocks(out) {
		if res.ID == "t1" {
			got, found = res, true
		}
	}
	if !found {
		t.Fatal("cleared result lost from history")
	}
	if got.Outcome != types.Failed || got.Ref != "ref-1" {
		t.Fatalf("outcome = %v, ref = %q; want Failed, ref-1", got.Outcome, got.Ref)
	}
	if got.Error == nil || got.Error.Kind != errKind || got.Error.Message != "boom" {
		t.Fatalf("error = %+v, want kind Permanent, message boom", got.Error)
	}
	if got.BlockOrigin() != (types.Origin{Kind: types.OriginTool, Name: "search"}) {
		t.Fatalf("origin = %v, want tool/search", got.BlockOrigin())
	}
	if !isPlaceholder(got) {
		t.Fatalf("content = %v, want placeholder", got.Content)
	}
}

func TestClearToolResultsEstimatorBinding(t *testing.T) {
	in := toolHistory("t1", "t2", "t3", "t4")
	c := NewClearToolResults(WithClearEstimator(resultEstimator{}), WithClearRatio(0.5))
	c.Keep, c.ClearAtLeast = 2, 1
	out, err := c.Project(context.Background(), in, profile(10_000))
	if err != nil {
		t.Fatal(err)
	}
	cleared := 0
	for _, r := range resultBlocks(out) {
		if isPlaceholder(r) {
			cleared++
		}
	}
	if cleared == 0 {
		t.Fatal("bound estimator did not fire a clearing")
	}
	if c.ratio != 0.5 || c.estimator == nil {
		t.Fatal("options did not bind")
	}
}

func TestClearToolResultsNoEstimator(t *testing.T) {
	in := toolHistory("t1", "t2", "t3", "t4")
	c := ClearToolResults{Keep: 2, ClearAtLeast: 1}
	out, err := c.Project(context.Background(), in, profile(10_000))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range resultBlocks(out) {
		if isPlaceholder(r) {
			t.Fatal("cleared without an estimator")
		}
	}
	if len(out.Messages) != len(in.Messages) {
		t.Fatal("changed the history without an estimator")
	}
}

func TestClearToolResultsBelowThresholdNoPartial(t *testing.T) {
	in := toolHistory("t1", "t2", "t3", "t4")
	before := len(in.Messages)
	c := clearProjection(2, 100_000)
	out, err := c.Project(context.Background(), in, profile(10_000))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Messages) != before {
		t.Fatalf("messages = %d, want %d", len(out.Messages), before)
	}
	for i, m := range out.Messages {
		if len(m.Blocks) != len(in.Messages[i].Blocks) {
			t.Fatalf("message %d blocks changed", i)
		}
		for j, b := range m.Blocks {
			if r, ok := b.(types.ToolResult); ok && isPlaceholder(r) {
				t.Fatalf("message %d block %d partially cleared", i, j)
			}
		}
	}
}
