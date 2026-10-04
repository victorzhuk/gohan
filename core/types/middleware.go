package types

import (
	"context"
	"iter"
)

// ToolFunc invokes one tool and streams nothing back: one result, one error.
type ToolFunc func(ctx context.Context, call ToolUse) (ToolResult, error)

// ToolMiddleware wraps one tool invocation.
type ToolMiddleware func(next ToolFunc) ToolFunc

// ModelFunc streams one model call: zero or more chunks, one final error.
type ModelFunc func(ctx context.Context, req ModelRequest) iter.Seq2[ModelChunk, error]

// ModelMiddleware wraps one model invocation.
type ModelMiddleware func(next ModelFunc) ModelFunc
