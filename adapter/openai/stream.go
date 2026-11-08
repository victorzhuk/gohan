package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// toolCall accumulates one streamed tool call: the identity the provider
// sent and the argument fragments. The fragments stay apart from the
// identity so a preview never carries an executable argument set.
type toolCall struct {
	id        string
	name      string
	fragments []string
}

// args joins the accumulated fragments once, when the call completes.
func (t *toolCall) args() string {
	switch len(t.fragments) {
	case 0:
		return ""
	case 1:
		return t.fragments[0]
	default:
		return strings.Join(t.fragments, "")
	}
}

// streamRun carries the state of one streamed response: the pending tool
// calls, the served model and the usage accounting.
type streamRun struct {
	provider *Provider
	req      types.ModelRequest
	keyID    string
	yield    func(types.ModelChunk, error) bool
	ctx      context.Context
	forced   bool

	served    string
	streamed  strings.Builder
	pending   map[int]*toolCall
	order     []int
	completed []types.ToolUse
	wire      *wireUsage
	usage     bool
	reported  bool
}

// canceled reports the caller's cancellation: the context of the run
// ended the stream.
func (r *streamRun) canceled() bool {
	return r.ctx.Err() != nil
}

// reportCancel yields the terminal cancellation exactly once: no success
// chunk, no usage and no finish may follow it.
func (r *streamRun) reportCancel() bool {
	if r.reported {
		return false
	}
	r.reported = true
	r.yield(types.ModelChunk{}, r.ctx.Err())
	return false
}

// finalize closes the stream with the terminal reason: a cancelled
// context reports the cancellation instead of a success terminal.
func (r *streamRun) finalize(raw string) bool {
	if r.canceled() {
		return r.reportCancel()
	}
	return r.end(raw)
}

// stream decodes the SSE body and yields chunks on the caller's goroutine.
// Generate owns and closes the response body; stream returns at the first
// refusal and yields nothing after a terminal error.
func (p *Provider) stream(ctx context.Context, resp *http.Response, req types.ModelRequest, keyID string, yield func(types.ModelChunk, error) bool) {
	run := &streamRun{
		provider: p,
		req:      req,
		keyID:    keyID,
		yield:    yield,
		ctx:      ctx,
		forced:   p.forcedSchema(req),
		pending:  map[int]*toolCall{},
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		if run.canceled() {
			run.reportCancel()
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
			run.finalize("")
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
			run.served = chunk.Model
		}
		finish := ""
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil {
				finish = *choice.FinishReason
			}
		}
		if !run.consume(chunk) {
			return
		}
		if chunk.Usage != nil {
			run.wire = chunk.Usage
		}
		if finish == "" {
			continue
		}
		if !run.finalize(finish) {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		if run.canceled() {
			run.reportCancel()
			return
		}
		yield(types.ModelChunk{}, &types.ModelError{
			Class:    types.ClassTransient,
			Provider: p.profile.Name,
			Err:      fmt.Errorf("read stream: %w", err),
		})
		return
	}
	run.finalize("")
}

// forcedSchema reports whether the request forces a structured response.
// The condition is the one the request assembler uses to map the response
// schema: a forced response carries its payload as text and never
// completes a wire tool call.
func (p *Provider) forcedSchema(req types.ModelRequest) bool {
	return len(req.Options.ResponseSchema) > 0 && p.profile.Caps.Constrained
}

// consume streams the content, the reasoning and the tool-call previews of
// one chunk. It reports false when the consumer refused the chunk.
func (r *streamRun) consume(chunk wireChunk) bool {
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			if r.canceled() {
				return r.reportCancel()
			}
			r.streamed.WriteString(choice.Delta.Content)
			if !r.yield(types.ModelChunk{Kind: types.DeltaText, Delta: choice.Delta.Content}, nil) {
				return false
			}
		}
		reasoning := ""
		if choice.Delta.ReasoningContent != nil {
			reasoning = *choice.Delta.ReasoningContent
		} else if choice.Delta.Reasoning != nil {
			reasoning = *choice.Delta.Reasoning
		}
		if reasoning != "" {
			if r.canceled() {
				return r.reportCancel()
			}
			r.streamed.WriteString(reasoning)
			if !r.yield(types.ModelChunk{Kind: types.DeltaReasoning, Delta: reasoning}, nil) {
				return false
			}
		}
		for _, delta := range choice.Delta.ToolCalls {
			if !r.preview(delta) {
				return false
			}
		}
	}
	return true
}

// preview streams one argument fragment with the identity of its call. The
// arguments stay empty: a preview carries the identity, never an argument
// set that could reach tool execution. A forced response previews
// nothing: its payload surfaces as text.
func (r *streamRun) preview(delta wireToolCallDelta) bool {
	if r.canceled() {
		return r.reportCancel()
	}
	acc := r.call(delta.Index)
	if delta.ID != "" {
		acc.id = delta.ID
	}
	if delta.Function.Name != "" {
		acc.name = delta.Function.Name
	}
	fragment := delta.Function.Arguments
	if fragment == "" {
		return true
	}
	acc.fragments = append(acc.fragments, fragment)
	if r.forced {
		return true
	}
	return r.yield(types.ModelChunk{
		Kind:    types.DeltaToolArgs,
		Delta:   fragment,
		ToolUse: &types.ToolUse{ID: acc.id, Name: acc.name},
	}, nil)
}

