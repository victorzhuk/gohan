package gohan

import (
	"context"

	"github.com/victorzhuk/gohan/core/types"
)

// RequestHumanName is the harness-reserved name of the handoff tool.
const RequestHumanName = "request_human"

type requestHumanArgs struct {
	Reason string            `json:"reason" desc:"why the model is handing the conversation to a human"`
	Skill  string            `json:"skill" desc:"optional skill the operator needs"`
	Fields map[string]string `json:"fields" desc:"optional free-form detail for the operator"`
}

type requestHumanResult struct{}

// NewRequestHumanTool builds the governed request_human tool. Its call
// never renders as a ToolResult: it returns the terminal HandoffError the
// lifecycle closes the run with.
func NewRequestHumanTool() (types.Tool, error) {
	return NewTool(RequestHumanName,
		"Hand the conversation to a human operator. The run ends; no further model turns happen.",
		func(ctx context.Context, args requestHumanArgs) (requestHumanResult, error) {
			return requestHumanResult{}, &types.HandoffError{Context: types.HandoffContext{
				Reason: args.Reason,
				Skill:  args.Skill,
				Fields: args.Fields,
			}}
		})
}
