package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/tokens"
)

type wireToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

type wireChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content          string              `json:"content"`
			ReasoningContent *string             `json:"reasoning_content"`
			Reasoning        *string             `json:"reasoning"`
			ToolCalls        []wireToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
}

func finishReason(raw string) types.FinishReason {
	switch raw {
	case "stop":
		return types.FinishStop
	case "tool_calls":
		return types.FinishToolUse
	case "length":
		return types.FinishMaxTokens
	case "content_filter":
		return types.FinishRefusal
	default:
		return types.FinishError
	}
}

// stream decodes the SSE body and yields chunks on the caller's goroutine.
// It returns at the first refusal to receive, yields nothing after an error
// and closes the body on the way out.
func (p *Provider) stream(ctx context.Context, resp *http.Response, req types.ModelRequest, keyID string, yield func(types.ModelChunk, error) bool) {
	type toolAcc struct {
		id, name, args string
	}
	var usageEmitted bool
	pending := map[int]*toolAcc{}
	var streamed strings.Builder
	served := ""
	defer func() {
		if !usageEmitted && ctx.Err() == nil {
			yield(types.ModelChunk{Usage: p.estimatedUsage(req, keyID, streamed.String())}, nil)
		}
	}()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[len("data:"):])
		if payload == "[DONE]" {
			return
		}
		var chunk wireChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			yield(types.ModelChunk{}, &types.ModelError{
				Class:    types.ClassTransient,
				Provider: p.profile.Name,
				Err:      fmt.Errorf("decode stream chunk: %w", err),
			})
			return
		}
		if chunk.Model != "" {
			served = chunk.Model
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				streamed.WriteString(choice.Delta.Content)
				if !yield(types.ModelChunk{Kind: types.DeltaText, Delta: choice.Delta.Content}, nil) {
					return
				}
			}
			reasoning := ""
			if choice.Delta.ReasoningContent != nil {
				reasoning = *choice.Delta.ReasoningContent
			} else if choice.Delta.Reasoning != nil {
				reasoning = *choice.Delta.Reasoning
			}
			if reasoning != "" {
				streamed.WriteString(reasoning)
				if !yield(types.ModelChunk{Kind: types.DeltaReasoning, Delta: reasoning}, nil) {
					return
				}
			}
			for _, delta := range choice.Delta.ToolCalls {
				acc := pending[delta.Index]
				if acc == nil {
					acc = &toolAcc{}
					pending[delta.Index] = acc
				}
				if delta.ID != "" {
					acc.id = delta.ID
				}
				if delta.Function.Name != "" {
					acc.name = delta.Function.Name
				}
				acc.args += delta.Function.Arguments
			}
		}
		finish := ""
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil {
				finish = *choice.FinishReason
			}
		}
		if finish == "" && chunk.Usage == nil {
			continue
		}
		var calls []types.ToolUse
		for index := range pending {
			acc := pending[index]
			calls = append(calls, types.ToolUse{
				ID:   acc.id,
				Name: acc.name,
				Args: json.RawMessage(acc.args),
			})
		}
		pending = nil
		slices.SortFunc(calls, func(a, b types.ToolUse) int {
			return strings.Compare(a.ID, b.ID)
		})
		for i := range calls[:max(len(calls)-1, 0)] {
			if !yield(types.ModelChunk{ToolUse: &calls[i]}, nil) {
				return
			}
		}
		out := &types.ModelChunk{}
		if len(calls) > 0 {
			out.ToolUse = &calls[len(calls)-1]
		}
		use := p.usageFrom(chunk.Usage, keyID, served, req, streamed.String())
		gohan.CountProviderCalls(req.Tools, calls, use)
		if finish != "" {
			out.Finish = finishReason(finish)
		}
		out.Usage = use
		usageEmitted = true
		if !yield(*out, nil) {
			return
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		yield(types.ModelChunk{}, &types.ModelError{
			Class:    types.ClassTransient,
			Provider: p.profile.Name,
			Err:      fmt.Errorf("read stream: %w", err),
		})
	}
}

// usageFrom fills the final-chunk usage. A provider that sends no usage
// never produces a zero report: the estimate rule fills it instead.
func (p *Provider) usageFrom(wire *wireUsage, keyID, served string, req types.ModelRequest, streamed string) *types.Usage {
	if wire == nil || (wire.PromptTokens == 0 && wire.CompletionTokens == 0) {
		return p.estimatedUsage(req, keyID, streamed)
	}
	use := &types.Usage{
		InputTokens:  wire.PromptTokens,
		OutputTokens: wire.CompletionTokens,
		KeyID:        keyID,
		ModelVersion: served,
	}
	if wire.PromptTokensDetails != nil {
		use.CachedInputTokens = wire.PromptTokensDetails.CachedTokens
	}
	switch p.profile.Caps.Cache {
	case types.CacheExplicit:
		use.CacheWriteTokens = wire.CacheWriteTokens
		if use.CacheWriteTokens == 0 {
			use.CacheWriteTokens = use.CachedInputTokens
		}
	case types.CacheAuto:
		use.CachedInputTokens = wire.PromptTokensDetails.CachedTokens
	default:
	}
	return use
}

func (p *Provider) estimatedUsage(req types.ModelRequest, keyID, streamed string) *types.Usage {
	caps := p.profile.Caps
	out := tokens.Heuristic.Estimate(types.ModelRequest{
		Messages: []types.Message{{Blocks: []types.Block{types.Text{Text: streamed}}}},
	}, caps)
	return &types.Usage{
		InputTokens:  tokens.Heuristic.Estimate(req, caps),
		OutputTokens: out,
		KeyID:        keyID,
		ModelVersion: p.profile.Version,
		Estimated:    true,
	}
}
