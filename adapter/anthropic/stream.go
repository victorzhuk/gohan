package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
)

type sseEvent struct {
	name string
	data []byte
}

// blockState owns one content block index: its call identity, its own
// argument fragments and its lifecycle, so two indices never share an
// accumulator.
type blockState struct {
	id      string
	name    string
	tool    bool
	forced  bool
	done    bool
	emitted bool
	args    strings.Builder
}

type streamState struct {
	schemaForced bool
	usage        types.Usage
	usageSeen    bool
	chars        int
	blocks       map[int]*blockState
	specs        []types.ToolSpec
	providerTool []types.ToolUse
}

func (m *Model) decode(ctx context.Context, r io.Reader, state *assembled, yield func(types.ModelChunk, error) bool) {
	st := &streamState{schemaForced: state.schemaForced, specs: state.specs}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var event sseEvent
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			yield(types.ModelChunk{}, err)
			return
		}
		line := sc.Bytes()
		switch {
		case bytes.HasPrefix(line, []byte("event:")):
			event = sseEvent{name: strings.TrimSpace(string(line[len("event:"):]))}
		case bytes.HasPrefix(line, []byte("data:")):
			event.data = append(event.data, line[len("data:"):]...)
		case len(line) == 0 && len(event.data) > 0:
			if !m.dispatch(st, event, yield) {
				return
			}
			event = sseEvent{}
		}
	}
	if err := sc.Err(); err != nil {
		yield(types.ModelChunk{}, err)
		return
	}
	if len(event.data) > 0 {
		if !m.dispatch(st, event, yield) {
			return
		}
	}
	m.finish(st, yield)
}

func (m *Model) dispatch(st *streamState, event sseEvent, yield func(types.ModelChunk, error) bool) bool {
	switch event.name {
	case "message_start":
		return messageStart(st, event.data, yield)
	case "content_block_start":
		return contentBlockStart(st, event.data, yield)
	case "content_block_delta":
		return contentBlockDelta(st, event.data, yield)
	case "content_block_stop":
		return contentBlockStop(st, event.data, yield)
	case "message_delta":
		return messageDelta(st, event.data, yield)
	case "error":
		return streamError(event.data, yield)
	}
	return true
}

func (m *Model) finish(st *streamState, yield func(types.ModelChunk, error) bool) {
	if !flushOpenBlocks(st, yield) {
		return
	}
	gohan.CountProviderCalls(st.specs, st.providerTool, &st.usage)
	if st.usageSeen && st.usage.OutputTokens == 0 {
		st.usage.OutputTokens = estimate(st.chars)
	}
	if !st.usageSeen {
		st.usage = types.Usage{OutputTokens: estimate(st.chars), Estimated: true}
	}
	st.usage.KeyID = m.cfg.KeyID
	st.usage.ModelVersion = orDefault(st.usage.ModelVersion, m.cfg.Profile.Version)
	if st.usage.InputTokens == 0 && st.usage.OutputTokens == 0 && !st.usageSeen {
		st.usage.OutputTokens = 1
	}
	chunk := types.ModelChunk{Usage: &st.usage}
	if chunk.Usage.OutputTokens == 0 {
		chunk.Usage.OutputTokens = 1
		chunk.Usage.Estimated = true
	}
	yield(chunk, nil)
}

