package outputs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestReadOutput(t *testing.T) {
	t.Run("working-state.output-paging", func(t *testing.T) {
		ctx := context.Background()
		store := stores.NewMemoryOutputs()
		tool, err := NewReadOutput(store)
		if err != nil {
			t.Fatalf("build read_output: %v", err)
		}

		head := "head-excerpt"
		tail := strings.Repeat("x", 4096)
		ref, err := store.Put(ctx, types.RunInfo{SessionID: "s1"}, []types.Block{
			types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool}}, Text: head + tail},
		})
		if err != nil {
			t.Fatalf("put: %v", err)
		}

		res, err := tool.Call(ctx, json.RawMessage(`{"ref":"`+ref+`"}`))
		if err != nil {
			t.Fatalf("call without range: %v", err)
		}
		if res.Outcome != types.Succeeded {
			t.Fatalf("outcome = %v, want Succeeded", res.Outcome)
		}
		if got := textOf(t, res); got != head+tail {
			t.Fatalf("full read returned %d bytes, want the whole content", len(got))
		}

		res, err = tool.Call(ctx, json.RawMessage(fmt.Sprintf(
			`{"ref":%q,"range":{"offset":%d,"limit":16}}`, ref, len(head))))
		if err != nil {
			t.Fatalf("call with range: %v", err)
		}
		if got := textOf(t, res); got != tail[:16] {
			t.Fatalf("ranged read returned %q, want the requested slice", got)
		}

		res, err = tool.Call(ctx, json.RawMessage(`{"ref":"unknown-ref"}`))
		if err == nil {
			t.Fatal("unknown ref: want an error the call path renders as a failure")
		}
	})
}

func textOf(t *testing.T, res types.ToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("content has %d blocks, want one Text block", len(res.Content))
	}
	txt, ok := res.Content[0].(types.Text)
	if !ok {
		t.Fatalf("block is %T, want types.Text", res.Content[0])
	}
	return txt.Text
}
