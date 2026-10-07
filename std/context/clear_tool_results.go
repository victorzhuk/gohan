package context

import (
	"context"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// clearedToolResultText replaces cleared tool result content. It is a
// constant so the cleared-token arithmetic and the request bytes stay
// identical across processes.
const clearedToolResultText = "[tool result cleared]"

// alwaysExcluded names tools whose results a clearing never touches.
var alwaysExcluded = map[string]struct{}{
	"notes_read":  {},
	"notes_write": {},
}

// ClearToolResults replaces the content of tool results that fall outside
// the last Keep tool uses of the history with a fixed placeholder. It
// implements the Projection contract: identity, outcome, error and origin
// survive; Version is carried over; the input history is left untouched.
type ClearToolResults struct {
	Keep         int
	ClearAtLeast int
	Exclude      []string

	estimator types.TokenEstimator
	ratio     float64
}

// ClearToolResultsOption configures a ClearToolResults projection.
type ClearToolResultsOption func(*ClearToolResults)

// WithClearEstimator sets the TokenEstimator consulted at the clearing
// decision, mirroring the ContextBudget counting rule.
func WithClearEstimator(est types.TokenEstimator) ClearToolResultsOption {
	return func(c *ClearToolResults) { c.estimator = est }
}

// WithClearRatio sets the calibration ratio applied to the context window
// before the limit is derived, as ContextBudget does for compaction.
func WithClearRatio(ratio float64) ClearToolResultsOption {
	return func(c *ClearToolResults) { c.ratio = ratio }
}

// NewClearToolResults builds a ClearToolResults projection with options
// applied; Keep, ClearAtLeast and Exclude are set on the returned value.
func NewClearToolResults(opts ...ClearToolResultsOption) ClearToolResults {
	var c ClearToolResults
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// Project returns a copy of the history whose eligible tool results outside
// the retained window carry the placeholder. Every ToolUse block and every
// ToolResult keeps its position; results of tools in Exclude, and of the
// harness notes tools always, stay intact. When the positive difference
// between the original and the candidate estimate is below ClearAtLeast, or
// no estimator is declared, the input history is returned unchanged.
func (c ClearToolResults) Project(_ context.Context, h stores.History, p types.ModelProfile) (stores.History, error) {
	if c.estimator == nil || c.Keep <= 0 || len(h.Messages) == 0 {
		return h, nil
	}
	nameByID, useIDs := scanUses(h.Messages)
	if len(useIDs) <= c.Keep {
		return h, nil
	}
	retained := make(map[string]bool, c.Keep)
	for _, id := range useIDs[len(useIDs)-c.Keep:] {
		retained[id] = true
	}
	excluded := c.excluded()
	out := make([]types.Message, len(h.Messages))
	copy(out, h.Messages)
	changed := false
	for i := range out {
		blocks, replaced := clearBlocks(out[i].Blocks, nameByID, retained, excluded)
		if replaced {
			out[i].Blocks = blocks
			changed = true
		}
	}
	if !changed {
		return h, nil
	}
	orig := types.ContextBudget(p, types.ModelRequest{Messages: h.Messages}, c.estimator, c.ratio).Estimated
	cand := types.ContextBudget(p, types.ModelRequest{Messages: out}, c.estimator, c.ratio).Estimated
	if cleared := orig - cand; cleared <= 0 || cleared < c.ClearAtLeast {
		return h, nil
	}
	return stores.History{
		Owner:      h.Owner,
		Messages:   out,
		Version:    h.Version,
		ForkedFrom: h.ForkedFrom,
	}, nil
}

// scanUses records every tool use in history order, pending uses included:
// a use without a result still occupies a slot in the retained window.
func scanUses(msgs []types.Message) (map[string]string, []string) {
	nameByID := make(map[string]string)
	var ids []string
	for _, m := range msgs {
		for _, b := range m.Blocks {
			u, ok := b.(types.ToolUse)
			if !ok {
				continue
			}
			nameByID[u.ID] = u.Name
			ids = append(ids, u.ID)
		}
	}
	return nameByID, ids
}

func (c ClearToolResults) excluded() map[string]bool {
	ex := make(map[string]bool, len(c.Exclude)+len(alwaysExcluded))
	for _, n := range c.Exclude {
		ex[n] = true
	}
	for n := range alwaysExcluded {
		ex[n] = true
	}
	return ex
}

// clearBlocks never clears a result whose tool name is excluded; the tool
// is found through the use with the matching ID.
func clearBlocks(blocks []types.Block, nameByID map[string]string, retained map[string]bool, excluded map[string]bool) ([]types.Block, bool) {
	out := blocks
	replaced := false
	for i, b := range out {
		r, ok := b.(types.ToolResult)
		if !ok || retained[r.ID] || excluded[nameByID[r.ID]] {
			continue
		}
		if !replaced {
			out = make([]types.Block, len(blocks))
			copy(out, blocks)
			replaced = true
		}
		out[i] = clearResult(r)
	}
	return out, replaced
}

// clearResult replaces the result's content with the placeholder and keeps
// the identity, outcome, error and origin of the block it replaced.
func clearResult(r types.ToolResult) types.ToolResult {
	r.Content = []types.Block{types.Text{BlockBase: r.BlockBase, Text: clearedToolResultText}}
	return r
}
