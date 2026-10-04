package types

import (
	"context"
	"iter"
)

// Model is the port every provider adapter implements. A value is built once
// and shared by every run: Generate must be safe for concurrent use, and
// per-request state comes only from ctx and req.
type Model interface {
	Profile() ModelProfile
	Generate(ctx context.Context, req ModelRequest) iter.Seq2[ModelChunk, error]
}
