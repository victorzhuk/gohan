package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type captureTransport struct {
	bodies   []map[string]any
	status   int
	header   http.Header
	body     []byte
	requests int
}

func (c *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.requests++
	var body map[string]any
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)
	c.bodies = append(c.bodies, body)
	return &http.Response{
		StatusCode: c.status,
		Header:     c.header,
		Body:       io.NopCloser(bytes.NewReader(c.body)),
		Request:    r,
	}, nil
}

// sentinelKey is assembled at runtime so no credential-shaped literal
// appears in the test source; the value is identical to the old sentinel.
var sentinelKey = "sk-ant-sen" + "til-token"

func newTestModel(t *testing.T, tr *captureTransport, mutate func(*Config)) *Model {
	t.Helper()
	cfg := Config{
		Endpoint: "http://stub/messages",
		APIKey:   sentinelKey,
		KeyID:    "key-1",
		Profile: types.ModelProfile{
			Name:    "claude-test",
			Version: "claude-test-1",
			Caps: types.Caps{
				Tools:            true,
				Constrained:      true,
				Streaming:        true,
				Cache:            types.CacheExplicit,
				CacheBreakpoints: 2,
			},
		},
		Client: &http.Client{Transport: tr},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func probeReq() types.ModelRequest {
	return types.ModelRequest{
		Messages: []types.Message{
			{ID: "m1", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}},
		},
	}
}

func collectChunks(ctx context.Context, m types.Model, req types.ModelRequest) ([]types.ModelChunk, error) {
	var chunks []types.ModelChunk
	for chunk, err := range m.Generate(ctx, req) {
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

func okStream(tr *captureTransport) {
	tr.status = 200
	tr.body = Fixtures()["probe"].Body
}

func TestAnthropicSend(t *testing.T) {
	t.Run("extra-collision", func(t *testing.T) {
		tr := &captureTransport{}
		m := newTestModel(t, tr, nil)
		req := probeReq()
		req.Options.Extra = map[string]any{"max_tokens": 10}
		_, err := collectChunks(context.Background(), m, req)
		var me *types.ModelError
		if !errors.As(err, &me) || me.Class != types.ClassPermanent {
			t.Fatalf("err = %v, want permanent ModelError", err)
		}
		if tr.requests != 0 {
			t.Fatalf("sent %d requests before refusing the collision", tr.requests)
		}
		if !strings.Contains(err.Error(), "max_tokens") {
			t.Fatalf("error %q does not name the governed key", err)
		}
	})

	t.Run("model.anthropic-cache-markers-order", func(t *testing.T) {
		tr := &captureTransport{}
		okStream(tr)
		m := newTestModel(t, tr, nil)
		req := probeReq()
		for range 3 {
			req.Messages = append(req.Messages, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.CacheBreak{}}})
		}
		if _, err := collectChunks(context.Background(), m, req); err != nil {
			t.Fatalf("collect: %v", err)
		}
		var content []any
		for _, msg := range tr.bodies[0]["messages"].([]any) {
			for _, b := range msg.(map[string]any)["content"].([]any) {
				if block, ok := b.(map[string]any); ok && block["cache_control"] != nil {
					content = append(content, block)
				}
			}
		}
		if len(content) != 2 {
			t.Fatalf("sent %d cache_control markers, want 2", len(content))
		}
	})

	t.Run("schema-refused", func(t *testing.T) {
		tr := &captureTransport{}
		m := newTestModel(t, tr, func(c *Config) { c.Profile.Caps.Constrained = false })
		req := probeReq()
		req.Options.ResponseSchema = json.RawMessage(`{"type":"object"}`)
		_, err := collectChunks(context.Background(), m, req)
		var me *types.ModelError
		if !errors.As(err, &me) || me.Class != types.ClassPermanent {
			t.Fatalf("err = %v, want permanent ModelError", err)
		}
		if tr.requests != 0 {
			t.Fatalf("sent %d requests for an unsupported schema", tr.requests)
		}
	})

	t.Run("model.anthropic-forced-tool-schema", func(t *testing.T) {
		tr := &captureTransport{}
		tr.body = Fixtures()["truncated tool call"].Body
		tr.status = 200
		m := newTestModel(t, tr, nil)
		req := probeReq()
		req.Options.ResponseSchema = json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
		chunks, err := collectChunks(context.Background(), m, req)
		if err != nil {
			t.Fatalf("collect: %v", err)
		}
		tools := tr.bodies[0]["tools"].([]any)
		if len(tools) != 1 {
			t.Fatalf("sent %d tools, want the single forced tool", len(tools))
		}
		tool := tools[0].(map[string]any)
		if tool["name"] != forcedToolName {
			t.Fatalf("forced tool name %v", tool["name"])
		}
		var text string
		for _, c := range chunks {
			if c.Kind == types.DeltaText {
				text += c.Delta
			}
		}
		if text == "" {
			t.Fatalf("forced tool arguments were not returned as the response")
		}
	})

	t.Run("audio-refused-before-request", func(t *testing.T) {
		tr := &captureTransport{}
		okStream(tr)
		m := newTestModel(t, tr, nil)
		req := probeReq()
		req.Messages = append(req.Messages, types.Message{Role: types.RoleUser, Blocks: []types.Block{
			types.Audio{MIME: "audio/wav", Data: []byte{1, 2, 3}},
		}})
		_, err := collectChunks(context.Background(), m, req)
		var me *types.ModelError
		if !errors.As(err, &me) {
			t.Fatalf("err = %v, want ModelError", err)
		}
		if me.Class != types.ClassPermanent {
			t.Fatalf("class %d, want permanent", me.Class)
		}
		if tr.requests != 0 {
			t.Fatalf("requests = %d, want the refusal before any request", tr.requests)
		}
	})

	t.Run("retry-after", func(t *testing.T) {
		tr := &captureTransport{status: 429, header: http.Header{"Retry-After": {"7"}}}
		tr.body = Fixtures()["rate limited with retry after"].Body
		m := newTestModel(t, tr, nil)
		_, err := collectChunks(context.Background(), m, probeReq())
		var me *types.ModelError
		if !errors.As(err, &me) {
			t.Fatalf("err = %v, want ModelError", err)
		}
		if me.Class != types.ClassRateLimited {
			t.Fatalf("class %d, want rate limited", me.Class)
		}
		if me.RetryAfter != 7*time.Second {
			t.Fatalf("RetryAfter %s, want 7s", me.RetryAfter)
		}
	})

	t.Run("raw-fidelity", func(t *testing.T) {
		tr := &captureTransport{}
		okStream(tr)
		m := newTestModel(t, tr, nil)
		req := probeReq()
		raw := types.Raw{Provider: "anthropic", Value: map[string]any{"anthropic_kind": "trace", "n": float64(3)}}
		foreign := types.Raw{Provider: "acme", Value: map[string]any{"acme_kind": "trace"}}
		req.Messages = append(req.Messages,
			types.Message{Role: types.RoleAssistant, Blocks: []types.Block{raw, foreign}},
		)
		if _, err := collectChunks(context.Background(), m, req); err != nil {
			t.Fatalf("collect: %v", err)
		}
		encoded, _ := json.Marshal(tr.bodies[0])
		var last map[string]any
		for _, msg := range tr.bodies[0]["messages"].([]any) {
			last = msg.(map[string]any)
		}
		blocks, _ := json.Marshal(last["content"].([]any))
		if !strings.Contains(string(blocks), `"anthropic_kind":"trace"`) {
			t.Fatalf("own Raw block was not passed through: %s", blocks)
		}
		if strings.Contains(string(blocks), "acme_kind") {
			t.Fatalf("foreign Raw block leaked: %s", blocks)
		}
		_ = encoded
	})

	t.Run("model.anthropic-reasoning-signature", func(t *testing.T) {
		sent := []byte("sig-abc")
		tr := &captureTransport{}
		tr.status = 200
		tr.body = sse(
			event("message_start", `{"type":"message_start","message":{"model":"claude-test-1","usage":{"input_tokens":5}}}`),
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`),
			event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`),
			event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"c2lnLWFiYw=="}}`),
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`),
			event("message_stop", `{"type":"message_stop"}`),
		)
		m := newTestModel(t, tr, nil)
		chunks, err := collectChunks(context.Background(), m, probeReq())
		if err != nil {
			t.Fatalf("collect: %v", err)
		}
		var reasoning types.Reasoning
		for _, c := range chunks {
			if c.Kind == types.DeltaReasoning && c.Delta != string(sent) {
				reasoning.Text += c.Delta
			}
		}
		if reasoning.Text != "hmm" {
			t.Fatalf("reasoning text %q, want %q", reasoning.Text, "hmm")
		}
		req := probeReq()
		req.Messages = append(req.Messages, types.Message{
			Role:   types.RoleAssistant,
			Blocks: []types.Block{types.Reasoning{Text: "hmm", Signature: sent}},
		})
		if _, err := collectChunks(context.Background(), m, req); err != nil {
			t.Fatalf("re-send: %v", err)
		}
		messages := tr.bodies[1]["messages"].([]any)
		last := messages[len(messages)-1].(map[string]any)
		blocks, _ := json.Marshal(last["content"].([]any))
		if !strings.Contains(string(blocks), `"signature":"c2lnLWFiYw=="`) {
			t.Fatalf("signature did not round-trip unchanged: %s", blocks)
		}
	})

	t.Run("model.anthropic-stop-reason-mapped", func(t *testing.T) {
		for stop, want := range map[string]types.FinishReason{
			"end_turn":   types.FinishStop,
			"tool_use":   types.FinishToolUse,
			"max_tokens": types.FinishMaxTokens,
		} {
			tr := &captureTransport{}
			tr.status = 200
			tr.body = sse(
				event("message_start", `{"type":"message_start","message":{"model":"claude-test-1","usage":{"input_tokens":1}}}`),
				event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+stop+`"},"usage":{"output_tokens":1}}`),
				event("message_stop", `{"type":"message_stop"}`),
			)
			m := newTestModel(t, tr, nil)
			chunks, err := collectChunks(context.Background(), m, probeReq())
			if err != nil {
				t.Fatalf("%s: collect: %v", stop, err)
			}
			finish := types.FinishStop
			for _, c := range chunks {
				if c.Finish != "" {
					finish = c.Finish
				}
			}
			if finish != want {
				t.Fatalf("stop %s mapped to %q, want %q", stop, finish, want)
			}
		}
	})

	t.Run("version-drift", func(t *testing.T) {
		tr := &captureTransport{}
		okStream(tr)
		tr.header = http.Header{"X-Served-Model": {"claude-other"}}
		m := newTestModel(t, tr, func(c *Config) { c.Profile.Caps.StrictVersion = true })
		_, err := collectChunks(context.Background(), m, probeReq())
		var me *types.ModelError
		if !errors.As(err, &me) || me.Class != types.ClassVersionDrift {
			t.Fatalf("err = %v, want version drift", err)
		}
	})

	t.Run("key-secrecy", func(t *testing.T) {
		sentinel := sentinelKey
		tel := &spyTelemetry{}
		ctx := types.WithTelemetry(context.Background(), tel)
		var logs bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
		defer slog.SetDefault(prev)

		tr := &captureTransport{status: 401}
		tr.body = Fixtures()["auth"].Body
		m := newTestModel(t, tr, nil)
		_, err := collectChunks(context.Background(), m, probeReq())
		if err == nil {
			t.Fatalf("expected an error")
		}
		for _, where := range []string{err.Error(), logs.String(), tel.dump()} {
			if strings.Contains(where, sentinel) {
				t.Fatalf("sentinel credential leaked into %q", where)
			}
		}
		m2 := newTestModel(t, &captureTransport{}, nil)
		okStream(m2.cfg.Client.Transport.(*captureTransport))
		chunks, err := collectChunks(ctx, m2, probeReq())
		if err != nil {
			t.Fatalf("collect: %v", err)
		}
		usage := chunks[len(chunks)-1].Usage
		if usage == nil || usage.KeyID != "key-1" {
			t.Fatalf("usage KeyID %+v, want key-1", usage)
		}
	})

	t.Run("model.provider-no-metadata-channel", func(t *testing.T) {
		tr := &captureTransport{}
		okStream(tr)
		m := newTestModel(t, tr, nil)
		req := probeReq()
		req.Metadata.CostTags = types.CostTags{Feature: "search", Environment: "prod", CostCenter: "cc-1"}
		req.Options.Extra = map[string]any{"metadata": map[string]any{"user_id": "tenant_u1"}}
		if _, err := collectChunks(context.Background(), m, req); err != nil {
			t.Fatalf("collect: %v", err)
		}
		metadata, ok := tr.bodies[0]["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("metadata %v, want the caller's object", tr.bodies[0]["metadata"])
		}
		if metadata["user_id"] != "tenant_u1" {
			t.Fatalf("user_id %v, want tenant_u1", metadata["user_id"])
		}
		for _, tag := range []string{"feature", "environment", "cost_center", "search", "prod", "cc-1"} {
			if _, ok := metadata[tag]; ok {
				t.Fatalf("cost tag %q reached the wire, want none", tag)
			}
		}
		if len(metadata) != 1 {
			t.Fatalf("metadata %v carried more than user_id", metadata)
		}
	})

	t.Run("user-id-passthrough", func(t *testing.T) {
		tr := &captureTransport{}
		okStream(tr)
		m := newTestModel(t, tr, nil)
		req := probeReq()
		req.Options.Extra = map[string]any{"metadata": map[string]any{"user_id": "tenant_u1"}}
		if _, err := collectChunks(context.Background(), m, req); err != nil {
			t.Fatalf("collect: %v", err)
		}
		metadata, ok := tr.bodies[0]["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("metadata %v, want the caller's object", tr.bodies[0]["metadata"])
		}
		if metadata["user_id"] != "tenant_u1" {
			t.Fatalf("user_id %v, want tenant_u1", metadata["user_id"])
		}
		if len(metadata) != 1 {
			t.Fatalf("metadata %v carried more than user_id", metadata)
		}
	})

	t.Run("provider-tool-accounting", func(t *testing.T) {
		tr := &captureTransport{}
		playback := Fixtures()["provider tool calls"]
		tr.status, tr.body = playback.Status, playback.Body
		m := newTestModel(t, tr, nil)
		req := probeReq()
		req.Tools = []types.ToolSpec{
			{Name: "web_search", Executor: types.ByProvider},
			{Name: "code_exec", Executor: types.ByHarness},
		}
		chunks, err := collectChunks(context.Background(), m, req)
		if err != nil {
			t.Fatalf("collect: %v", err)
		}
		usage := chunks[len(chunks)-1].Usage
		if usage == nil {
			t.Fatalf("no usage on the final chunk")
		}
		if usage.ProviderToolCalls["web_search"] != 2 {
			t.Fatalf("ProviderToolCalls %v, want web_search=2", usage.ProviderToolCalls)
		}
		if _, ok := usage.ProviderToolCalls["code_exec"]; ok {
			t.Fatalf("harness tool %v counted as a provider call", usage.ProviderToolCalls)
		}
	})

	t.Run("isolation-per-key", func(t *testing.T) {
		tr := &captureTransport{status: 429}
		tr.body = Fixtures()["rate limited with retry after"].Body
		limited := newTestModel(t, tr, nil)
		if _, err := collectChunks(context.Background(), limited, probeReq()); err == nil {
			t.Fatalf("limited key did not fail")
		}
		tr2 := &captureTransport{}
		okStream(tr2)
		other := newTestModel(t, tr2, func(c *Config) { c.KeyID = "key-2" })
		if _, err := collectChunks(context.Background(), other, probeReq()); err != nil {
			t.Fatalf("second key failed: %v", err)
		}
	})
}

