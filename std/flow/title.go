package flow

import (
	"context"
	"encoding/json"
	"strings"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
)

// titleInstruction is the one model-facing sentence the title recipe adds
// ahead of the conversation blocks.
const titleInstruction = "Write a title for this conversation in at most six words. Answer with a JSON string."

// titleSchema is the response schema Title pins: Extract derives its schema
// from Out, and a string Out has no struct schema to derive.
var titleSchema = json.RawMessage(`{"type":"string"}`)

// Title returns a Flow that names a conversation: one governed model call
// over the session's first exchange, constrained to at most six words. No
// tool is ever offered. The instruction rides as a system block ahead of
// the input, so the recipe's schema and validation stay Extract's own.
func Title(s *gohan.Stack, profile string, opts ...Option) gohan.Flow[[]types.Block, string] {
	inner := Extract[string](s, profile, append([]Option{WithSchema(titleSchema)}, opts...)...)
	return gohan.FlowFunc[[]types.Block, string]("flow.title", func(ctx context.Context, in []types.Block) (string, error) {
		out, err := inner.Invoke(ctx, append([]types.Block{titleBlock()}, in...))
		if err != nil {
			return "", err
		}
		return clipTitle(out), nil
	})
}

func titleBlock() types.Text {
	return types.Text{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginSystem}},
		Text:      titleInstruction,
	}
}

// clipTitle enforces the six-word ceiling the recipe promises even when a
// model ignores the instruction.
func clipTitle(s string) string {
	words := strings.Fields(s)
	if len(words) <= 6 {
		return strings.TrimSpace(s)
	}
	return strings.Join(words[:6], " ")
}
