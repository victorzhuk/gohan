package gohantest

import (
	"github.com/victorzhuk/gohan/core/types"
)

// ToolUses streams several tool-use blocks in one turn and finishes with
// tool_use, so a suite can script a multi-call model message.
func ToolUses(uses ...types.ToolUse) Turn {
	chunks := make([]types.ModelChunk, 0, len(uses)+1)
	for i := range uses {
		call := uses[i]
		chunks = append(chunks, types.ModelChunk{ToolUse: &call})
	}
	chunks = append(chunks, types.ModelChunk{Finish: types.FinishToolUse})
	return Turn{chunks: chunks}
}
