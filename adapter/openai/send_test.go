package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/adapter/openai"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/conformance"
)

func proofCaps(mod func(*types.Caps)) types.Caps {
	caps := fixtureCaps(conformance.Fixture{Name: "proof", Kind: conformance.FixtureUsage})
	if mod != nil {
		mod(&caps)
	}
	return caps
}

func proofModel(t *testing.T, caps types.Caps, keys types.ProviderKeySource, tp *captureTransport) *openai.Provider {
	t.Helper()
	p, err := openai.New(openai.Config{
		Profile: types.ModelProfile{
			Name:    fixtureProfile,
			Version: fixtureVersion,
			Keys:    types.PlatformKey,
			Caps:    caps,
		},
		Keys:      keys,
		Transport: tp,
	})
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	return p
}

func proofRequest(fixture string) types.ModelRequest {
	req := types.ModelRequest{
		Messages: []types.Message{
			{ID: "proof", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "proof probe"}}},
		},
	}
	req.Options.Extra = map[string]any{conformance.FixtureKey: fixture}
	return req
}

func collect(t *testing.T, m *openai.Provider, req types.ModelRequest) []types.ModelChunk {
	t.Helper()
	return collectIn(t, context.Background(), m, req)
}

func collectIn(t *testing.T, ctx context.Context, m *openai.Provider, req types.ModelRequest) []types.ModelChunk {
	t.Helper()
	var chunks []types.ModelChunk
	for chunk, err := range m.Generate(ctx, req) {
		if err != nil {
			t.Fatalf("stream failed: %v", err)
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func drainErr(m *openai.Provider, req types.ModelRequest) error {
	for _, err := range m.Generate(context.Background(), req) {
		if err != nil {
			return err
		}
	}
	return nil
}

type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) attrValues() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	var walk func(a slog.Attr)
	walk = func(a slog.Attr) {
		v := a.Value
		out = append(out, v.String())
		if v.Kind() == slog.KindAny {
			out = append(out, fmt.Sprintf("%+v", v.Any()))
		}
		if g := v.Group(); g != nil {
			for _, inner := range g {
				walk(inner)
			}
		}
	}
	for _, rec := range h.records {
		rec.Attrs(func(a slog.Attr) bool { walk(a); return true })
	}
	return out
}

func (h *captureHandler) droppedCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, rec := range h.records {
		rec.Attrs(func(a slog.Attr) bool {
			if a.Key == "gohan.cache.breakpoints_dropped" {
				n = int(a.Value.Int64())
			}
			return true
		})
	}
	return n
}

func withSlog(t *testing.T, h *captureHandler) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func lastUsage(t *testing.T, chunks []types.ModelChunk) *types.Usage {
	t.Helper()
	if len(chunks) == 0 {
		t.Fatal("no chunks streamed")
	}
	u := chunks[len(chunks)-1].Usage
	if u == nil {
		t.Fatal("no usage on final chunk")
	}
	return u
}

func TestExtraCollision(t *testing.T) {
	tp := &captureTransport{}
	p := proofModel(t, proofCaps(nil), simpleKeys(fixtureKeyID, fixtureToken), tp)
	req := proofRequest("extra collision")
	req.Options.Extra["max_tokens"] = 128
	var modelErr *types.ModelError
	for _, err := range p.Generate(context.Background(), req) {
		if err == nil {
			t.Fatal("governed extra key streamed a chunk")
		}
		if !errors.As(err, &modelErr) {
			t.Fatalf("error is %T, want *ModelError", err)
		}
	}
	if modelErr.Class != types.ClassPermanent {
		t.Errorf("class %d, want permanent", modelErr.Class)
	}
	if modelErr.Code != "gohan.extra_governed_key" {
		t.Errorf("code %q, want gohan.extra_governed_key", modelErr.Code)
	}
	if !strings.Contains(modelErr.Error(), "max_tokens") {
		t.Errorf("error %q does not name the governed key", modelErr.Error())
	}
	if len(tp.requests) != 0 {
		t.Errorf("transport called %d times before refusal", len(tp.requests))
	}
}

