package types

import "context"

// TokenBudget is a computed budget over the context window.
type TokenBudget struct {
	Limit     int
	Reserved  int
	Estimated int
	Ratio     float64
}

// TokenEstimator predicts a request's token cost before it is sent. It must
// be deterministic for identical input.
type TokenEstimator interface {
	Estimate(req ModelRequest, caps Caps) int
}

// TokenCounter is the optional exact-count port a Model may back with a
// provider count endpoint. The harness consults it only at the compaction
// decision, never per request.
type TokenCounter interface {
	Count(ctx context.Context, req ModelRequest) (int, error)
}
