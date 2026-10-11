package std_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

type explainVerifyTool struct{ spec types.ToolSpec }

func (t explainVerifyTool) Spec() types.ToolSpec { return t.spec }

func (t explainVerifyTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	return types.ToolResult{}, nil
}

func explainHandlerStack(t *testing.T, tools ...types.Tool) (*gohan.Stack, *gohantest.ScriptedModel) {
	t.Helper()
	model := gohantest.NewScriptedModel(types.ModelProfile{Name: "interactive", Caps: types.Caps{Tools: true}})
	preset := std.Interactive()
	specs := specsOf(tools)
	opts := append(preset.Options(),
		gohan.WithModels(model),
		gohan.WithLimits("interactive", types.InteractiveLimits),
		gohan.WithEgressClient(func(types.EgressPolicy) *http.Client { return &http.Client{} }),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:   gohan.FlowRequest{Name: "interactive"},
			Profile:   "interactive",
			Tools:     tools,
			ToolChain: preset.ToolChain,
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				tools := in.Tools
				if len(tools) == 0 {
					tools = specs
				}
				return types.ModelRequest{System: in.System, Messages: in.Input, Tools: tools}, nil
			},
		}),
	)
	stack, err := gohan.Build(opts...)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return stack, model
}

func specsOf(tools []types.Tool) []types.ToolSpec {
	specs := make([]types.ToolSpec, len(tools))
	for i, tool := range tools {
		specs[i] = tool.Spec()
	}
	return specs
}

func TestExplainHandler(t *testing.T) {
	t.Run("registered-flow-returns-explanation", func(t *testing.T) {
		stack, _ := explainHandlerStack(t)
		srv := httptest.NewServer(std.ExplainHandler(stack))
		t.Cleanup(srv.Close)

		resp, err := http.Get(srv.URL + "/explain?flow=interactive")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", ct)
		}
		var ex chains.Explanation
		if err := json.NewDecoder(resp.Body).Decode(&ex); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if ex.Flow != "interactive" {
			t.Fatalf("Flow = %q, want interactive", ex.Flow)
		}
		if ex.Profile != "interactive" {
			t.Fatalf("Profile = %q, want interactive", ex.Profile)
		}
		if len(ex.Steps) == 0 {
			t.Fatal("the explanation carries no chain steps")
		}
		if ex.Limits == (types.RunLimits{}) {
			t.Fatal("the explanation carries no limits")
		}
		if ex.Release == "" {
			t.Fatal("the explanation carries no release")
		}
	})

	t.Run("unknown-flow-is-refused", func(t *testing.T) {
		stack, _ := explainHandlerStack(t)
		srv := httptest.NewServer(std.ExplainHandler(stack))
		t.Cleanup(srv.Close)

		resp, err := http.Get(srv.URL + "/explain?flow=nope")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != types.ProblemOf(types.ErrInputInvalid).Status {
			t.Fatalf("status = %d, want %d", resp.StatusCode, types.ProblemOf(types.ErrInputInvalid).Status)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
			t.Fatalf("Content-Type = %q, want application/problem+json", ct)
		}
		var body struct {
			Type   string `json:"type"`
			Status int    `json:"status"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !strings.HasSuffix(body.Type, "gohan.input_invalid") {
			t.Fatalf("type = %q, want the gohan.input_invalid problem", body.Type)
		}
		if body.Status != resp.StatusCode {
			t.Fatalf("body status = %d, want %d", body.Status, resp.StatusCode)
		}
	})

	t.Run("missing-flow-is-refused", func(t *testing.T) {
		stack, _ := explainHandlerStack(t)
		srv := httptest.NewServer(std.ExplainHandler(stack))
		t.Cleanup(srv.Close)

		resp, err := http.Get(srv.URL + "/explain")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("a request without a flow name must be refused")
		}
	})

	t.Run("no-provider-call-is-made", func(t *testing.T) {
		stack, model := explainHandlerStack(t)
		srv := httptest.NewServer(std.ExplainHandler(stack))
		t.Cleanup(srv.Close)

		resp, err := http.Get(srv.URL + "/explain?flow=interactive")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		_ = resp.Body.Close()
		if calls := len(model.Requests()); calls != 0 {
			t.Fatalf("the model recorded %d calls, want 0", calls)
		}
	})

	t.Run("tool-with-verify-is-explained", func(t *testing.T) {
		verifyRan := false
		tool := explainVerifyTool{spec: types.ToolSpec{
			Name:   "refund_order",
			Schema: json.RawMessage(`{"type":"object"}`),
			Effect: types.SideEffect,
			Verify: func(context.Context, json.RawMessage, types.ToolResult) (types.Outcome, error) {
				verifyRan = true
				return types.Succeeded, nil
			},
		}}
		stack, _ := explainHandlerStack(t, tool)
		srv := httptest.NewServer(std.ExplainHandler(stack))
		t.Cleanup(srv.Close)

		resp, err := http.Get(srv.URL + "/explain?flow=interactive")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 200: body=%s", resp.StatusCode, b)
		}
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			t.Fatalf("decode: %v", err)
		}
		var sample types.ModelRequest
		if err := json.Unmarshal(raw["Sample"], &sample); err != nil {
			t.Fatalf("decode sample: %v", err)
		}
		if len(sample.Tools) != 1 || sample.Tools[0].Name != "refund_order" {
			t.Fatalf("explained tools = %+v, want refund_order", sample.Tools)
		}
		if string(sample.Tools[0].Schema) != `{"type":"object"}` {
			t.Fatalf("schema = %s, want the declared schema", sample.Tools[0].Schema)
		}
		if sample.Tools[0].Effect != types.SideEffect {
			t.Fatalf("effect = %v, want %v", sample.Tools[0].Effect, types.SideEffect)
		}
		if verifyRan {
			t.Fatal("explain ran the tool's verify function")
		}
	})

	t.Run("tool-with-egress-renders-policy", func(t *testing.T) {
		tool := explainVerifyTool{spec: types.ToolSpec{
			Name:     "fetch_order",
			Schema:   json.RawMessage(`{"type":"object"}`),
			Executor: types.ByHarness,
			Egress:   &types.EgressPolicy{Allow: []string{"api.example.com"}, Schemes: []string{"https"}},
		}}
		stack, _ := explainHandlerStack(t, tool)
		srv := httptest.NewServer(std.ExplainHandler(stack))
		t.Cleanup(srv.Close)

		resp, err := http.Get(srv.URL + "/explain?flow=interactive")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 200: body=%s", resp.StatusCode, b)
		}
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			t.Fatalf("decode: %v", err)
		}
		var infos []chains.ToolInfo
		if err := json.Unmarshal(raw["Tools"], &infos); err != nil {
			t.Fatalf("decode tools: %v", err)
		}
		if len(infos) != 1 || infos[0].Name != "fetch_order" {
			t.Fatalf("explained tools = %+v, want fetch_order", infos)
		}
		if infos[0].Egress == nil {
			t.Fatal("rendered egress = nil, want the declared policy")
		}
		if !reflect.DeepEqual(infos[0].Egress.Allow, []string{"api.example.com"}) {
			t.Errorf("rendered egress allow = %v, want [api.example.com]", infos[0].Egress.Allow)
		}
		if !reflect.DeepEqual(infos[0].Egress.Schemes, []string{"https"}) {
			t.Errorf("rendered egress schemes = %v, want [https]", infos[0].Egress.Schemes)
		}
	})
}
