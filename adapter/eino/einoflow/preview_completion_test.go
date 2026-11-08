package einoflow

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/victorzhuk/gohan/core/types"
)

// previewChunks scripts one model pass the way a streaming backend speaks:
// identity-only DeltaToolArgs previews first, then the complete call, then
// a legacy nil-Args non-preview call.
var previewChunks = []types.ModelChunk{
	{Kind: types.DeltaToolArgs, Delta: "f0", ToolUse: &types.ToolUse{ID: "call_prev_1", Name: "ping"}},
	{Kind: types.DeltaToolArgs, Delta: "f1", ToolUse: &types.ToolUse{ID: "call_prev_1", Name: "ping"}},
	{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "call_complete_1", Name: "ping", Args: jsontext.Value(`{"n":"1"}`)}, Finish: types.FinishToolUse},
	{ToolUse: &types.ToolUse{ID: "call_legacy_1", Name: "legacy"}},
}

func TestGraphPreviewCompletion(t *testing.T) {
	t.Run("generate-collector-folds-previews-out", func(t *testing.T) {
		node := ModelNode(&scriptGraphModel{chunks: previewChunks})
		msg, err := node.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if len(msg.ToolCalls) != 2 {
			t.Fatalf("Generate folded %d tool calls (%+v), want 2: identity-only previews must never become executable calls", len(msg.ToolCalls), msg.ToolCalls)
		}
		first := msg.ToolCalls[0]
		if first.ID != "call_complete_1" || first.Function.Name != "ping" || first.Function.Arguments != `{"n":"1"}` {
			t.Fatalf("first folded call = %+v, want the complete call under call_complete_1 with its args", first)
		}
		second := msg.ToolCalls[1]
		if second.ID != "call_legacy_1" || second.Function.Name != "legacy" {
			t.Fatalf("second folded call = %+v, want the legacy nil-Args non-preview call preserved", second)
		}
	})

	t.Run("stream-collector-folds-previews-out", func(t *testing.T) {
		node := ModelNode(&scriptGraphModel{chunks: previewChunks})
		sr, err := node.Stream(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		defer sr.Close()
		complete, legacy := 0, 0
		for {
			msg, err := sr.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				t.Fatalf("stream recv: %v", err)
			}
			for _, c := range msg.ToolCalls {
				switch c.ID {
				case "call_prev_1":
					t.Errorf("preview leaked into the schema message as %+v, want identity-only previews folded out", c)
				case "call_complete_1":
					complete++
					if c.Function.Name != "ping" || c.Function.Arguments != `{"n":"1"}` {
						t.Errorf("complete call = %+v, want ping with its args", c)
					}
				case "call_legacy_1":
					legacy++
					if c.Function.Name != "legacy" {
						t.Errorf("legacy call = %+v, want the legacy nil-Args call preserved", c)
					}
				}
			}
		}
		if complete != 1 {
			t.Fatalf("complete call reached the schema message %d times, want exactly once", complete)
		}
		if legacy != 1 {
			t.Fatalf("legacy nil-Args call reached the schema message %d times, want exactly once", legacy)
		}
	})
}
