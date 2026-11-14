package adkgo

import (
	"reflect"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func bridgeFixtureMessages() []types.Message {
	return []types.Message{
		{
			Role: types.RoleAssistant,
			Blocks: []types.Block{
				types.Reasoning{
					BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel, Name: "conformance"}, Seq: 1},
					Text:      "thought",
					Signature: []byte("sig"),
					Provider:  "adkgo",
				},
				types.Text{
					BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel, Name: "conformance"}, Seq: 2},
					Text:      "answer",
				},
				types.Image{
					BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser, Name: "u1"}, Seq: 3},
					MIME:      "image/png",
					Data:      []byte{1, 2, 3},
					URL:       "https://example.test/pic.png",
					Blob:      types.Blob{Ref: "blb-1", SHA256: "aa11", Bytes: 3},
				},
				types.Document{
					BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser, Name: "u1"}, Seq: 4},
					Source:    "doc-source",
					Meta:      map[string]string{"k": "v"},
					Content: []types.Block{
						types.Text{BlockBase: types.BlockBase{Seq: 5}, Text: "inner"},
					},
				},
				types.ToolUse{
					BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel, Name: "conformance"}, Seq: 6},
					ID:        "call-1",
					Name:      "lookup",
					Args:      []byte(`{"q":1}`),
				},
				types.Raw{
					BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginProvider, Name: "adkgo"}, Seq: 7},
					Provider:  "adkgo",
					Value:     map[string]any{"k": "v"},
				},
			},
		},
	}
}

