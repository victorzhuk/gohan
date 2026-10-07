package eino

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/victorzhuk/gohan/core/types"
)

// Fidelity: the gohan block kinds the eino agentic schema natively
// carries (text, reasoning, image, audio, file, tool call) map onto their
// own channels; the kinds the schema has no channel for (document, tool
// result, raw, cache break, compaction) round-trip losslessly through a
// reserved block type. Reasoning travels only through the reasoning
// channel of the provider that produced it; foreign reasoning is dropped,
// never folded into text.
const gohanBlockType schema.ContentBlockType = "gohan"

const (
	extraKindKey     = "gohan.kind"
	extraValueKey    = "gohan.value"
	extraProviderKey = "gohan.provider"
)

// EncodeMessages converts gohan messages to eino agentic messages. The
// provider names the adapter whose fidelity is declared; reasoning from
// any other provider is dropped.
func EncodeMessages(provider string, msgs []types.Message) ([]*schema.AgenticMessage, error) {
	out := make([]*schema.AgenticMessage, 0, len(msgs))
	for _, msg := range msgs {
		am, err := encodeMessage(provider, msg)
		if err != nil {
			return nil, err
		}
		out = append(out, am)
	}
	return out, nil
}

// DecodeMessages converts eino agentic messages back to gohan messages.
func DecodeMessages(provider string, in []*schema.AgenticMessage) ([]types.Message, error) {
	out := make([]types.Message, 0, len(in))
	for _, am := range in {
		msg, err := decodeMessage(provider, am)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

func encodeMessage(provider string, msg types.Message) (*schema.AgenticMessage, error) {
	role, err := agenticRole(msg.Role)
	if err != nil {
		return nil, err
	}
	blocks := make([]*schema.ContentBlock, 0, len(msg.Blocks))
	for _, b := range msg.Blocks {
		cb, err := encodeBlock(provider, role, b)
		if err != nil {
			return nil, err
		}
		if cb == nil {
			continue
		}
		blocks = append(blocks, cb)
	}
	am := &schema.AgenticMessage{Role: role, ContentBlocks: blocks}
	if len(msg.Meta) > 0 {
		am.Extra = make(map[string]any, len(msg.Meta))
		for k, v := range msg.Meta {
			am.Extra[k] = v
		}
	}
	return am, nil
}

func encodeBlock(provider string, role schema.AgenticRoleType, b types.Block) (*schema.ContentBlock, error) {
	switch r := b.(type) {
	case types.Text:
		text := &schema.ContentBlock{}
		if role == schema.AgenticRoleTypeAssistant {
			text.Type = schema.ContentBlockTypeAssistantGenText
			text.AssistantGenText = &schema.AssistantGenText{Text: r.Text}
		} else {
			text.Type = schema.ContentBlockTypeUserInputText
			text.UserInputText = &schema.UserInputText{Text: r.Text}
		}
		return text, nil
	case types.Reasoning:
		if r.Provider != provider {
			return nil, nil
		}
		return &schema.ContentBlock{
			Type:      schema.ContentBlockTypeReasoning,
			Reasoning: &schema.Reasoning{Text: r.Text, Signature: string(r.Signature)},
		}, nil
	case types.Image:
		img := &schema.UserInputImage{URL: r.URL, MIMEType: r.MIME}
		if len(r.Data) > 0 {
			img.Base64Data = base64.StdEncoding.EncodeToString(r.Data)
			img.URL = ""
		}
		return &schema.ContentBlock{Type: schema.ContentBlockTypeUserInputImage, UserInputImage: img}, nil
	case types.Audio:
		aud := &schema.UserInputAudio{URL: r.URL, MIMEType: r.MIME}
		if len(r.Data) > 0 {
			aud.Base64Data = base64.StdEncoding.EncodeToString(r.Data)
			aud.URL = ""
		}
		return &schema.ContentBlock{Type: schema.ContentBlockTypeUserInputAudio, UserInputAudio: aud}, nil
	case types.File:
		f := &schema.UserInputFile{URL: r.URL, Name: r.Name, MIMEType: r.MIME}
		if len(r.Data) > 0 {
			f.Base64Data = base64.StdEncoding.EncodeToString(r.Data)
			f.URL = ""
		}
		return &schema.ContentBlock{Type: schema.ContentBlockTypeUserInputFile, UserInputFile: f}, nil
	case types.ToolUse:
		return &schema.ContentBlock{
			Type: schema.ContentBlockTypeFunctionToolCall,
			FunctionToolCall: &schema.FunctionToolCall{
				CallID:    r.ID,
				Name:      r.Name,
				Arguments: string(r.Args),
			},
		}, nil
	case types.Document:
		return opaqueBlock(types.KindDocument, r, "")
	case types.ToolResult:
		return opaqueBlock(types.KindToolResult, r, "")
	case types.Raw:
		return opaqueBlock(types.KindRaw, r, r.Provider)
	case types.CacheBreak:
		return opaqueBlock(types.KindCacheBreak, r, "")
	case types.Compaction:
		return opaqueBlock(types.KindCompaction, r, "")
	default:
		return nil, fmt.Errorf("bridge: cannot carry block kind %T through eino", b)
	}
}

func opaqueBlock(kind types.BlockKind, value any, provider string) (*schema.ContentBlock, error) {
	extra := map[string]any{extraKindKey: string(kind), extraValueKey: value}
	if provider != "" {
		extra[extraProviderKey] = provider
	}
	return &schema.ContentBlock{Type: gohanBlockType, Extra: extra}, nil
}

func decodeMessage(provider string, am *schema.AgenticMessage) (types.Message, error) {
	blocks := make([]types.Block, 0, len(am.ContentBlocks))
	for _, cb := range am.ContentBlocks {
		b, err := decodeBlock(provider, cb)
		if err != nil {
			return types.Message{}, err
		}
		blocks = append(blocks, b)
	}
	msg := types.Message{Blocks: blocks}
	switch am.Role {
	case schema.AgenticRoleTypeSystem:
		msg.Role = types.RoleSystem
	case schema.AgenticRoleTypeUser:
		msg.Role = types.RoleUser
	case schema.AgenticRoleTypeAssistant:
		msg.Role = types.RoleAssistant
	default:
		return types.Message{}, fmt.Errorf("bridge: unknown eino role %q", am.Role)
	}
	if len(am.Extra) > 0 {
		msg.Meta = make(map[string]any, len(am.Extra))
		for k, v := range am.Extra {
			msg.Meta[k] = v
		}
	}
	return msg, nil
}

func decodeBlock(provider string, cb *schema.ContentBlock) (types.Block, error) {
	if cb.Type == gohanBlockType {
		return decodeOpaqueBlock(cb.Extra)
	}
	switch cb.Type {
	case schema.ContentBlockTypeUserInputText:
		return types.Text{Text: cb.UserInputText.Text}, nil
	case schema.ContentBlockTypeAssistantGenText:
		return types.Text{Text: cb.AssistantGenText.Text}, nil
	case schema.ContentBlockTypeReasoning:
		r := cb.Reasoning
		if r == nil {
			return nil, fmt.Errorf("bridge: eino reasoning block without payload")
		}
		return types.Reasoning{
			Text:      r.Text,
			Signature: []byte(r.Signature),
			Provider:  provider,
		}, nil
	case schema.ContentBlockTypeUserInputImage:
		img := cb.UserInputImage
		data, url, err := blobFields(img.Base64Data, img.URL)
		if err != nil {
			return nil, fmt.Errorf("bridge: image block: %w", err)
		}
		return types.Image{MIME: img.MIMEType, Data: data, URL: url}, nil
	case schema.ContentBlockTypeUserInputAudio:
		aud := cb.UserInputAudio
		data, url, err := blobFields(aud.Base64Data, aud.URL)
		if err != nil {
			return nil, fmt.Errorf("bridge: audio block: %w", err)
		}
		return types.Audio{MIME: aud.MIMEType, Data: data, URL: url}, nil
	case schema.ContentBlockTypeUserInputFile:
		f := cb.UserInputFile
		data, url, err := blobFields(f.Base64Data, f.URL)
		if err != nil {
			return nil, fmt.Errorf("bridge: file block: %w", err)
		}
		return types.File{MIME: f.MIMEType, Name: f.Name, Data: data, URL: url}, nil
	case schema.ContentBlockTypeFunctionToolCall:
		call := cb.FunctionToolCall
		if call == nil {
			return nil, fmt.Errorf("bridge: eino tool call block without payload")
		}
		return types.ToolUse{ID: call.CallID, Name: call.Name, Args: jsontext.Value(call.Arguments)}, nil
	default:
		return nil, fmt.Errorf("bridge: unknown eino block type %q", cb.Type)
	}
}

func decodeOpaqueBlock(extra map[string]any) (types.Block, error) {
	kind, _ := extra[extraKindKey].(string)
	value, ok := extra[extraValueKey]
	if kind == "" || !ok {
		return nil, fmt.Errorf("bridge: opaque block without kind or value")
	}
	switch types.BlockKind(kind) {
	case types.KindDocument:
		doc, ok := value.(types.Document)
		if !ok {
			return nil, fmt.Errorf("bridge: opaque document block of wrong shape %T", value)
		}
		return doc, nil
	case types.KindToolResult:
		res, ok := value.(types.ToolResult)
		if !ok {
			return nil, fmt.Errorf("bridge: opaque tool result block of wrong shape %T", value)
		}
		return res, nil
	case types.KindRaw:
		raw, ok := value.(types.Raw)
		if !ok {
			return nil, fmt.Errorf("bridge: opaque raw block of wrong shape %T", value)
		}
		if p, ok := extra[extraProviderKey].(string); ok && raw.Provider == "" {
			raw.Provider = p
		}
		return raw, nil
	case types.KindCacheBreak:
		cb, ok := value.(types.CacheBreak)
		if !ok {
			return nil, fmt.Errorf("bridge: opaque cache break block of wrong shape %T", value)
		}
		return cb, nil
	case types.KindCompaction:
		c, ok := value.(types.Compaction)
		if !ok {
			return nil, fmt.Errorf("bridge: opaque compaction block of wrong shape %T", value)
		}
		return c, nil
	default:
		return nil, fmt.Errorf("bridge: unknown opaque block kind %q", kind)
	}
}

func blobFields(base64Data, url string) ([]byte, string, error) {
	if base64Data == "" {
		return nil, url, nil
	}
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, "", err
	}
	return data, "", nil
}

func agenticRole(r types.Role) (schema.AgenticRoleType, error) {
	switch r {
	case types.RoleSystem:
		return schema.AgenticRoleTypeSystem, nil
	case types.RoleUser:
		return schema.AgenticRoleTypeUser, nil
	case types.RoleAssistant:
		return schema.AgenticRoleTypeAssistant, nil
	default:
		return "", fmt.Errorf("bridge: unknown role %q", r)
	}
}
