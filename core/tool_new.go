package gohan

import (
	"encoding/json"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// outBlocks maps fn's result onto the model-visible content per the tool
// author contract: a []Block and a ToolResult pass through unchanged, a
// string becomes one Text block, anything else is marshalled to JSON in
// one Text block.
func outBlocks[Out any](out Out) (types.ToolResult, error) {
	switch v := any(out).(type) {
	case []types.Block:
		return types.ToolResult{Content: v, Outcome: types.Succeeded}, nil
	case types.ToolResult:
		return v, nil
	case string:
		return textResult(v), nil
	default:
		encoded, err := json.Marshal(out)
		if err != nil {
			return types.ToolResult{}, fmt.Errorf("gohan: tool result: %w", err)
		}
		return textResult(string(encoded)), nil
	}
}

func textResult(s string) types.ToolResult {
	return types.ToolResult{
		Content: []types.Block{types.Text{Text: s}},
		Outcome: types.Succeeded,
	}
}
