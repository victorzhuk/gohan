package conformance

import (
	"encoding/json/jsontext"
	"fmt"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// fidelityFixture covers every block kind the declared fidelity names,
// with block base and blob fields on every block that carries them.
func fidelityFixture() []types.Message {
	return []types.Message{
		{
			Role: types.RoleUser,
			Blocks: []types.Block{
				types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser, Name: "u1"}, Seq: 1}, Text: "hi"},
				types.Image{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser, Name: "u1"}, Seq: 2}, MIME: "image/png", Data: []byte{1, 2}, URL: "https://example.test/a.png", Blob: types.Blob{Ref: "r1", SHA256: "aa", Bytes: 2}},
			},
		},
		{
			Role: types.RoleAssistant,
			Blocks: []types.Block{
				types.Reasoning{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel, Name: "m"}, Seq: 3}, Text: "why", Provider: "eino"},
				types.ToolUse{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel, Name: "m"}, Seq: 4}, ID: "c1", Name: "echo", Args: jsontext.Value(`{}`)},
				types.ToolResult{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "echo"}, Seq: 5}, ID: "c1", Content: []types.Block{types.Text{Text: "out"}}},
				types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel, Name: "m"}, Seq: 6}, Text: "done"},
			},
		},
		{
			Role: types.RoleUser,
			Blocks: []types.Block{
				types.Audio{BlockBase: types.BlockBase{Seq: 7}, MIME: "audio/wav", Data: []byte{3}, Blob: types.Blob{Ref: "r2", SHA256: "bb", Bytes: 1}},
				types.File{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser, Name: "u1"}, Seq: 8}, Name: "n.txt", URL: "https://example.test/n.txt", Blob: types.Blob{Ref: "r3", SHA256: "cc", Bytes: 9}},
				types.Document{BlockBase: types.BlockBase{Seq: 9}, Source: "s", Content: []types.Block{types.Text{Text: "inner"}}},
				types.Raw{BlockBase: types.BlockBase{Seq: 10}, Provider: "eino", Value: map[string]any{"k": "v"}},
			},
		},
	}
}

// fidelityBlock is the fixture codec's wire form. The faithful fixture
// codec keeps every declared field; the dropping codecs in the self-check
// drop exactly one declared obligation each.
type fidelityBlock struct {
	Kind       string
	Text       string
	Origin     types.Origin
	Seq        int64
	Data       []byte
	URL        string
	Blob       types.Blob
	MIME       string
	Name       string
	Document   *fidelityDocument
	ToolUse    *fidelityToolUse
	ToolResult *fidelityToolResult
	Raw        any
}

type fidelityDocument struct {
	Source  string
	Meta    map[string]string
	Content []types.Block
}

type fidelityToolUse struct {
	ID   string
	Name string
	Args string
}

type fidelityToolResult struct {
	ID      string
	Content []types.Block
	Outcome types.Outcome
	Error   *types.ToolError
}

type fidelityMessage struct {
	Role   types.Role
	Meta   map[string]any
	Blocks []fidelityBlock
}

func fixtureKindOf(b types.Block) string {
	switch b.(type) {
	case types.Text:
		return "text"
	case types.Reasoning:
		return "reasoning"
	case types.Image:
		return "image"
	case types.Audio:
		return "audio"
	case types.File:
		return "file"
	case types.Document:
		return "document"
	case types.ToolUse:
		return "tool_use"
	case types.ToolResult:
		return "tool_result"
	case types.Raw:
		return "raw"
	default:
		return fmt.Sprintf("%T", b)
	}
}

func fixtureBaseFields(b types.Block) (types.Origin, int64, []byte, string, string, types.Blob) {
	base := blockBaseOf(b)
	switch v := b.(type) {
	case types.Image:
		return base.Origin, base.Seq, v.Data, v.URL, v.MIME, v.Blob
	case types.Audio:
		return base.Origin, base.Seq, v.Data, v.URL, v.MIME, v.Blob
	case types.File:
		return base.Origin, base.Seq, v.Data, v.URL, v.MIME, v.Blob
	}
	return base.Origin, base.Seq, nil, "", "", types.Blob{}
}

func fixtureEncode(msgs []types.Message) (any, error) {
	return fixtureEncodeDrop(msgs, false, false)
}

// fixtureEncodeDropBase drops the block base from every block.
func fixtureEncodeDropBase(msgs []types.Message) (any, error) {
	return fixtureEncodeDrop(msgs, true, false)
}

// fixtureEncodeDropBlock drops the second block of the first message.
func fixtureEncodeDropBlock(msgs []types.Message) (any, error) {
	return fixtureEncodeDrop(msgs, false, true)
}

