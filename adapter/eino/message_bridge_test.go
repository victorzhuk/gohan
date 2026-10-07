package eino

import (
	"encoding/json/jsontext"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/victorzhuk/gohan/core/types"
)

func TestMessageBridge(t *testing.T) {
	t.Run("runtime.message-round-trip", func(t *testing.T) {
		in := []types.Message{{
			Role: types.RoleAssistant,
			Blocks: []types.Block{
				types.Reasoning{Text: "thought", Signature: []byte("sig"), Provider: "eino"},
				types.Text{Text: "answer"},
				types.Image{MIME: "image/png", Data: []byte{1, 2, 3}},
				types.Document{
					Source: "doc-source",
					Meta:   map[string]string{"k": "v"},
					Content: []types.Block{
						types.Text{Text: "inner"},
					},
				},
				types.ToolUse{ID: "call-1", Name: "lookup", Args: jsontext.Value(`{"q":1}`)},
				types.Raw{Provider: "eino", Value: map[string]any{"k": "v"}},
			},
		}}
		encoded, err := EncodeMessages("eino", in)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if len(encoded) != 1 {
			t.Fatalf("encoded %d messages, want 1", len(encoded))
		}
		blocks := encoded[0].ContentBlocks
		if len(blocks) != len(in[0].Blocks) {
			t.Fatalf("encoded %d blocks, want %d", len(blocks), len(in[0].Blocks))
		}
		wantTypes := []schema.ContentBlockType{
			schema.ContentBlockTypeReasoning,
			schema.ContentBlockTypeAssistantGenText,
			schema.ContentBlockTypeUserInputImage,
			"gohan",
			schema.ContentBlockTypeFunctionToolCall,
			"gohan",
		}
		for i, wt := range wantTypes {
			if blocks[i].Type != wt {
				t.Fatalf("block %d type = %q, want %q", i, blocks[i].Type, wt)
			}
		}
		decoded, err := DecodeMessages("eino", encoded)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !reflect.DeepEqual(decoded, in) {
			t.Fatalf("round trip lost fidelity:\ndecoded: %#v\ninput:    %#v", decoded, in)
		}
	})

	t.Run("foreignReasoningDropped", func(t *testing.T) {
		in := []types.Message{{
			Role: types.RoleUser,
			Blocks: []types.Block{
				types.Reasoning{Text: "secret", Provider: "other"},
				types.Text{Text: "visible"},
			},
		}}
		encoded, err := EncodeMessages("eino", in)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		blocks := encoded[0].ContentBlocks
		if len(blocks) != 1 {
			t.Fatalf("encoded %d blocks, want 1", len(blocks))
		}
		if blocks[0].Type != schema.ContentBlockTypeUserInputText {
			t.Fatalf("block type = %q, want %q", blocks[0].Type, schema.ContentBlockTypeUserInputText)
		}
		if blocks[0].UserInputText.Text != "visible" {
			t.Fatalf("foreign reasoning folded into text: %q", blocks[0].UserInputText.Text)
		}
		decoded, err := DecodeMessages("eino", encoded)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(decoded[0].Blocks) != 1 {
			t.Fatalf("decoded %d blocks, want 1", len(decoded[0].Blocks))
		}
		if _, ok := decoded[0].Blocks[0].(types.Text); !ok {
			t.Fatalf("decoded block %T, want types.Text", decoded[0].Blocks[0])
		}
	})
}