func estimate(chars int) int {
	if n := chars / 4; n > 0 {
		return n
	}
	return 1
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func messageStart(st *streamState, data []byte, yield func(types.ModelChunk, error) bool) bool {
	var payload struct {
		Message struct {
			Model string `json:"model"`
			Usage struct {
				InputTokens              int `json:"input_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return yield(types.ModelChunk{}, decodeErr(err))
	}
	st.usage.InputTokens = payload.Message.Usage.InputTokens
	st.usage.CachedInputTokens = payload.Message.Usage.CacheReadInputTokens
	st.usage.CacheWriteTokens = payload.Message.Usage.CacheCreationInputTokens
	st.usage.ModelVersion = payload.Message.Model
	st.usageSeen = st.usage.InputTokens > 0 || st.usage.CacheWriteTokens > 0 || st.usage.CachedInputTokens > 0
	return true
}

func contentBlockStart(st *streamState, data []byte, yield func(types.ModelChunk, error) bool) bool {
	var payload struct {
		Index        int `json:"index"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return yield(types.ModelChunk{}, decodeErr(err))
	}
	b := &blockState{id: payload.ContentBlock.ID, name: payload.ContentBlock.Name}
	switch payload.ContentBlock.Type {
	case "tool_use":
		b.tool = true
		b.forced = st.schemaForced
	case "server_tool_use":
		st.providerTool = append(st.providerTool, types.ToolUse{Name: payload.ContentBlock.Name})
	}
	if st.blocks == nil {
		st.blocks = make(map[int]*blockState)
	}
	st.blocks[payload.Index] = b
	return true
}

func contentBlockDelta(st *streamState, data []byte, yield func(types.ModelChunk, error) bool) bool {
	var payload struct {
		Index int `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			Signature   string `json:"signature"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return yield(types.ModelChunk{}, decodeErr(err))
	}
	switch payload.Delta.Type {
	case "text_delta":
		st.chars += len(payload.Delta.Text)
		return yield(types.ModelChunk{Kind: types.DeltaText, Delta: payload.Delta.Text}, nil)
	case "thinking_delta":
		return yield(types.ModelChunk{Kind: types.DeltaReasoning, Delta: payload.Delta.Thinking}, nil)
	case "signature_delta":
		raw, err := base64.StdEncoding.DecodeString(payload.Delta.Signature)
		if err != nil {
			return yield(types.ModelChunk{}, decodeErr(err))
		}
		return yield(types.ModelChunk{Kind: types.DeltaReasoning, Delta: string(raw)}, nil)
	case "input_json_delta":
		b := st.blocks[payload.Index]
		if b == nil || !b.tool {
			return true
		}
		b.args.WriteString(payload.Delta.PartialJSON)
		if b.forced {
			return true
		}
		return yield(types.ModelChunk{
			Kind:    types.DeltaToolArgs,
			Delta:   payload.Delta.PartialJSON,
			ToolUse: &types.ToolUse{ID: b.id, Name: b.name},
		}, nil)
	}
	return true
}

func contentBlockStop(st *streamState, data []byte, yield func(types.ModelChunk, error) bool) bool {
	var payload struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return yield(types.ModelChunk{}, decodeErr(err))
	}
	b := st.blocks[payload.Index]
	if b == nil || !b.tool || b.done {
		return true
	}
	args := b.args.String()
	if b.forced {
		b.done = true
		b.emitted = true
		if args == "" {
			return true
		}
		return yield(types.ModelChunk{Kind: types.DeltaText, Delta: args}, nil)
	}
	if !json.Valid([]byte(args)) {
		yield(types.ModelChunk{}, &types.ModelError{
			Class:    types.ClassPermanent,
			Provider: providerName,
			Err:      fmt.Errorf("content block %d ended with invalid tool arguments %q", payload.Index, args),
		})
		return false
	}
	b.done = true
	b.emitted = true
	return yield(types.ModelChunk{
		Kind:    types.DeltaToolArgs,
		ToolUse: &types.ToolUse{ID: b.id, Name: b.name, Args: jsontext.Value(args)},
	}, nil)
}

func messageDelta(st *streamState, data []byte, yield func(types.ModelChunk, error) bool) bool {
	var payload struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return yield(types.ModelChunk{}, decodeErr(err))
	}
	if payload.Usage.OutputTokens > 0 {
		st.usage.OutputTokens = payload.Usage.OutputTokens
		st.usageSeen = true
	}
	if payload.Usage.CacheCreationInputTokens > 0 {
		st.usage.CacheWriteTokens = payload.Usage.CacheCreationInputTokens
		st.usageSeen = true
	}
	if !flushOpenBlocks(st, yield) {
		return false
	}
	if stop := payload.Delta.StopReason; stop != "" {
		return yield(types.ModelChunk{Finish: finishReason(stop)}, nil)
	}
	return true
}

// flushOpenBlocks surfaces pending bytes of still-open tool blocks as
// identity-only previews with empty Args, so truncation by max_tokens,
// message end or EOF never completes a call.
func flushOpenBlocks(st *streamState, yield func(types.ModelChunk, error) bool) bool {
	for _, b := range openBlocks(st) {
		args := b.args.String()
		if args == "" {
			continue
		}
		b.emitted = true
		if b.forced {
			if !yield(types.ModelChunk{Kind: types.DeltaText, Delta: args}, nil) {
				return false
			}
			continue
		}
		if !yield(types.ModelChunk{
			Kind:    types.DeltaToolArgs,
			Delta:   args,
			ToolUse: &types.ToolUse{ID: b.id, Name: b.name},
		}, nil) {
			return false
		}
	}
	return true
}

func openBlocks(st *streamState) []*blockState {
	if len(st.blocks) == 0 {
		return nil
	}
	idx := make([]int, 0, len(st.blocks))
	for i, b := range st.blocks {
		if b.tool && !b.done && !b.emitted {
			idx = append(idx, i)
		}
	}
	sort.Ints(idx)
	out := make([]*blockState, 0, len(idx))
	for _, i := range idx {
		out = append(out, st.blocks[i])
	}
	return out
}

func streamError(data []byte, yield func(types.ModelChunk, error) bool) bool {
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &payload)
	class := types.ClassifyProviderError(0, payload.Error.Type)
	yield(types.ModelChunk{}, &types.ModelError{
		Class:    class,
		Provider: providerName,
		Code:     payload.Error.Type,
		Err:      errors.New(payload.Error.Message),
	})
	return false
}

func finishReason(stop string) types.FinishReason {
	switch stop {
	case "end_turn", "stop_sequence":
		return types.FinishStop
	case "tool_use":
		return types.FinishToolUse
	case "max_tokens":
		return types.FinishMaxTokens
	case "refusal":
		return types.FinishRefusal
	default:
		return types.FinishStop
	}
}

func decodeErr(err error) error {
	return &types.ModelError{
		Class:    types.ClassPermanent,
		Provider: providerName,
		Err:      fmt.Errorf("decode response: %w", err),
	}
}
