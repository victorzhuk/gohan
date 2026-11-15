package flow

import (
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

// deepSchema nests five object levels, one past the depth-4 strict bound
// (structured-output.unrepresentable-schema).
const deepSchema = `{
  "type": "object",
  "properties": {
    "a": {
      "type": "object",
      "properties": {
        "b": {
          "type": "object",
          "properties": {
            "c": {
              "type": "object",
              "properties": {
                "d": {
                  "type": "object",
                  "properties": {"e": {"type": "string"}},
                  "required": ["e"],
                  "additionalProperties": false
                }
              },
              "required": ["d"],
              "additionalProperties": false
            }
          },
          "required": ["c"],
          "additionalProperties": false
        }
      },
      "required": ["b"],
      "additionalProperties": false
    }
  },
  "required": ["a"],
  "additionalProperties": false
}`

// quantity is the bounded type for the bounds-stay-validation scenario.
type quantity struct {
	Quantity int `json:"quantity" bounds:"min=1"`
}

func TestStructuredOutputHardening(t *testing.T) {
	t.Run("structured-output.tool-schema-fallback", func(t *testing.T) {
		m := newArgsModel("cheap", `{"total":0}`, `{"total":7}`)
		f := Extract[invoice](recipeStack(t, chains.PromptSet{}, m), "cheap")
		got, err := f.Invoke(gohanctx(), nil)
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if got.Total != 7 {
			t.Fatalf("result = %+v, want total 7 after one repair turn", got)
		}
		if m.calls != 2 {
			t.Fatalf("model calls = %d, want the one repair turn", m.calls)
		}
		req := m.requests[0]
		if len(req.Options.ResponseSchema) != 0 {
			t.Errorf("fallback request carried ResponseSchema %s, want none", req.Options.ResponseSchema)
		}
		if len(req.Tools) != 1 || req.Tools[0].Name != "gohan_respond" {
			t.Fatalf("tools = %v, want exactly gohan_respond", req.Tools)
		}
		if len(req.Tools[0].Schema) == 0 {
			t.Error("gohan_respond carried no schema")
		}
		if req.Options.ToolChoice != types.ToolChoiceAny {
			t.Errorf("tool choice = %q, want any", req.Options.ToolChoice)
		}

		spent := newArgsModel("cheap", `{"total":0}`, `{"total":0}`)
		spentf := Extract[invoice](recipeStack(t, chains.PromptSet{}, spent), "cheap")
		if _, err := spentf.Invoke(gohanctx(), nil); !errors.Is(err, types.ErrStructuredOutput) {
			t.Fatalf("err = %v, want ErrStructuredOutput after repairs are spent", err)
		}

		mixed := &scriptedModel{profile: types.ModelProfile{Name: "cheap"}}
		mixed.streams = [][]streamItem{
			{
				{chunk: types.ModelChunk{Kind: types.DeltaText, Delta: `{"total":`}},
				{chunk: types.ModelChunk{Kind: types.DeltaToolArgs, Delta: `{"total":0`}},
				{chunk: types.ModelChunk{Kind: types.DeltaToolArgs, Delta: "", ToolUse: &types.ToolUse{
					ID:   "call_mixed_1",
					Name: "gohan_respond",
					Args: jsontext.Value(`{"total":0}`),
				}}},
			},
			{
				{chunk: types.ModelChunk{Kind: types.DeltaToolArgs, Delta: `{"total":7}`}},
			},
		}
		mixedf := Extract[invoice](recipeStack(t, chains.PromptSet{}, mixed), "cheap")
		gotMixed, err := mixedf.Invoke(gohanctx(), nil)
		if err != nil {
			t.Fatalf("mixed Invoke: %v", err)
		}
		if gotMixed.Total != 7 {
			t.Fatalf("mixed result = %+v, want total 7 from the complete args", gotMixed)
		}
		if mixed.calls != 2 {
			t.Fatalf("mixed model calls = %d, want the one repair turn", mixed.calls)
		}
		var repairUse types.ToolUse
		var repairRes types.ToolResult
		for _, msg := range mixed.requests[1].Messages {
			for _, b := range msg.Blocks {
				switch bl := b.(type) {
				case types.ToolUse:
					repairUse = bl
				case types.ToolResult:
					repairRes = bl
				}
			}
		}
		if repairUse.ID != "call_mixed_1" || repairUse.Name != "gohan_respond" || string(repairUse.Args) != `{"total":0}` {
			t.Errorf("repair ToolUse = %+v, want gohan_respond with the complete args under call_mixed_1", repairUse)
		}
		if repairRes.ID != "call_mixed_1" || len(repairRes.Content) == 0 {
			t.Errorf("repair ToolResult = %+v, want the validation error content under call_mixed_1", repairRes)
		}
		if repairRes.Error == nil || repairRes.Error.Kind != types.Retryable || repairRes.Error.Message != repairRes.Content[0].(types.Text).Text {
			t.Errorf("repair ToolResult.Error = %+v, want a Retryable error carrying the validation message", repairRes.Error)
		}

		cm := newArgsModel("cheap", `{"label":"refund"}`)
		labels := []label{"refund", "billing"}
		clabel, err := Classify(recipeStack(t, chains.PromptSet{}, cm), "cheap", labels).Invoke(gohanctx(), nil)
		if err != nil {
			t.Fatalf("cap-less Classify Invoke: %v", err)
		}
		if clabel != "refund" {
			t.Fatalf("cap-less Classify label = %q, want refund", clabel)
		}
		creq := cm.requests[0]
		if len(creq.Tools) != 1 || creq.Tools[0].Name != "gohan_respond" {
			t.Fatalf("cap-less Classify tools = %v, want exactly gohan_respond", creq.Tools)
		}
		if len(creq.Options.ResponseSchema) != 0 {
			t.Errorf("cap-less Classify carried ResponseSchema %s, want none", creq.Options.ResponseSchema)
		}
	})

	t.Run("structured-output.unrepresentable-schema", func(t *testing.T) {
		m := &scriptedModel{profile: types.ModelProfile{Name: "cheap"}}
		f := Extract[invoice](recipeStack(t, chains.PromptSet{}, m), "cheap", WithSchema(json.RawMessage(deepSchema)))
		_, err := f.Invoke(gohanctx(), nil)
		var me *types.ModelError
		if !errors.As(err, &me) || me.Class != types.ClassPermanent {
			t.Fatalf("err = %v, want a ClassPermanent model error", err)
		}
		for _, part := range []string{"properties.a.properties.b.properties.c.properties.d", "maximum depth 4"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("err %q misses %q", err, part)
			}
		}
		if m.calls != 0 {
			t.Fatalf("model calls = %d, want 0 before the provider", m.calls)
		}
	})

	t.Run("structured-output.bounds-stay-validation", func(t *testing.T) {
		m := newConstrainedTextModel("cheap", `{"quantity":0}`, `{"quantity":0}`)
		f := Extract[quantity](recipeStack(t, chains.PromptSet{}, m), "cheap")
		_, err := f.Invoke(gohanctx(), nil)
		if !errors.Is(err, types.ErrStructuredOutput) {
			t.Fatalf("err = %v, want ErrStructuredOutput", err)
		}
		var me *types.ModelError
		if errors.As(err, &me) {
			t.Fatalf("err = %v, want no permanent model error for a bounds violation", err)
		}
	})
}
