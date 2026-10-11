package outputs

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

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
	schema, err := types.NewSchemaBuilder().Schema(reflect.TypeFor[readOutputArgs]())
	if err != nil {
		return nil, fmt.Errorf("gohan: tool read_output schema: %w", err)
	}
	return &readOutputTool{
		store: store,
		spec: types.ToolSpec{
			Name:        "read_output",
			Description: "Read a stored output by ref, optionally limited to a byte range",
			Schema:      schema,
			Effect:      types.ReadOnly,
			Risk:        types.RiskLow,
			MaxOutput:   DefaultMaxOutput,
			Timeout:     10 * time.Second,
		},
	}, nil
}

type readOutputTool struct {
	store stores.OutputStore
	spec  types.ToolSpec
}

func (t *readOutputTool) Spec() types.ToolSpec { return t.spec }

func (t *readOutputTool) Call(ctx context.Context, raw json.RawMessage) (types.ToolResult, error) {
	var args readOutputArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return types.ToolResult{}, fmt.Errorf("gohan: tool arguments: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, t.spec.Timeout)
	defer cancel()
	data, err := t.store.Get(ctx, args.Ref)
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
}
