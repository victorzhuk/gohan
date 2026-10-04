package types

import (
	"reflect"
	"testing"

	"encoding/json/jsontext"
	"encoding/json/v2"
)

func TestMessageRoundTrip(t *testing.T) {
	t.Run("messages.order-preserved", func(t *testing.T) {
		orig := Message{
			Role: RoleAssistant,
			Blocks: []Block{
				Reasoning{
					BlockBase: BlockBase{Origin: Origin{Kind: OriginModel}, Seq: 1},
					Text:      "deliberating",
					Signature: []byte("opaque-signature"),
					Provider:  "openai",
				},
				Text{BlockBase: BlockBase{Seq: 2}, Text: "answer"},
				ToolUse{BlockBase: BlockBase{Seq: 3}, ID: "call-1", Name: "search", Args: jsontext.Value(`{"q":"gohan"}`)},
				ToolUse{BlockBase: BlockBase{Seq: 4}, ID: "call-2", Name: "fetch", Args: jsontext.Value(`{"url":"https://example.invalid"}`)},
			},
		}
		raw, err := json.Marshal(orig)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got Message
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.Role != RoleAssistant {
			t.Errorf("role = %q, want %q", got.Role, RoleAssistant)
		}
		wantKinds := []string{"reasoning", "text", "tool_use", "tool_use"}
		if len(got.Blocks) != len(wantKinds) {
			t.Fatalf("len(blocks) = %d, want %d", len(got.Blocks), len(wantKinds))
		}
		for i, want := range wantKinds {
			if gotKind := blockKind(got.Blocks[i]); gotKind != want {
				t.Errorf("block %d kind = %q, want %q", i, gotKind, want)
			}
		}
		r, ok := got.Blocks[0].(Reasoning)
		if !ok {
			t.Fatalf("block 0 is %T, want Reasoning", got.Blocks[0])
		}
		if !reflect.DeepEqual(r.Signature, orig.Blocks[0].(Reasoning).Signature) {
			t.Errorf("signature = %q, want %q", r.Signature, orig.Blocks[0].(Reasoning).Signature)
		}
		if r.Provider != "openai" {
			t.Errorf("provider = %q, want %q", r.Provider, "openai")
		}
	})

	t.Run("every block kind round-trips", func(t *testing.T) {
		cases := []struct {
			name  string
			block Block
		}{
			{"text", Text{BlockBase: BlockBase{Origin: Origin{Kind: OriginUser}, Seq: 1}, Text: "hi"}},
			{"reasoning", Reasoning{BlockBase: BlockBase{Origin: Origin{Kind: OriginModel}, Seq: 2}, Signature: []byte("s"), Provider: "p"}},
			{"image", Image{BlockBase: BlockBase{Origin: Origin{Kind: OriginUser}, Seq: 3}, MIME: "image/png", Blob: Blob{Ref: "r", SHA256: "h", Bytes: 9}}},
			{"audio", Audio{BlockBase: BlockBase{Origin: Origin{Kind: OriginTool, Name: "tts"}, Seq: 4}, MIME: "audio/wav"}},
			{"file", File{BlockBase: BlockBase{Origin: Origin{Kind: OriginUser}, Seq: 5}, MIME: "text/plain", Name: "a.txt"}},
			{"document", Document{BlockBase: BlockBase{Origin: Origin{Kind: OriginTool, Name: "web"}, Seq: 6}, Source: "https://example.invalid"}},
			{"tool_use", ToolUse{BlockBase: BlockBase{Origin: Origin{Kind: OriginModel}, Seq: 7}, ID: "c", Name: "n", Args: jsontext.Value(`{}`)}},
			{"tool_result", ToolResult{BlockBase: BlockBase{Origin: Origin{Kind: OriginTool, Name: "n"}, Seq: 8}, ID: "c", Outcome: Succeeded}},
			{"cache_break", CacheBreak{BlockBase: BlockBase{Origin: Origin{Kind: OriginSystem}, Seq: 9}}},
			{"raw", Raw{BlockBase: BlockBase{Origin: Origin{Kind: OriginProvider, Name: "acme"}, Seq: 10}, Provider: "acme", Value: map[string]any{"k": "v"}}},
			{"compaction", Compaction{BlockBase: BlockBase{Origin: Origin{Kind: OriginModel}, Seq: 11}, CoversUpTo: 7, Kind: CompactionText, Model: "m", Tokens: 100}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				raw, err := json.Marshal(tc.block)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				back := reflect.New(reflect.TypeOf(tc.block)).Interface()
				if err := json.Unmarshal(raw, back); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				got := reflect.ValueOf(back).Elem().Interface().(Block)
				if want := tc.block.BlockOrigin(); got.BlockOrigin() != want {
					t.Errorf("origin = %+v, want %+v", got.BlockOrigin(), want)
				}
				if reflect.TypeOf(got) != reflect.TypeOf(tc.block) {
					t.Errorf("type = %T, want %T", got, tc.block)
				}
			})
		}
	})
}

var _ = []Block{
	Text{}, Reasoning{}, Image{}, Audio{}, File{}, Document{},
	ToolUse{}, ToolResult{}, CacheBreak{}, Raw{}, Compaction{},
}