func TestSchemaCapability(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)

	t.Run("refuses before send without constrained capability", func(t *testing.T) {
		tp := &captureTransport{}
		p := proofModel(t, proofCaps(func(c *types.Caps) { c.Constrained = false }), simpleKeys(fixtureKeyID, fixtureToken), tp)
		req := proofRequest("schema unsupported")
		req.Options.ResponseSchema = schema
		err := drainErr(p, req)
		if err == nil {
			t.Fatal("schema on an unconstrained profile streamed")
		}
		var modelErr *types.ModelError
		if !errors.As(err, &modelErr) {
			t.Fatalf("error is %T, want *ModelError", err)
		}
		if modelErr.Class != types.ClassPermanent {
			t.Errorf("class %d, want permanent", modelErr.Class)
		}
		if modelErr.Code != "output_schema_unsupported" {
			t.Errorf("code %q, want output_schema_unsupported", modelErr.Code)
		}
		if len(tp.requests) != 0 {
			t.Errorf("transport called %d times before refusal", len(tp.requests))
		}
	})

	t.Run("emits response_format on a constrained profile", func(t *testing.T) {
		tp := &captureTransport{}
		p := proofModel(t, proofCaps(nil), simpleKeys(fixtureKeyID, fixtureToken), tp)
		req := proofRequest("usage estimated")
		req.Options.ResponseSchema = schema
		collect(t, p, req)
		if len(tp.requests) != 1 {
			t.Fatalf("transport called %d times, want 1", len(tp.requests))
		}
		var body struct {
			ResponseFormat map[string]any `json:"response_format"`
		}
		if err := json.Unmarshal(tp.requests[0].Body, &body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.ResponseFormat["type"] != "json_schema" {
			t.Errorf("response_format type %v, want json_schema", body.ResponseFormat["type"])
		}
		inner, ok := body.ResponseFormat["json_schema"].(map[string]any)
		if !ok {
			t.Fatalf("response_format carries no json_schema object: %v", body.ResponseFormat)
		}
		if inner["strict"] != true {
			t.Errorf("json_schema strict %v, want true", inner["strict"])
		}
		if _, ok := inner["schema"]; !ok {
			t.Errorf("json_schema carries no schema")
		}
	})
}

func TestCacheModes(t *testing.T) {
	cacheBreakMessage := func() types.Message {
		return types.Message{Role: types.RoleUser, Blocks: []types.Block{types.CacheBreak{}}}
	}
	const marker = "gohan:cache_breakpoint"

	markers := func(t *testing.T, req capturedRequest) []int {
		t.Helper()
		var at []int
		for i, msg := range req.Messages {
			if msg["role"] == "system" && msg["content"] == marker {
				at = append(at, i)
			}
		}
		return at
	}

	t.Run("explicit maps breakpoints in order and drops the rest", func(t *testing.T) {
		h := &captureHandler{}
		withSlog(t, h)
		tp := &captureTransport{}
		p := proofModel(t, proofCaps(func(c *types.Caps) {
			c.Cache = types.CacheExplicit
			c.CacheBreakpoints = 2
		}), simpleKeys(fixtureKeyID, fixtureToken), tp)
		req := proofRequest("cache breakpoints")
		req.Messages = append(req.Messages, cacheBreakMessage(), cacheBreakMessage(), cacheBreakMessage())
		u := lastUsage(t, collect(t, p, req))
		if u.CacheWriteTokens != 12 {
			t.Errorf("CacheWriteTokens %d, want 12", u.CacheWriteTokens)
		}
		at := markers(t, tp.requests[0])
		if len(at) != 2 {
			t.Fatalf("%d cache markers in the outgoing body, want 2", len(at))
		}
		if at[0] >= at[1] {
			t.Errorf("cache markers out of order: %v", at)
		}
		if n := h.droppedCount(); n != 1 {
			t.Errorf("gohan.cache.breakpoints_dropped %d, want 1", n)
		}
	})

	t.Run("auto drops breakpoints and fills cached input", func(t *testing.T) {
		h := &captureHandler{}
		withSlog(t, h)
		tp := &captureTransport{}
		p := proofModel(t, proofCaps(func(c *types.Caps) { c.Cache = types.CacheAuto }), simpleKeys(fixtureKeyID, fixtureToken), tp)
		req := proofRequest("usage reported")
		req.Messages = append(req.Messages, cacheBreakMessage(), cacheBreakMessage(), cacheBreakMessage())
		u := lastUsage(t, collect(t, p, req))
		if u.CachedInputTokens != 20 {
			t.Errorf("CachedInputTokens %d, want 20", u.CachedInputTokens)
		}
		if u.CacheWriteTokens != 0 {
			t.Errorf("CacheWriteTokens %d, want 0 under auto caching", u.CacheWriteTokens)
		}
		if at := markers(t, tp.requests[0]); len(at) != 0 {
			t.Errorf("auto caching emitted %d cache markers, want none", len(at))
		}
		if n := h.droppedCount(); n != 3 {
			t.Errorf("gohan.cache.breakpoints_dropped %d, want 3", n)
		}
	})

	t.Run("none drops breakpoints and reports no cache writes", func(t *testing.T) {
		h := &captureHandler{}
		withSlog(t, h)
		tp := &captureTransport{}
		p := proofModel(t, proofCaps(func(c *types.Caps) { c.Cache = types.CacheNone }), simpleKeys(fixtureKeyID, fixtureToken), tp)
		req := proofRequest("usage estimated")
		req.Messages = append(req.Messages, cacheBreakMessage(), cacheBreakMessage())
		u := lastUsage(t, collect(t, p, req))
		if u.CacheWriteTokens != 0 {
			t.Errorf("CacheWriteTokens %d, want 0 under no caching", u.CacheWriteTokens)
		}
		if at := markers(t, tp.requests[0]); len(at) != 0 {
			t.Errorf("no caching emitted %d cache markers, want none", len(at))
		}
	})
}

