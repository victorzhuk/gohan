// Package context provides assembly-time projections over session history.
// A projection is a view: it returns a reduced History for one request and
// never writes to the SessionLog. Persisted compaction is a separate stage.
package context

import (
	"context"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Truncate drops the oldest droppable turns from a History until the
// estimated request size fits the profile's ContextBudget limit. It
// implements the Projection contract: deterministic for identical
// (History.Version, ModelProfile), never persisted.
//
// The estimator measures one message at a time; without one the projection
// never fires and returns the history unchanged.
type Truncate struct {
	estimator types.TokenEstimator
	ratio     float64
}

// TruncateOption configures a Truncate projection.
type TruncateOption func(*Truncate)

// WithTruncateEstimator sets the TokenEstimator consulted at the truncation
// decision, mirroring the ContextBudget counting rule.
func WithTruncateEstimator(est types.TokenEstimator) TruncateOption {
	return func(t *Truncate) { t.estimator = est }
}

// WithTruncateRatio sets the calibration ratio applied to the context window
// before the limit is derived, as ContextBudget does for compaction.
func WithTruncateRatio(ratio float64) TruncateOption {
	return func(t *Truncate) { t.ratio = ratio }
}

// NewTruncate builds a Truncate projection.
func NewTruncate(opts ...TruncateOption) Truncate {
	var t Truncate
	for _, opt := range opts {
		opt(&t)
	}
	return t
}

// Project returns a copy of the history whose oldest droppable turns are
// dropped until the estimate fits the budget limit. The first turn is the
// cached-prefix anchor and is never dropped: everything before the caching
// boundary must survive a projection. A turn holding a ToolUse without its
// result is not droppable. The input history and its messages are left
// untouched; Version is carried over unchanged.
func (t Truncate) Project(_ context.Context, h stores.History, p types.ModelProfile) (stores.History, error) {
	if t.estimator == nil || len(h.Messages) == 0 {
		return h, nil
	}
	if !t.over(h, p) {
		return h, nil
	}
	turns := splitTurns(h.Messages)
	keep := make([]bool, len(turns))
	for i := range keep {
		keep[i] = true
	}
	est := 0
	for _, turn := range turns {
		for _, m := range turn {
			est += t.estimate(m, p)
		}
	}
	for i := len(turns) - 1; i > 0 && est > t.limit(p); i-- {
		if keep[i] && !droppable(turns[i]) {
			continue
		}
		for _, m := range turns[i] {
			est -= t.estimate(m, p)
		}
		keep[i] = false
	}
	out := make([]types.Message, 0, len(h.Messages))
	for i, turn := range turns {
		if keep[i] {
			out = append(out, turn...)
		}
	}
	return stores.History{
		Owner:      h.Owner,
		Messages:   out,
		Version:    h.Version,
		ForkedFrom: h.ForkedFrom,
	}, nil
}

func (t Truncate) limit(p types.ModelProfile) int {
	return types.ContextBudget(p, types.ModelRequest{}, t.estimator, t.ratio).Limit
}

func (t Truncate) over(h stores.History, p types.ModelProfile) bool {
	req := types.ModelRequest{Messages: h.Messages}
	return types.ContextBudget(p, req, t.estimator, t.ratio).Over()
}

func (t Truncate) estimate(m types.Message, p types.ModelProfile) int {
	return t.estimator.Estimate(types.ModelRequest{Messages: []types.Message{m}}, p.Caps)
}

// splitTurns groups messages into turns: one user message plus every
// following non-user message. Assistant messages, their tool results and
// system interjections travel together, so a turn is never split.
func splitTurns(msgs []types.Message) [][]types.Message {
	var turns [][]types.Message
	for _, m := range msgs {
		if m.Role == types.RoleUser || len(turns) == 0 {
			turns = append(turns, []types.Message{m})
			continue
		}
		turns[len(turns)-1] = append(turns[len(turns)-1], m)
	}
	return turns
}

// droppable reports whether a turn may leave the view. A ToolUse whose
// result is not in the same turn is pending: the pair is covered together
// or not at all.
func droppable(turn []types.Message) bool {
	var uses, results int
	for _, m := range turn {
		for _, b := range m.Blocks {
			switch b.(type) {
			case types.ToolUse:
				uses++
			case types.ToolResult:
				results++
			}
		}
	}
	return uses == results
}