// call returns the accumulator of one tool-call index. The identity the
// provider sent stays with the index, so a fragment without an identity
// still previews under the call it belongs to.
func (r *streamRun) call(index int) *toolCall {
	acc, ok := r.pending[index]
	if !ok {
		acc = &toolCall{}
		r.pending[index] = acc
		r.order = append(r.order, index)
	}
	return acc
}

// completeBatch validates every pending call before any of them is emitted:
// a batch that carries an invalid argument set is refused whole, so no
// sibling of a broken call ever reaches tool execution. A forced response
// completes no call at all: its payload surfaces as text.
func (r *streamRun) completeBatch() bool {
	calls := r.pendingCalls()
	if r.forced {
		for i := range calls {
			if r.canceled() {
				return r.reportCancel()
			}
			r.streamed.WriteString(string(calls[i].Args))
			if !r.yield(types.ModelChunk{Kind: types.DeltaText, Delta: string(calls[i].Args)}, nil) {
				return false
			}
		}
		r.clear()
		return true
	}
	for _, call := range calls {
		if !json.Valid(call.Args) {
			r.fail(types.ClassPermanent, fmt.Errorf("tool call %q carries invalid arguments %q", call.ID, string(call.Args)))
			return false
		}
	}
	for i := range calls {
		if r.canceled() {
			return r.reportCancel()
		}
		r.completed = append(r.completed, calls[i])
		if !r.yield(types.ModelChunk{ToolUse: &calls[i]}, nil) {
			return false
		}
	}
	r.clear()
	return true
}

// pendingCalls returns the accumulated calls in provider order.
func (r *streamRun) pendingCalls() []types.ToolUse {
	calls := make([]types.ToolUse, 0, len(r.order))
	for _, index := range r.order {
		acc := r.pending[index]
		calls = append(calls, types.ToolUse{
			ID:   acc.id,
			Name: acc.name,
			Args: json.RawMessage(acc.args()),
		})
	}
	return calls
}

// abandon drops every pending call: an argument set the provider never
// finished never reaches tool execution.
func (r *streamRun) abandon() {
	r.clear()
}

// clear drops the pending calls: they are completed or abandoned, never
// pending any more.
func (r *streamRun) clear() {
	clear(r.pending)
	r.order = nil
}

// end closes the stream: it completes or abandons the pending calls and
// reports the terminal reason and the usage exactly once. It reports false
// when the stream must stop: the consumer refused the terminal chunk or the
// terminal was a failure.
func (r *streamRun) end(raw string) bool {
	switch raw {
	case "tool_calls":
		if !r.completeBatch() {
			return false
		}
		return r.terminal(types.FinishToolUse, false)
	case "length":
		r.abandon()
		return r.terminal(types.FinishMaxTokens, false)
	case "content_filter":
		r.abandon()
		return r.terminal(types.FinishRefusal, false)
	case "stop":
		if len(r.pending) > 0 {
			r.abandon()
			return r.terminal(types.FinishMaxTokens, false)
		}
		return r.terminal(types.FinishStop, false)
	case "":
		finish := types.FinishReason("")
		if len(r.pending) > 0 {
			r.abandon()
			finish = types.FinishMaxTokens
		}
		return r.terminal(finish, true)
	case "error":
		r.fail(types.ClassTransient, fmt.Errorf("provider ended the stream with an error"))
		return false
	default:
		r.fail(types.ClassTransient, fmt.Errorf("provider ended the stream with an unknown reason %q", raw))
		return false
	}
}

// terminal yields the last chunk of the stream: the finish reason and the
// usage. The estimate fills a missing usage only at the clean end of a
// stream, never at a finish reason.
func (r *streamRun) terminal(finish types.FinishReason, estimate bool) bool {
	if r.canceled() {
		return r.reportCancel()
	}
	out := types.ModelChunk{Finish: finish}
	if !r.usage {
		use := r.provider.usageFrom(r.wire, r.keyID, r.served)
		if use == nil && estimate {
			use = r.provider.estimatedUsage(r.req, r.keyID, r.streamed.String())
		}
		if use != nil {
			gohan.CountProviderCalls(r.req.Tools, r.completed, use)
			out.Usage = use
			r.usage = true
		}
	}
	if out == (types.ModelChunk{}) {
		return true
	}
	return r.yield(out, nil)
}

// fail reports one terminal model error. The stream ends with it: no usage
// and no completion follows a failure.
func (r *streamRun) fail(class types.ErrorClass, err error) {
	r.yield(types.ModelChunk{}, &types.ModelError{
		Class:    class,
		Provider: r.provider.profile.Name,
		Err:      err,
	})
}

// usageFrom fills the usage the provider reported. A provider that reports
// no usage leaves the report to the estimate rule: a zero report never
// reaches the accounting.
func (p *Provider) usageFrom(wire *wireUsage, keyID, served string) *types.Usage {
	if wire == nil || (wire.PromptTokens == 0 && wire.CompletionTokens == 0) {
		return nil
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
		if wire.PromptTokensDetails != nil {
			use.CachedInputTokens = wire.PromptTokensDetails.CachedTokens
		}
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