func TestMetadataCostTags(t *testing.T) {
	t.Run("model.cost-tags-forwarded-as-metadata", func(t *testing.T) {
		tp := &captureTransport{}
		p := proofModel(t, proofCaps(nil), simpleKeys(fixtureKeyID, fixtureToken), tp)
		req := proofRequest("usage reported")
		req.Metadata.CostTags = types.CostTags{
			Feature:     "search",
			Environment: "prod",
			CostCenter:  "cc-9",
		}
		collect(t, p, req)
		if len(tp.requests) != 1 {
			t.Fatalf("transport called %d times, want 1", len(tp.requests))
		}
		var body struct {
			Metadata map[string]any `json:"metadata"`
		}
		if err := json.Unmarshal(tp.requests[0].Body, &body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		want := map[string]any{"feature": "search", "environment": "prod", "cost_center": "cc-9"}
		if !reflect.DeepEqual(body.Metadata, want) {
			t.Errorf("metadata %v, want %v", body.Metadata, want)
		}
	})

	t.Run("zero cost tags send no metadata", func(t *testing.T) {
		tp := &captureTransport{}
		p := proofModel(t, proofCaps(nil), simpleKeys(fixtureKeyID, fixtureToken), tp)
		collect(t, p, proofRequest("usage reported"))
		if len(tp.requests) != 1 {
			t.Fatalf("transport called %d times, want 1", len(tp.requests))
		}
		var body map[string]any
		if err := json.Unmarshal(tp.requests[0].Body, &body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, ok := body["metadata"]; ok {
			t.Errorf("zero cost tags produced a metadata entry: %v", body["metadata"])
		}
	})
}

func TestProviderToolCalls(t *testing.T) {
	tp := &captureTransport{}
	p := proofModel(t, proofCaps(nil), simpleKeys(fixtureKeyID, fixtureToken), tp)
	req := proofRequest("provider tools")
	req.Tools = []types.ToolSpec{{Name: "web_search", Executor: types.ByProvider}}
	chunks := collect(t, p, req)
	var toolUses int
	for _, chunk := range chunks {
		if chunk.ToolUse != nil {
			toolUses++
		}
	}
	if toolUses != 2 {
		t.Errorf("%d tool-use chunks streamed, want 2", toolUses)
	}
	u := lastUsage(t, chunks)
	if u.ProviderToolCalls["web_search"] != 2 {
		t.Errorf("ProviderToolCalls %v, want web_search=2", u.ProviderToolCalls)
	}
}

func TestRetryAfterPropagationAndRawFidelity(t *testing.T) {
	t.Run("retry-after reaches the model error", func(t *testing.T) {
		p := proofModel(t, proofCaps(nil), simpleKeys(fixtureKeyID, fixtureToken), &captureTransport{})
		err := drainErr(p, proofRequest("rate limited with retry after"))
		if err == nil {
			t.Fatal("rate limited response was swallowed")
		}
		var modelErr *types.ModelError
		if !errors.As(err, &modelErr) {
			t.Fatalf("error is %T, want *ModelError", err)
		}
		if modelErr.Class != types.ClassRateLimited {
			t.Errorf("class %d, want rate limited", modelErr.Class)
		}
		if modelErr.RetryAfter != 7e9 {
			t.Errorf("RetryAfter %s, want 7s", modelErr.RetryAfter)
		}
	})

	t.Run("raw block re-sends unchanged to the same provider", func(t *testing.T) {
		tp := &captureTransport{}
		p := proofModel(t, proofCaps(nil), simpleKeys(fixtureKeyID, fixtureToken), tp)
		req := proofRequest("raw round trip")
		raw := map[string]any{"acme_kind": "trace"}
		req.Messages = append(req.Messages, types.Message{
			Role:   types.RoleAssistant,
			Blocks: []types.Block{types.Raw{Provider: fixtureProfile, Value: raw}},
		})
		collect(t, p, req)
		if len(tp.requests) != 1 {
			t.Fatalf("transport called %d times, want 1", len(tp.requests))
		}
		for _, msg := range tp.requests[0].Messages {
			if msg["acme_kind"] == "trace" {
				return
			}
		}
		t.Errorf("outgoing body carries no unchanged raw block: %v", tp.requests[0].Messages)
	})
}
