package gohan

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/victorzhuk/gohan/core/types"
)

// outBlocks maps fn's result onto the model-visible content per the tool
// author contract: a []Block and a ToolResult pass through unchanged, a
// string becomes one Text block, a Quarantined becomes one nested block
// per distinct origin, anything else is marshalled to JSON in one Text
// block.
func outBlocks[Out any](out Out) (types.ToolResult, error) {
	switch v := any(out).(type) {
	case []types.Block:
		return types.ToolResult{Content: v, Outcome: types.Succeeded}, nil
	case types.ToolResult:
		return v, nil
	case string:
		return textResult(v), nil
	}
	// outBlocks is instantiated with Out already carrying the Quarantined
	// type arguments, so a Quarantined[Out] type case would nest the
	// instantiation and can never match; the shape is detected once here.
	if rt := reflect.TypeOf(any(out)); rt != nil && rt.PkgPath() == "github.com/victorzhuk/gohan/core/types" && strings.HasPrefix(rt.Name(), "Quarantined") {
		rv := reflect.ValueOf(out)
		if rv.NumField() < 2 || rv.Field(1).Type() != reflect.TypeFor[[]types.Origin]() {
			return types.ToolResult{}, fmt.Errorf("gohan: tool result: unexpected Quarantined shape %s", rt)
		}
		return quarantinedBlocks(
			rv.Field(0).Interface(),
			rv.Field(1).Interface().([]types.Origin),
		)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("gohan: tool result: %w", err)
	}
	return textResult(string(encoded)), nil
}

// quarantinedBlocks renders a quarantined child output: canonical JSON of
// the value in one nested block per distinct origin, mirroring
// RecordQuarantined.
func quarantinedBlocks(value any, origins []types.Origin) (types.ToolResult, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("gohan: tool result: %w", err)
	}
	if len(origins) == 0 {
		return textResult(string(raw)), nil
	}
	blocks := make([]types.Block, 0, len(origins))
	seen := make(map[types.Origin]bool, len(origins))
	for _, o := range origins {
		if seen[o] {
			continue
		}
		seen[o] = true
		blocks = append(blocks, types.ToolResult{
			BlockBase: types.BlockBase{Origin: o},
			Content:   []types.Block{types.Text{Text: string(raw)}},
			Outcome:   types.Succeeded,
		})
	}
	return types.ToolResult{Content: blocks, Outcome: types.Succeeded}, nil
}

func textResult(s string) types.ToolResult {
	return types.ToolResult{
		Content: []types.Block{types.Text{Text: s}},
		Outcome: types.Succeeded,
	}
}
