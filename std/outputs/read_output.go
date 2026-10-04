package outputs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Span is the byte window read_output returns. Limit 0 means to the end.
type Span struct {
	Offset int64 `json:"offset"`
	Limit  int64 `json:"limit"`
}

type readOutputArgs struct {
	Ref   string `json:"ref"`
	Range *Span  `json:"range,omitempty"`
}

// NewReadOutput builds the harness read_output tool: it pages through
// content the output store holds, so a truncated tool result can be read
// back as head excerpt plus Ref (working-state, OutputStore). The name is
// reserved for the harness; registering a user tool with it fails Build.
func NewReadOutput(store stores.OutputStore) (types.Tool, error) {
	return gohan.NewTool("read_output",
		"Read a stored output by ref, optionally limited to a byte range",
		func(ctx context.Context, args readOutputArgs) (types.ToolResult, error) {
			data, err := store.Get(ctx, args.Ref)
			if err != nil {
				return types.ToolResult{}, fmt.Errorf("read_output: %w", err)
			}
			var msg types.Message
			if err := json.Unmarshal(data, &msg); err != nil {
				return types.ToolResult{}, fmt.Errorf("read_output: decode stored blocks: %w", err)
			}
			content := &strings.Builder{}
			for _, b := range msg.Blocks {
				if txt, ok := b.(types.Text); ok {
					content.WriteString(txt.Text)
				}
			}
			full := content.String()
			start, end := 0, len(full)
			if args.Range != nil {
				start = int(min(args.Range.Offset, int64(end)))
				if start < 0 {
					start = 0
				}
				if args.Range.Limit > 0 {
					end = int(min(int64(start)+args.Range.Limit, int64(end)))
				}
			}
			return types.ToolResult{
				Content: []types.Block{types.Text{Text: full[start:end]}},
				Outcome: types.Succeeded,
			}, nil
		},
		gohan.WithEffect(types.ReadOnly),
	)
}
