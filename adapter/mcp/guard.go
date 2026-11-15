package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorzhuk/gohan/core/types"
)

// AnnotationCap folds a tool's declared annotations into an effect ceiling.
// A tool that declares itself read-only is capped at ReadOnly; a tool that
// declares both non-destructive and idempotent is capped at Idempotent.
// Annotations never raise an effect, they only lower it.
func AnnotationCap(a *mcp.ToolAnnotations) (types.Effect, bool) {
	if a == nil {
		return 0, false
	}
	if a.ReadOnlyHint {
		return types.ReadOnly, true
	}
	if a.DestructiveHint != nil && !*a.DestructiveHint && a.IdempotentHint {
		return types.Idempotent, true
	}
	return 0, false
}

func minEffect(a, b types.Effect) types.Effect {
	if a < b {
		return a
	}
	return b
}
