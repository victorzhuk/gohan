package types

import "context"

// The margin and rounding here are frozen by row 19.2, which has no spec
// formula to lean on: margin is half the request's MaxTokens, truncated
// toward zero; Reserved is MaxTokens plus that margin; Limit is the usable
// window minus Reserved and never negative. A non-positive ratio means the
// full context window; otherwise the window is scaled and truncated.

// ContextBudget derives the input ceiling for one request from the profile's
// context window by reserving the output allowance and the margin. The
// optional estimator, when given, is consulted exactly here — at the
// compaction decision — and never on the ordinary request path; a nil
// estimator leaves Estimated at zero.
func ContextBudget(p ModelProfile, req ModelRequest, est TokenEstimator, ratio float64) TokenBudget {
	window := p.ContextWindow
	if ratio > 0 {
		window = int(float64(window) * ratio)
	}
	maxTokens := req.Options.MaxTokens
	margin := maxTokens / 2
	b := TokenBudget{
		Reserved: maxTokens + margin,
	}
	b.Limit = window - b.Reserved
	if b.Limit < 0 {
		b.Limit = 0
	}
	if est != nil {
		b.Estimated = est.Estimate(req, p.Caps)
	}
	if b.Limit > 0 {
		b.Ratio = float64(b.Estimated) / float64(b.Limit)
	}
	return b
}

// Over reports whether the estimate exceeds the input ceiling.
func (b TokenBudget) Over() bool {
	return b.Estimated > b.Limit
}

// NeedsCompaction is the compaction decision. It consults the model's
// TokenCounter exactly once and only here; a model without the port, or a
// failed count, never compacts.
func NeedsCompaction(ctx context.Context, p ModelProfile, req ModelRequest, m Model) (bool, error) {
	counter, ok := m.(TokenCounter)
	if !ok {
		return false, ErrNoTokenEstimator
	}
	n, err := counter.Count(ctx, req)
	if err != nil {
		return false, err
	}
	budget := ContextBudget(p, req, nil, 0)
	return n > budget.Limit, nil
}