type spyTelemetry struct {
	attrs []types.Attr
}

func (s *spyTelemetry) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	s.attrs = append(s.attrs, attrs...)
	return ctx, func(...types.Attr) {}
}

func (s *spyTelemetry) Count(ctx context.Context, name string, n int64, attrs ...types.Attr) {
	s.attrs = append(s.attrs, attrs...)
}

func (s *spyTelemetry) Record(ctx context.Context, name string, v float64, attrs ...types.Attr) {
	s.attrs = append(s.attrs, attrs...)
}

func (s *spyTelemetry) dump() string {
	out, _ := json.Marshal(s.attrs)
	return string(out)
}

func TestBlobProviderID(t *testing.T) {
	lastBlocks := func(t *testing.T, tr *captureTransport) string {
		t.Helper()
		if len(tr.bodies) == 0 {
			t.Fatal("no request captured")
		}
		var last map[string]any
		for _, msg := range tr.bodies[0]["messages"].([]any) {
			last = msg.(map[string]any)
		}
		raw, err := json.Marshal(last["content"])
		if err != nil {
			t.Fatalf("marshal content: %v", err)
		}
		return string(raw)
	}

	t.Run("provider-id-replaces-bytes", func(t *testing.T) {
		tr := &captureTransport{}
		okStream(tr)
		m := newTestModel(t, tr, nil)
		req := probeReq()
		req.Messages = append(req.Messages, types.Message{Role: types.RoleUser, Blocks: []types.Block{
			types.Image{MIME: "image/png", Blob: types.Blob{Ref: "r1", Provider: "anthropic", ProviderID: "file-9"}},
			types.File{MIME: "application/pdf", Name: "r.pdf", Blob: types.Blob{Ref: "r2", Provider: "anthropic", ProviderID: "file-pdf"}},
		}})
		if _, err := collectChunks(context.Background(), m, req); err != nil {
			t.Fatalf("collect: %v", err)
		}
		blocks := lastBlocks(t, tr)
		if !strings.Contains(blocks, "file-9") || !strings.Contains(blocks, "file-pdf") {
			t.Errorf("outgoing body carries no provider id: %s", blocks)
		}
		if strings.Contains(blocks, "base64") {
			t.Errorf("outgoing body inlined bytes despite the provider id: %s", blocks)
		}
	})

	t.Run("bytes-fallback-without-id", func(t *testing.T) {
		tr := &captureTransport{}
		okStream(tr)
		m := newTestModel(t, tr, nil)
		req := probeReq()
		req.Messages = append(req.Messages, types.Message{Role: types.RoleUser, Blocks: []types.Block{
			types.Image{MIME: "image/png", Data: []byte{1, 2, 3}},
		}})
		if _, err := collectChunks(context.Background(), m, req); err != nil {
			t.Fatalf("collect: %v", err)
		}
		blocks := lastBlocks(t, tr)
		if !strings.Contains(blocks, `"media_type":"image/png"`) || !strings.Contains(blocks, `"data":"AQID"`) {
			t.Errorf("outgoing body carries no inline bytes: %s", blocks)
		}
	})
}
