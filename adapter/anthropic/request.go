package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

const forcedToolName = "gohan_respond"

// assembled is the encoded request plus the per-call decode state the
// response needs: whether the schema forced a tool and how many explicit
// cache breakpoints the caps dropped.
type assembled struct {
	body         []byte
	schemaForced bool
	dropped      int
	specs        []types.ToolSpec
}

func (m *Model) assemble(ctx context.Context, req types.ModelRequest) ([]byte, *assembled, error) {
	for _, key := range types.GovernedModelOptionKeys() {
		if _, ok := req.Options.Extra[key]; ok {
			return nil, nil, &types.ModelError{
				Class:    types.ClassPermanent,
				Provider: providerName,
				Err:      fmt.Errorf("request extra overrides governed field %s", key),
			}
		}
	}
	state := &assembled{specs: req.Tools}
	body := map[string]any{
		"model":  m.cfg.Profile.Version,
		"stream": true,
	}
	if system := systemText(req.System); system != "" {
		body["system"] = system
	}
	content, err := m.content(ctx, req.Messages, state)
	if err != nil {
		return nil, nil, err
	}
	body["messages"] = content
	if schema := req.Options.ResponseSchema; len(schema) > 0 {
		if !m.cfg.Profile.Caps.Constrained {
			return nil, nil, &types.ModelError{
				Class:    types.ClassPermanent,
				Provider: providerName,
				Err:      errors.New("profile does not declare constrained output for a response schema"),
			}
		}
		state.schemaForced = true
		body["tools"] = []any{forcedTool(schema)}
		body["tool_choice"] = map[string]any{"type": "tool", "name": forcedToolName}
	} else if tools := toolSpecs(req.Tools); len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = toolChoice(req.Options.ToolChoice)
	}
	if n := req.Options.MaxTokens; n > 0 {
		body["max_tokens"] = n
	} else {
		body["max_tokens"] = defaultMaxTokens
	}
	if req.Options.Temperature != nil {
		body["temperature"] = *req.Options.Temperature
	}
	for key, value := range req.Options.Extra {
		body[key] = value
	}
	if state.dropped > 0 {
		if tel := types.TelemetryFrom(ctx); tel != nil {
			tel.Count(ctx, "gohan.cache.breakpoints_dropped", int64(state.dropped),
				types.String(types.KeyModelProfile, m.cfg.Profile.Name))
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, nil, &types.ModelError{Class: types.ClassPermanent, Provider: providerName, Err: err}
	}
	state.body = encoded
	return encoded, state, nil
}

func systemText(blocks []types.Block) string {
	out := ""
	for _, b := range blocks {
		if t, ok := b.(types.Text); ok {
			if out != "" {
				out += "\n"
			}
			out += t.Text
		}
	}
	return out
}

func (m *Model) content(ctx context.Context, messages []types.Message, state *assembled) ([]any, error) {
	breaks := 0
	for _, msg := range messages {
		for _, b := range msg.Blocks {
			if _, ok := b.(types.CacheBreak); ok {
				breaks++
			}
		}
	}
	cap := m.cfg.Profile.Caps.CacheBreakpoints
	issued := 0
	var out []any
	for _, msg := range messages {
		var blocks []any
		for _, b := range msg.Blocks {
			switch block := b.(type) {
			case types.Text:
				blocks = append(blocks, map[string]any{"type": "text", "text": block.Text})
			case types.Reasoning:
				blocks = append(blocks, map[string]any{
					"type":      "thinking",
					"thinking":  block.Text,
					"signature": base64.StdEncoding.EncodeToString(block.Signature),
				})
			case types.ToolUse:
				args := json.RawMessage(block.Args)
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    block.ID,
					"name":  block.Name,
					"input": args,
				})
			case types.ToolResult:
				var text string
				for _, inner := range block.Content {
					if t, ok := inner.(types.Text); ok {
						if text != "" {
							text += "\n"
						}
						text += t.Text
					}
				}
				blocks = append(blocks, map[string]any{
					"type":        "tool_result",
					"tool_use_id": block.ID,
					"content":     text,
				})
			case types.CacheBreak:
				if issued < cap {
					issued++
					blocks = append(blocks, map[string]any{
						"type":          "text",
						"text":          "",
						"cache_control": map[string]any{"type": "ephemeral"},
					})
				}
			case types.Raw:
				if block.Provider == providerName {
					blocks = append(blocks, block.Value)
				}
			}
		}
		if len(blocks) == 0 {
			continue
		}
		out = append(out, map[string]any{"role": string(msg.Role), "content": blocks})
	}
	state.dropped = breaks - issued
	return out, nil
}

func forcedTool(schema json.RawMessage) map[string]any {
	return map[string]any{
		"name":         forcedToolName,
		"description":  "Produce the response in the requested schema.",
		"input_schema": json.RawMessage(schema),
	}
}

func toolSpecs(tools []types.ToolSpec) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		schema := json.RawMessage(t.Schema)
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": schema,
		})
	}
	return out
}

func toolChoice(choice types.ToolChoice) map[string]any {
	switch choice {
	case types.ToolChoiceNone:
		return map[string]any{"type": "none"}
	case types.ToolChoiceAny:
		return map[string]any{"type": "any"}
	default:
		return map[string]any{"type": "auto"}
	}
}