func fixtureEncodeDrop(msgs []types.Message, dropBase, dropBlock bool) (any, error) {
	out := make([]fidelityMessage, 0, len(msgs))
	for mi, m := range msgs {
		fm := fidelityMessage{Role: m.Role, Meta: m.Meta}
		for bi, b := range m.Blocks {
			if dropBlock && mi == 0 && bi == 1 {
				continue
			}
			fb := fidelityBlock{Kind: fixtureKindOf(b)}
			switch v := b.(type) {
			case types.Text:
				fb.Text = v.Text
			case types.Reasoning:
				fb.Text = v.Text
			case types.File:
				fb.Name = v.Name
			case types.Document:
				fb.Document = &fidelityDocument{Source: v.Source, Meta: v.Meta, Content: v.Content}
			case types.ToolUse:
				fb.ToolUse = &fidelityToolUse{ID: v.ID, Name: v.Name, Args: string(v.Args)}
			case types.ToolResult:
				fb.ToolResult = &fidelityToolResult{ID: v.ID, Content: v.Content, Outcome: v.Outcome, Error: v.Error}
			case types.Raw:
				fb.Raw = v.Value
			}
			fb.Origin, fb.Seq, fb.Data, fb.URL, fb.MIME, fb.Blob = fixtureBaseFields(b)
			if dropBase {
				fb.Origin, fb.Seq = types.Origin{}, 0
			}
			fm.Blocks = append(fm.Blocks, fb)
		}
		out = append(out, fm)
	}
	return out, nil
}

func fixtureDecode(in any) ([]types.Message, error) {
	wire, ok := in.([]fidelityMessage)
	if !ok {
		return nil, fmt.Errorf("fixture codec: wrong wire shape %T", in)
	}
	out := make([]types.Message, 0, len(wire))
	for _, m := range wire {
		msg := types.Message{Role: m.Role, Meta: m.Meta}
		for _, fb := range m.Blocks {
			b, err := fixtureBlockDecode(fb)
			if err != nil {
				return nil, err
			}
			msg.Blocks = append(msg.Blocks, b)
		}
		out = append(out, msg)
	}
	return out, nil
}

func fixtureBlockDecode(fb fidelityBlock) (types.Block, error) {
	base := types.BlockBase{Origin: fb.Origin, Seq: fb.Seq}
	switch fb.Kind {
	case "text":
		return types.Text{BlockBase: base, Text: fb.Text}, nil
	case "reasoning":
		return types.Reasoning{BlockBase: base, Text: fb.Text, Provider: "eino"}, nil
	case "image":
		return types.Image{BlockBase: base, MIME: fb.MIME, Data: fb.Data, URL: fb.URL, Blob: fb.Blob}, nil
	case "audio":
		return types.Audio{BlockBase: base, MIME: fb.MIME, Data: fb.Data, URL: fb.URL, Blob: fb.Blob}, nil
	case "file":
		return types.File{BlockBase: base, Name: fb.Name, MIME: fb.MIME, Data: fb.Data, URL: fb.URL, Blob: fb.Blob}, nil
	case "tool_use":
		return types.ToolUse{BlockBase: base, ID: fb.ToolUse.ID, Name: fb.ToolUse.Name, Args: jsontext.Value(fb.ToolUse.Args)}, nil
	case "tool_result":
		return types.ToolResult{BlockBase: base, ID: fb.ToolResult.ID, Content: fb.ToolResult.Content, Outcome: fb.ToolResult.Outcome, Error: fb.ToolResult.Error}, nil
	case "document":
		return types.Document{BlockBase: base, Source: fb.Document.Source, Meta: fb.Document.Meta, Content: fb.Document.Content}, nil
	case "raw":
		return types.Raw{BlockBase: base, Provider: "eino", Value: fb.Raw}, nil
	default:
		return nil, fmt.Errorf("fixture codec: unknown kind %q", fb.Kind)
	}
}

func TestMessageFidelity(t *testing.T) {
	faithful := MessageCodec{Provider: "eino", Encode: fixtureEncode, Decode: fixtureDecode}

	t.Run("fixture_codec_round_trips", func(t *testing.T) {
		MessageFidelity(testingTB{t}, "contract", faithful, fidelityFixture())
	})

	t.Run("drops_block_base", func(t *testing.T) {
		capture := &captureTB{}
		MessageFidelity(capture, "contract", MessageCodec{
			Provider: "eino",
			Encode:   fixtureEncodeDropBase,
			Decode:   fixtureDecode,
		}, fidelityFixture())
		if !capture.Failed() {
			t.Fatal("the contract passed a codec that drops the block base")
		}
	})

	t.Run("drops_block", func(t *testing.T) {
		capture := &captureTB{}
		MessageFidelity(capture, "contract", MessageCodec{
			Provider: "eino",
			Encode:   fixtureEncodeDropBlock,
			Decode:   fixtureDecode,
		}, fidelityFixture())
		if !capture.Failed() {
			t.Fatal("the contract passed a codec that drops a source block")
		}
	})
}
