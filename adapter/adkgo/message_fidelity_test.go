package adkgo

import (
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/conformance"
	"google.golang.org/genai"
)

// adkgoFidelityFixture is the fixture the real adk-go codec satisfies the
// shared contract with: every declared kind, each with its block base, and
// the blob fields the fidelity contract names.
func adkgoFidelityFixture() []types.Message {
	msgs := bridgeFixtureMessages()
	msgs = append(msgs,
		types.Message{Role: types.RoleUser, Blocks: []types.Block{
			types.Audio{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser, Name: "u1"}, Seq: 31}, MIME: "audio/wav", Data: []byte{4}, Blob: types.Blob{Ref: "blb-a", SHA256: "bb22", Bytes: 1}},
			types.File{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser, Name: "u1"}, Seq: 32}, Name: "n.txt", MIME: "text/plain", Data: []byte{5}, URL: "https://example.test/n.txt", Blob: types.Blob{Ref: "blb-f", SHA256: "cc33", Bytes: 1}},
			types.ToolResult{
				BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "lookup"}, Seq: 33},
				ID:        "call-2",
				Content:   []types.Block{types.Text{Text: "res"}},
			},
			types.CacheBreak{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginOperator, Name: "op1"}, Seq: 34}},
			types.Compaction{
				BlockBase:  types.BlockBase{Origin: types.Origin{Kind: types.OriginSystem, Name: "sys"}, Seq: 35},
				CoversUpTo: 30,
				Kind:       types.CompactionText,
				Summary:    []types.Block{types.Text{Text: "summary"}},
				Model:      "gemini",
				Tokens:     12,
			},
		}},
	)
	return msgs
}

// TestMessageFidelity satisfies the shared message-fidelity contract with
// the real adk-go codec: block order, every declared kind's origin and
// sequence, and every carried blob equal the input after a full round
// trip.
func TestMessageFidelity(t *testing.T) {
	t.Run("engines.adkgo-message-fidelity", func(t *testing.T) {
		codec := conformance.MessageCodec{
			Provider: "adkgo",
			Encode:   func(msgs []types.Message) (any, error) { return EncodeMessages("adkgo", msgs) },
			Decode: func(wire any) ([]types.Message, error) {
				return DecodeMessages("adkgo", wire.([]*genai.Content))
			},
		}
		conformance.MessageFidelity(conformance.TestingTB(t), "adkgo", codec, adkgoFidelityFixture())
	})
}