func TestMessageBridge(t *testing.T) {
	t.Run("runtime.message-round-trip", func(t *testing.T) {
		in := bridgeFixtureMessages()
		encoded, err := EncodeMessages("adkgo", in)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if len(encoded) != 1 {
			t.Fatalf("encoded %d messages, want 1", len(encoded))
		}
		parts := encoded[0].Parts
		if len(parts) != len(in[0].Blocks) {
			t.Fatalf("encoded %d parts, want %d", len(parts), len(in[0].Blocks))
		}
		if encoded[0].Role != "model" {
			t.Fatalf("encoded role = %q, want %q", encoded[0].Role, "model")
		}
		if parts[1].Text != "answer" {
			t.Fatalf("text part = %q, want %q", parts[1].Text, "answer")
		}
		if !parts[0].Thought || string(parts[0].ThoughtSignature) != "sig" {
			t.Fatalf("reasoning part = thought %v signature %q", parts[0].Thought, parts[0].ThoughtSignature)
		}
		if parts[4].FunctionCall == nil || parts[4].FunctionCall.ID != "call-1" {
			t.Fatalf("tool use part = %+v, want a function call", parts[4])
		}
		if parts[5].PartMetadata == nil {
			t.Fatalf("raw part without reserved metadata")
		}
		decoded, err := DecodeMessages("adkgo", encoded)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !reflect.DeepEqual(decoded, in) {
			t.Fatalf("round trip lost fidelity:\ndecoded: %#v\ninput:    %#v", decoded, in)
		}
	})

	t.Run("runtime.message-block-base-carried", func(t *testing.T) {
		in := []types.Message{{
			Role: types.RoleUser,
			Blocks: []types.Block{
				types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser, Name: "u1"}, Seq: 10}, Text: "t"},
				types.Reasoning{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel, Name: "m"}, Seq: 11}, Text: "r", Provider: "adkgo"},
				types.Image{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser, Name: "u1"}, Seq: 12}, MIME: "image/jpeg", Data: []byte{9}},
				types.Audio{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser, Name: "u1"}, Seq: 13}, MIME: "audio/wav", Data: []byte{8}},
				types.File{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser, Name: "u1"}, Seq: 14}, Name: "f.txt", Data: []byte{7}},
				types.ToolUse{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel, Name: "m"}, Seq: 15}, ID: "c", Name: "echo"},
			},
		}}
		decoded := roundTrip(t, "adkgo", in)
		for i, want := range in[0].Blocks {
			got := decoded[0].Blocks[i]
			if got.BlockOrigin() != want.BlockOrigin() {
				t.Fatalf("block %d origin = %+v, want %+v", i, got.BlockOrigin(), want.BlockOrigin())
			}
			if gotSeq(got) != wantSeq(want) {
				t.Fatalf("block %d seq = %d, want %d", i, gotSeq(got), wantSeq(want))
			}
			if reflect.TypeOf(got) != reflect.TypeOf(want) {
				t.Fatalf("block %d kind = %T, want %T", i, got, want)
			}
		}
	})

	t.Run("runtime.message-blob-carried", func(t *testing.T) {
		in := []types.Message{{
			Role: types.RoleUser,
			Blocks: []types.Block{
				types.Image{MIME: "image/png", Data: []byte{1, 2}, URL: "https://example.test/a.png", Blob: types.Blob{Ref: "r1", SHA256: "aa", Bytes: 2}},
				types.Audio{MIME: "audio/mpeg", URL: "https://example.test/a.mp3", Blob: types.Blob{Ref: "r2", SHA256: "bb", Bytes: 5}},
				types.File{MIME: "text/plain", Name: "n.txt", Data: []byte{3}, URL: "https://example.test/n.txt", Blob: types.Blob{Ref: "r3", SHA256: "cc", Bytes: 1}},
			},
		}}
		decoded := roundTrip(t, "adkgo", in)
		want := in[0].Blocks
		for i, b := range decoded[0].Blocks {
			var gotData []byte
			var gotURL string
			var gotBlob types.Blob
			switch v := b.(type) {
			case types.Image:
				gotData, gotURL, gotBlob = v.Data, v.URL, v.Blob
			case types.Audio:
				gotData, gotURL, gotBlob = v.Data, v.URL, v.Blob
			case types.File:
				gotData, gotURL, gotBlob = v.Data, v.URL, v.Blob
			default:
				t.Fatalf("block %d kind = %T, want a blob kind", i, b)
			}
			w := want[i]
			wantData, wantURL, wantBlob := blobOf(w)
			if string(gotData) != string(wantData) || gotURL != wantURL || gotBlob != wantBlob {
				t.Fatalf("block %d = data %q url %q blob %+v, want data %q url %q blob %+v",
					i, gotData, gotURL, gotBlob, wantData, wantURL, wantBlob)
			}
		}
	})

	t.Run("guards.origin-survives-conversion", func(t *testing.T) {
		in := []types.Message{{
			Role: types.RoleAssistant,
			Blocks: []types.Block{
				types.ToolResult{
					BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "lookup"}, Seq: 21},
					ID:        "call-1",
					Content:   []types.Block{types.Text{Text: "res"}},
				},
			},
		}}
		encoded, err := EncodeMessages("adkgo", in)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		fr := encoded[0].Parts[0].FunctionResponse
		if fr == nil || fr.ID != "call-1" {
			t.Fatalf("tool result part = %+v, want a function response", encoded[0].Parts[0])
		}
		if _, ok := fr.Response[gohanToolResultKey]; !ok {
			t.Fatalf("function response without the reserved %q key: %+v", gohanToolResultKey, fr.Response)
		}
		decoded, err := DecodeMessages("adkgo", encoded)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		res, ok := decoded[0].Blocks[0].(types.ToolResult)
		if !ok {
			t.Fatalf("decoded block %T, want types.ToolResult", decoded[0].Blocks[0])
		}
		if want := (types.Origin{Kind: types.OriginTool, Name: "lookup"}); res.BlockOrigin() != want {
			t.Fatalf("tool result origin = %+v, want %+v", res.BlockOrigin(), want)
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
		encoded, err := EncodeMessages("adkgo", in)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		parts := encoded[0].Parts
		if len(parts) != 1 {
			t.Fatalf("encoded %d parts, want 1", len(parts))
		}
		if parts[0].Thought || parts[0].Text != "visible" {
			t.Fatalf("foreign reasoning folded into the visible part: %+v", parts[0])
		}
		decoded, err := DecodeMessages("adkgo", encoded)
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

func roundTrip(t *testing.T, provider string, in []types.Message) []types.Message {
	t.Helper()
	encoded, err := EncodeMessages(provider, in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeMessages(provider, encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return decoded
}

func wantSeq(b types.Block) int64 { return blockBase(b).Seq }

func gotSeq(b types.Block) int64 { return blockBase(b).Seq }

func blockBase(b types.Block) types.BlockBase {
	switch v := b.(type) {
	case types.Text:
		return v.BlockBase
	case types.Reasoning:
		return v.BlockBase
	case types.Image:
		return v.BlockBase
	case types.Audio:
		return v.BlockBase
	case types.File:
		return v.BlockBase
	case types.Document:
		return v.BlockBase
	case types.ToolUse:
		return v.BlockBase
	case types.ToolResult:
		return v.BlockBase
	case types.CacheBreak:
		return v.BlockBase
	case types.Raw:
		return v.BlockBase
	case types.Compaction:
		return v.BlockBase
	default:
		return types.BlockBase{}
	}
}

func blobOf(b types.Block) ([]byte, string, types.Blob) {
	switch v := b.(type) {
	case types.Image:
		return v.Data, v.URL, v.Blob
	case types.Audio:
		return v.Data, v.URL, v.Blob
	case types.File:
		return v.Data, v.URL, v.Blob
	default:
		return nil, "", types.Blob{}
	}
}
