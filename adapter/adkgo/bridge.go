package adkgo

import (
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
	"google.golang.org/genai"
)

// Fidelity: the gohan block kinds the adk-go content schema natively
// carries (text, reasoning, image, audio, file, tool call, tool result)
// map onto their own channels; the kinds the schema has no channel for
// (document, raw, cache break, compaction) round-trip losslessly through
// the reserved Part.PartMetadata key. Reasoning travels only through the
// channel of the provider that produced it; foreign reasoning is dropped,
// never folded into text.
const gohanMetaKey = "gohan"

const gohanToolResultKey = "gohan_tool_result"

type gohanMeta struct {
	Kind     types.BlockKind `json:"kind"`
	Base     types.BlockBase `json:"base,omitempty"`
	Blob     types.Blob      `json:"blob,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Payload  any             `json:"payload,omitempty"`
}

// EncodeMessages converts gohan messages to adk-go contents. The provider
// names the adapter whose fidelity is declared; reasoning from any other
// provider is dropped.
func EncodeMessages(provider string, msgs []types.Message) ([]*genai.Content, error) {
	out := make([]*genai.Content, 0, len(msgs))
	for _, msg := range msgs {
		c, err := encodeMessage(provider, msg)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// DecodeMessages converts adk-go contents back to gohan messages.
func DecodeMessages(provider string, in []*genai.Content) ([]types.Message, error) {
	out := make([]types.Message, 0, len(in))
	for _, c := range in {
		msg, err := decodeMessage(provider, c)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

func encodeMessage(provider string, msg types.Message) (*genai.Content, error) {
	role, err := genaiRole(msg.Role)
	if err != nil {
		return nil, err
	}
	parts := make([]*genai.Part, 0, len(msg.Blocks))
	for _, b := range msg.Blocks {
		p, err := encodeBlock(provider, b)
		if err != nil {
			return nil, err
		}
		if p == nil {
			continue
		}
		parts = append(parts, p)
	}
	return &genai.Content{Role: role, Parts: parts}, nil
}

func encodeBlock(provider string, b types.Block) (*genai.Part, error) {
	switch r := b.(type) {
	case types.Text:
		p := &genai.Part{Text: r.Text}
		carryMeta(p, gohanMeta{Kind: types.KindText, Base: r.BlockBase})
		return p, nil
	case types.Reasoning:
		if r.Provider != provider {
			return nil, nil
		}
		p := &genai.Part{Text: r.Text, Thought: true, ThoughtSignature: r.Signature}
		carryMeta(p, gohanMeta{Kind: types.KindReasoning, Base: r.BlockBase})
		return p, nil
	case types.Image:
		p := mediaPart(r.MIME, r.Data, r.URL)
		carryMeta(p, gohanMeta{Kind: types.KindImage, Base: r.BlockBase, Blob: r.Blob})
		return p, nil
	case types.Audio:
		p := mediaPart(r.MIME, r.Data, r.URL)
		carryMeta(p, gohanMeta{Kind: types.KindAudio, Base: r.BlockBase, Blob: r.Blob})
		return p, nil
	case types.File:
		p := mediaPart(r.MIME, r.Data, r.URL)
		if r.Name != "" {
			if p.FileData == nil {
				p.FileData = &genai.FileData{MIMEType: r.MIME}
			}
			p.FileData.DisplayName = r.Name
		}
		carryMeta(p, gohanMeta{Kind: types.KindFile, Base: r.BlockBase, Blob: r.Blob, Payload: r.Name})
		return p, nil
	case types.ToolUse:
		var args map[string]any
		if len(r.Args) > 0 {
			if err := json.Unmarshal(r.Args, &args); err != nil {
				return nil, fmt.Errorf("bridge: tool call %s args: %w", r.Name, err)
			}
		}
		p := &genai.Part{FunctionCall: &genai.FunctionCall{ID: r.ID, Name: r.Name, Args: args}}
		carryMeta(p, gohanMeta{Kind: types.KindToolUse, Base: r.BlockBase, Payload: string(r.Args)})
		return p, nil
	case types.ToolResult:
		content, err := json.Marshal(r.Content)
		if err != nil {
			return nil, fmt.Errorf("bridge: tool result %s content: %w", r.ID, err)
		}
		p := &genai.Part{FunctionResponse: &genai.FunctionResponse{
			ID:       r.ID,
			Response: map[string]any{gohanToolResultKey: string(content)},
		}}
		carryMeta(p, gohanMeta{Kind: types.KindToolResult, Base: r.BlockBase, Payload: r})
		return p, nil
	case types.Document:
		return opaquePart(types.KindDocument, r, ""), nil
	case types.Raw:
		return opaquePart(types.KindRaw, r, r.Provider), nil
	case types.CacheBreak:
		return opaquePart(types.KindCacheBreak, r, ""), nil
	case types.Compaction:
		return opaquePart(types.KindCompaction, r, ""), nil
	default:
		return nil, fmt.Errorf("bridge: cannot carry block kind %T through adk-go", b)
	}
}

func opaquePart(kind types.BlockKind, value any, provider string) *genai.Part {
	return &genai.Part{PartMetadata: map[string]any{
		gohanMetaKey: gohanMeta{Kind: kind, Provider: provider, Payload: value},
	}}
}

// mediaPart builds the media channel for a blob block: inline bytes when
// present, otherwise or additionally the URI channel.
func mediaPart(mime string, data []byte, url string) *genai.Part {
	p := &genai.Part{}
	if len(data) > 0 || url == "" {
		p.InlineData = &genai.Blob{Data: data, MIMEType: mime}
	}
	if url != "" {
		p.FileData = &genai.FileData{FileURI: url, MIMEType: mime}
	}
	return p
}

// carryMeta attaches the reserved metadata that keeps the block kind,
// provenance and blob beside the native channel fields.
func carryMeta(p *genai.Part, meta gohanMeta) {
	if p.PartMetadata == nil {
		p.PartMetadata = map[string]any{}
	}
	p.PartMetadata[gohanMetaKey] = meta
}

func partMeta(p *genai.Part) (gohanMeta, bool) {
	v, ok := p.PartMetadata[gohanMetaKey]
	if !ok {
		return gohanMeta{}, false
	}
	if m, ok := v.(gohanMeta); ok {
		return m, true
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return gohanMeta{}, false
	}
	var m gohanMeta
	if err := json.Unmarshal(raw, &m); err != nil || m.Kind == "" {
		return gohanMeta{}, false
	}
	return m, true
}

func decodeMessage(provider string, c *genai.Content) (types.Message, error) {
	blocks := make([]types.Block, 0, len(c.Parts))
	for _, p := range c.Parts {
		b, err := decodeBlock(provider, p)
		if err != nil {
			return types.Message{}, err
		}
		blocks = append(blocks, b)
	}
	msg := types.Message{Blocks: blocks}
	switch c.Role {
	case "model":
		msg.Role = types.RoleAssistant
	case "user":
		msg.Role = types.RoleUser
	case string(types.RoleSystem):
		msg.Role = types.RoleSystem
	default:
		return types.Message{}, fmt.Errorf("bridge: unknown adk-go role %q", c.Role)
	}
	return msg, nil
}

func decodeBlock(provider string, p *genai.Part) (types.Block, error) {
	meta, hasMeta := partMeta(p)
	if hasMeta {
		b, err := decodeWithMeta(provider, p, meta)
		if err != nil {
			return nil, err
		}
		if b != nil {
			return b, nil
		}
	}
	switch {
	case p.FunctionCall != nil:
		call := p.FunctionCall
		args, err := marshalArgs(call.Args)
		if err != nil {
			return nil, fmt.Errorf("bridge: tool call %s args: %w", call.Name, err)
		}
		return types.ToolUse{BlockBase: meta.Base, ID: call.ID, Name: call.Name, Args: args}, nil
	case p.FunctionResponse != nil:
		return decodeToolResponse(p.FunctionResponse, meta)
	case p.Thought:
		return types.Reasoning{BlockBase: meta.Base, Text: p.Text, Signature: p.ThoughtSignature, Provider: provider}, nil
	case p.InlineData != nil || p.FileData != nil:
		return decodeMedia(meta.Kind, p, meta)
	case p.Text != "":
		return types.Text{BlockBase: meta.Base, Text: p.Text}, nil
	default:
		return nil, fmt.Errorf("bridge: unknown adk-go part shape")
	}
}

func decodeWithMeta(provider string, p *genai.Part, meta gohanMeta) (types.Block, error) {
	switch meta.Kind {
	case types.KindText:
		return types.Text{BlockBase: meta.Base, Text: p.Text}, nil
	case types.KindReasoning:
		return types.Reasoning{BlockBase: meta.Base, Text: p.Text, Signature: p.ThoughtSignature, Provider: provider}, nil
	case types.KindImage, types.KindAudio, types.KindFile:
		return decodeMedia(meta.Kind, p, meta)
	case types.KindToolUse:
		call := p.FunctionCall
		if call == nil {
			return nil, fmt.Errorf("bridge: adk-go tool call block without payload")
		}
		args, err := restoreArgs(meta.Payload, call.Args)
		if err != nil {
			return nil, fmt.Errorf("bridge: tool call %s args: %w", call.Name, err)
		}
		return types.ToolUse{BlockBase: meta.Base, ID: call.ID, Name: call.Name, Args: args}, nil
	case types.KindToolResult:
		if res, ok := meta.Payload.(types.ToolResult); ok {
			if res.ID == "" {
				res.ID = p.FunctionResponse.ID
			}
			return res, nil
		}
		return decodeToolResponse(p.FunctionResponse, meta)
	case types.KindDocument:
		doc, ok := meta.Payload.(types.Document)
		if !ok {
			return nil, fmt.Errorf("bridge: opaque document block of wrong shape %T", meta.Payload)
		}
		return doc, nil
	case types.KindRaw:
		raw, ok := meta.Payload.(types.Raw)
		if !ok {
			return nil, fmt.Errorf("bridge: opaque raw block of wrong shape %T", meta.Payload)
		}
		return raw, nil
	case types.KindCacheBreak:
		cb, ok := meta.Payload.(types.CacheBreak)
		if !ok {
			return nil, fmt.Errorf("bridge: opaque cache break block of wrong shape %T", meta.Payload)
		}
		return cb, nil
	case types.KindCompaction:
		comp, ok := meta.Payload.(types.Compaction)
		if !ok {
			return nil, fmt.Errorf("bridge: opaque compaction block of wrong shape %T", meta.Payload)
		}
		return comp, nil
	default:
		return nil, nil
	}
}

func decodeMedia(kind types.BlockKind, p *genai.Part, meta gohanMeta) (types.Block, error) {
	var data []byte
	var mime string
	if p.InlineData != nil {
		data = p.InlineData.Data
		mime = p.InlineData.MIMEType
	}
	var url string
	var name string
	if p.FileData != nil {
		url = p.FileData.FileURI
		if mime == "" {
			mime = p.FileData.MIMEType
		}
		name = p.FileData.DisplayName
	}
	if n, ok := meta.Payload.(string); ok && n != "" {
		name = n
	}
	base := types.BlockBase{Origin: meta.Base.Origin, Seq: meta.Base.Seq}
	switch kind {
	case types.KindImage:
		return types.Image{BlockBase: base, Blob: meta.Blob, MIME: mime, Data: data, URL: url}, nil
	case types.KindAudio:
		return types.Audio{BlockBase: base, Blob: meta.Blob, MIME: mime, Data: data, URL: url}, nil
	case types.KindFile:
		return types.File{BlockBase: base, Blob: meta.Blob, MIME: mime, Name: name, Data: data, URL: url}, nil
	default:
		return nil, fmt.Errorf("bridge: media block of unknown kind %q", kind)
	}
}

func decodeToolResponse(fr *genai.FunctionResponse, meta gohanMeta) (types.Block, error) {
	if fr == nil {
		return nil, fmt.Errorf("bridge: adk-go tool result block without payload")
	}
	var content []types.Block
	if raw, ok := fr.Response[gohanToolResultKey].(string); ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &content); err != nil {
			return nil, fmt.Errorf("bridge: tool result %s content: %w", fr.ID, err)
		}
	}
	return types.ToolResult{BlockBase: meta.Base, ID: fr.ID, Content: content}, nil
}

func restoreArgs(payload any, args map[string]any) (jsontext.Value, error) {
	if raw, ok := payload.(string); ok {
		return jsontext.Value(raw), nil
	}
	return marshalArgs(args)
}

func marshalArgs(args map[string]any) (jsontext.Value, error) {
	if len(args) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return jsontext.Value(raw), nil
}

func genaiRole(r types.Role) (string, error) {
	switch r {
	case types.RoleSystem:
		return string(types.RoleSystem), nil
	case types.RoleUser:
		return "user", nil
	case types.RoleAssistant:
		return "model", nil
	default:
		return "", fmt.Errorf("bridge: unknown gohan role %q", r)
	}
}
