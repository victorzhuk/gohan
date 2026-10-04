package types

import (
	"context"
)

// ToolFunc invokes one tool and streams nothing back: one result, one error.
type ToolFunc func(ctx context.Context, call ToolUse) (ToolResult, error)

// ToolMiddleware wraps one tool invocation.
type ToolMiddleware func(next ToolFunc) ToolFunc
