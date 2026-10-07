package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/victorzhuk/gohan/core/types"
)

const cacheBreakpointMarker = "gohan:cache_breakpoint"

var governedKeys = types.GovernedModelOptionKeys()

// assembleBody maps one ModelRequest onto the chat-completions body. It is
// pure: every capability decision comes from the profile, never from call
// state.
func (p *Provider) assembleBody(req types.ModelRequest) (map[string]any, error) {
	body := map[string]any{"model": p.profile.Version}
	var messages []map[string]any
	if sys := systemText(req.System); sys != "" {
		messages = append(messages, map[string]any{"role": "system", "content": sys})
	}
	breaksMapped := 0
	breaksDropped := 0
	var pending map[string]any
	flush := func() {
		if pending != nil {
			messages = append(messages, pending)
			pending = nil
		}
	}
	for _, msg := range req.Messages {
		var content []string
		var rich []any
		var toolCalls []map[string]any
		addText := func(s string) {
			if rich != nil {
				rich = append(rich, map[string]any{"type": "text", "text": s})
				return
			}
			content = append(content, s)
		}
		for _, block := range msg.Blocks {
			switch bl := block.(type) {
			case types.Text:
				addText(bl.Text)
			case types.Image, types.Audio, types.File:
				part := openaiBlobPart(block)
				if part == nil {
					continue
				}
				if rich == nil {
					for _, s := range content {
						rich = append(rich, map[string]any{"type": "text", "text": s})
					}
					content = nil
				}
				rich = append(rich, part)
			case types.Reasoning:
			case types.ToolUse:
				toolCalls = append(toolCalls, map[string]any{
					"id":   bl.ID,
					"type": "function",
					"function": map[string]any{
						"name":      bl.Name,
						"arguments": string(bl.Args),
					},
				})
			case types.ToolResult:
				flush()
				messages = append(messages, map[string]any{
					"role":         "tool",
					"tool_call_id": bl.ID,
					"content":      blocksText(bl.Content),
				})
			case types.CacheBreak:
				flush()
				if p.profile.Caps.Cache == types.CacheExplicit && breaksMapped < p.profile.Caps.CacheBreakpoints {
					messages = append(messages, map[string]any{
						"role":    "system",
						"content": cacheBreakpointMarker,
					})
					breaksMapped++
				} else {
					breaksDropped++
				}
			case types.Raw:
				flush()
				if bl.Provider == p.profile.Name {
					if obj, ok := bl.Value.(map[string]any); ok {
						messages = append(messages, obj)
					} else if data, err := json.Marshal(bl.Value); err == nil {
						var obj map[string]any
						if json.Unmarshal(data, &obj) == nil && obj != nil {
							messages = append(messages, obj)
						}
					}
				}
			}
		}
		if len(content) == 0 && rich == nil && len(toolCalls) == 0 {
			continue
		}
		flush()
		pending = map[string]any{"role": string(msg.Role)}
		switch {
		case rich != nil:
			pending["content"] = rich
		case len(toolCalls) > 0:
			pending["content"] = strings.Join(content, "\n")
			pending["tool_calls"] = toolCalls
		default:
			pending["content"] = strings.Join(content, "\n")
		}
	}
	flush()
	if breaksDropped > 0 {
		slog.InfoContext(context.Background(), "cache breakpoints dropped",
			"gohan.cache.breakpoints_dropped", breaksDropped)
	}
	body["messages"] = messages
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, spec := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        spec.Name,
					"description": spec.Description,
					"parameters":  spec.Schema,
				},
			})
		}
		body["tools"] = tools
		switch req.Options.ToolChoice {
		case types.ToolChoiceAuto:
			body["tool_choice"] = "auto"
		case types.ToolChoiceNone:
			body["tool_choice"] = "none"
		case types.ToolChoiceAny:
			body["tool_choice"] = "required"
		}
	}
	if req.Options.MaxTokens > 0 {
		body["max_tokens"] = req.Options.MaxTokens
	}
	if req.Options.Temperature != nil {
		body["temperature"] = *req.Options.Temperature
	}
	if len(req.Options.ResponseSchema) > 0 && p.profile.Caps.Constrained {
		var schema any
		if err := json.Unmarshal(req.Options.ResponseSchema, &schema); err != nil {
			return nil, fmt.Errorf("decode response schema: %w", err)
		}
		body["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   p.profile.Name,
				"strict": true,
				"schema": schema,
			},
		}
	}
	if req.Options.Priority != 0 {
		body["extra_body"] = map[string]any{"priority": req.Options.Priority}
	}
	if meta := costTagMetadata(req.Metadata.CostTags); len(meta) > 0 {
		body["metadata"] = meta
	}
	for key, value := range req.Options.Extra {
		if slices.Contains(governedKeys[:], key) {
			continue
		}
		body[key] = value
	}
	return body, nil
}

func costTagMetadata(tags types.CostTags) map[string]any {
	meta := make(map[string]any, 3)
	if tags.Feature != "" {
		meta["feature"] = tags.Feature
	}
	if tags.Environment != "" {
		meta["environment"] = tags.Environment
	}
	if tags.CostCenter != "" {
		meta["cost_center"] = tags.CostCenter
	}
	return meta
}

func systemText(blocks []types.Block) string {
	var parts []string
	for _, block := range blocks {
		if text, ok := block.(types.Text); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func blocksText(blocks []types.Block) string {
	var parts []string
	for _, block := range blocks {
		if text, ok := block.(types.Text); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// openaiBlobPart renders a blob block as one chat-completions content part:
// the provider id when one was issued for the ref, otherwise the bytes as a
// data URI. A block with neither encodes nothing.
func openaiBlobPart(block types.Block) map[string]any {
	switch bl := block.(type) {
	case types.Image:
		ref, ok := blobRef(bl.Blob, bl.MIME, bl.Data)
		if !ok {
			return nil
		}
		return map[string]any{"type": "image_url", "image_url": map[string]any{"url": ref}}
	case types.Audio:
		ref, ok := blobRef(bl.Blob, bl.MIME, bl.Data)
		if !ok {
			return nil
		}
		return map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": ref, "format": mimeFormat(bl.MIME)}}
	case types.File:
		ref, ok := blobRef(bl.Blob, bl.MIME, bl.Data)
		if !ok {
			return nil
		}
		if bl.Blob.ProviderID != "" {
			return map[string]any{"type": "file", "file": map[string]any{"file_id": ref, "filename": bl.Name}}
		}
		return map[string]any{"type": "file", "file": map[string]any{"file_data": ref, "filename": bl.Name}}
	}
	return nil
}

// blobRef prefers the issued provider id over the bytes: assembly only
// stamps an id the target provider itself issued.
func blobRef(b types.Blob, mime string, data []byte) (string, bool) {
	if b.ProviderID != "" {
		return b.ProviderID, true
	}
	if len(data) == 0 {
		return "", false
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), true
}

func mimeFormat(mime string) string {
	if _, rest, ok := strings.Cut(mime, "/"); ok {
		return rest
	}
	return mime
}

func marshalBody(body map[string]any) (*bytes.Reader, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}
