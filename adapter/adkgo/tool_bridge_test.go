package adkgo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool/toolconfirmation"
	"google.golang.org/genai"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

// errBridgeSentinel is the error identity the imported tool wraps.
var errBridgeSentinel = errors.New("bridge: sentinel failure")

// countingTool is an imported adk tool that counts Run calls and reports a
// scripted reply map or error.
type countingTool struct {
	name     string
	reply    map[string]any
	runErr   error
	runCalls int
	lastCtx  context.Context
}

func (t *countingTool) Name() string {
	if t.name == "" {
		return "echo"
	}
	return t.name
}

func (*countingTool) Description() string { return "echoes its input" }
func (*countingTool) IsLongRunning() bool { return false }

func (t *countingTool) Run(ctx agent.Context, _ any) (map[string]any, error) {
	t.runCalls++
	t.lastCtx = ctx
	if t.runErr != nil {
		return nil, t.runErr
	}
	if t.reply != nil {
		return t.reply, nil
	}
	return map[string]any{"text": "1"}, nil
}

// plainTool satisfies only adk's tool.Tool port; its Run signature does not
// match the runnable shape, so the bridge must refuse it.
type plainTool struct{ countingTool }

func (*plainTool) Run(context.Context, any) (map[string]any, error) {
	return nil, errors.New("plainTool must never run")
}

// mockCtx is a plain context.Context carrying the agent.Context shape adk
// tools demand, so chain middleware can hand it through untouched.
type mockCtx struct {
	*agent.StrictContextMock
}

// runCtx returns a context.Context value that also satisfies agent.Context.
func runCtx() context.Context {
	return &mockCtx{StrictContextMock: &agent.StrictContextMock{Ctx: context.Background()}}
}

func declaredSpec() types.ToolSpec {
	return types.ToolSpec{
		Name:           "echo",
		Description:    "declared echo",
		Schema:         json.RawMessage(`{"type":"object"}`),
		Effect:         types.Idempotent,
		RequiredScopes: []string{"tool:echo"},
		Timeout:        3 * time.Second,
		ReadBack:       "tail",
		MaxOutput:      128,
		Network:        true,
		Risk:           types.RiskMedium,
		Egress:         &types.EgressPolicy{Allow: []string{"example.test"}},
	}
}

func TestForeignToolTrustResolution(t *testing.T) {
	ctx := context.Background()

	t.Run("zero-trust-resolves-untrusted", func(t *testing.T) {
		spec := declaredSpec()
		spec.Trust = 0
		got, err := NewForeignTool(ctx, &countingTool{}, spec)
		if err != nil {
			t.Fatalf("NewForeignTool: %v", err)
		}
		if got.Spec().Trust != types.Untrusted {
			t.Fatalf("zero trust: got %v, want types.Untrusted", got.Spec().Trust)
		}
	})

	t.Run("declared-untrusted-stays", func(t *testing.T) {
		spec := declaredSpec()
		spec.Trust = types.Untrusted
		got, err := NewForeignTool(ctx, &countingTool{}, spec)
		if err != nil {
			t.Fatalf("NewForeignTool: %v", err)
		}
		if got.Spec().Trust != types.Untrusted {
			t.Fatalf("declared Untrusted changed to %v", got.Spec().Trust)
		}
	})
}

func TestForeignToolBridge(t *testing.T) {
	ctx := context.Background()

	t.Run("runtime.foreign-tool-spec-declared", func(t *testing.T) {
		tl := &countingTool{}
		spec := declaredSpec()
		got, err := NewForeignTool(ctx, tl, spec)
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		spec.Trust = types.Untrusted
		if !reflect.DeepEqual(got.Spec(), spec) {
			t.Fatalf("spec = %+v, want the declared %+v", got.Spec(), spec)
		}
		s := got.Spec()
		if s.Effect != types.Idempotent || s.Risk != types.RiskMedium {
			t.Fatalf("governance fields drifted: effect %v risk %v", s.Effect, s.Risk)
		}
		if len(s.RequiredScopes) != 1 || s.RequiredScopes[0] != "tool:echo" || s.Timeout != 3*time.Second {
			t.Fatalf("scopes/timeout drifted: %+v", s)
		}
		if !reflect.DeepEqual(s.Egress, spec.Egress) {
			t.Fatalf("egress = %+v, want %+v", s.Egress, spec.Egress)
		}
	})

	t.Run("construction_refusals", func(t *testing.T) {
		t.Run("nil_import", func(t *testing.T) {
			if _, err := NewForeignTool(ctx, nil, types.ToolSpec{Name: "echo"}); err == nil {
				t.Fatal("construction passed a nil imported tool")
			}
		})
		t.Run("empty_declared_name", func(t *testing.T) {
			if _, err := NewForeignTool(ctx, &countingTool{}, types.ToolSpec{}); err == nil {
				t.Fatal("construction passed an empty declared name")
			}
		})
		t.Run("name_mismatch", func(t *testing.T) {
			tl := &countingTool{name: "other"}
			if _, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"}); err == nil {
				t.Fatal("construction passed a mis-declared name")
			}
		})
		t.Run("not_runnable", func(t *testing.T) {
			if _, err := NewForeignTool(ctx, &plainTool{}, types.ToolSpec{Name: "echo"}); err == nil {
				t.Fatal("construction passed a tool without Run")
			}
		})
		t.Run("no_run_at_construction", func(t *testing.T) {
			tl := &countingTool{}
			if _, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"}); err != nil {
				t.Fatalf("construct: %v", err)
			}
			if tl.runCalls != 0 {
				t.Fatalf("invoked %d times at construction", tl.runCalls)
			}
		})
	})

	t.Run("runtime.foreign-tool-trust-untrusted", func(t *testing.T) {
		tl := &countingTool{}
		got, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"})
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		if got.Spec().Trust != types.Untrusted {
			t.Fatalf("zero trust resolved to %v, want Untrusted", got.Spec().Trust)
		}
		got, err = NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo", Trust: types.Untrusted})
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		if got.Spec().Trust != types.Untrusted {
			t.Fatalf("explicit Untrusted changed to %v", got.Spec().Trust)
		}
	})

	t.Run("runtime.foreign-tool-result-mapped", func(t *testing.T) {
		replies := []map[string]any{
			{"text": "1"},
			{"nested": map[string]any{"k": "v"}},
			{},
		}
		for _, reply := range replies {
			tl := &countingTool{reply: reply}
			got, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"})
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			res, err := got.Call(runCtx(), json.RawMessage(`{"in":"1"}`))
			if err != nil {
				t.Fatalf("call on %v: %v", reply, err)
			}
			if len(res.Content) != 1 {
				t.Fatalf("reply %v produced %d blocks, want exactly one", reply, len(res.Content))
			}
			txt, ok := res.Content[0].(types.Text)
			if !ok {
				t.Fatalf("reply %v produced %T, want types.Text", reply, res.Content[0])
			}
			want, err := json.Marshal(reply)
			if err != nil {
				t.Fatalf("marshal reply: %v", err)
			}
			if txt.Text != string(want) {
				t.Fatalf("text = %q, want the verbatim reply %q", txt.Text, want)
			}
		}
	})

	t.Run("runtime.foreign-tool-envelope-parsed", func(t *testing.T) {
		envelope := map[string]any{
			"gohan_tool_result": map[string]any{
				"ID":      "call-1",
				"Content": []any{map[string]any{"Kind": "text", "Block": map[string]any{"Text": "structured"}}},
				"Outcome": float64(1),
			},
		}
		tl := &countingTool{reply: envelope}
		got, err := NewForeignToolWithEnvelope(ctx, tl, types.ToolSpec{Name: "echo"})
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		res, err := got.Call(runCtx(), json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if res.Outcome != types.Failed || len(res.Content) != 1 || res.ID != "call-1" {
			t.Fatalf("envelope parsed to outcome %v with %d blocks id %q, want Failed with 1 block", res.Outcome, len(res.Content), res.ID)
		}

		for _, reply := range []map[string]any{
			{"gohan_tool_result": nil},
			{"gohan_tool_result": "not an object"},
			{"gohan_tool_result": 123},
			{"gohan_tool_result": map[string]any{}},
			{"other": "value", "gohan_tool_result": map[string]any{"outcome": float64(1)}},
			{"other": "value"},
			{},
		} {
			tl := &countingTool{reply: reply}
			got, err := NewForeignToolWithEnvelope(ctx, tl, types.ToolSpec{Name: "echo"})
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			res, err := got.Call(runCtx(), json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("call on %v failed: %v", reply, err)
			}
			if len(res.Content) != 1 {
				t.Fatalf("reply %v produced %d blocks, want one Text block", reply, len(res.Content))
			}
			txt, ok := res.Content[0].(types.Text)
			if !ok {
				t.Fatalf("reply %v stayed as %T, want one verbatim Text block", reply, res.Content[0])
			}
			want, err := json.Marshal(reply)
			if err != nil {
				t.Fatalf("marshal reply: %v", err)
			}
			if txt.Text != string(want) {
				t.Fatalf("text = %q, want the verbatim reply %q", txt.Text, want)
			}
		}
	})

	t.Run("runtime.foreign-tool-error-identity", func(t *testing.T) {
		tl := &countingTool{runErr: fmt.Errorf("wrapped: %w", errBridgeSentinel)}
		got, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"})
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		_, err = got.Call(runCtx(), json.RawMessage(`{}`))
		if !errors.Is(err, errBridgeSentinel) {
			t.Fatalf("errors.Is(sentinel) = false for %v", err)
		}
	})

	t.Run("caller_ctx_unchanged", func(t *testing.T) {
		tl := &countingTool{}
		got, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"})
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		ctxKey := &struct{ name string }{"call-ctx"}
		m := agent.NewStrictContextMock(context.WithValue(ctx, ctxKey, "kept"))
		if _, err := got.Call(&m, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("call: %v", err)
		}
		if tl.lastCtx.Value(ctxKey) != "kept" {
			t.Fatal("the caller ctx did not reach Run unchanged")
		}
	})

	t.Run("plain_context_refused_at_seam", func(t *testing.T) {
		tl := &countingTool{}
		got, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"})
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		if _, err := got.Call(ctx, json.RawMessage(`{}`)); err == nil {
			t.Fatal("Call executed on a plain context.Context; only an adk invocation context can reach Run")
		}
		if tl.runCalls != 0 {
			t.Fatalf("invoked %d times behind a refused ctx", tl.runCalls)
		}
	})

	t.Run("middleware_order_preserved", func(t *testing.T) {
		tl := &countingTool{}
		got, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"})
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		var order []string
		step := func(name string) types.ToolMiddleware {
			return func(next types.ToolFunc) types.ToolFunc {
				return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
					order = append(order, name)
					return next(ctx, call)
				}
			}
		}
		chain := chains.ToolChain{
			{Name: "outer", Kind: chains.KindGate, Use: step("outer")},
			{Name: "inner", Kind: chains.KindJournal, Use: step("inner")},
		}
		m := agent.NewStrictContextMock(ctx)
		callCtx := &mockCtx{StrictContextMock: &m}
		fn := chains.ComposeToolChain(chain, func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			order = append(order, "tool")
			return got.Call(ctx, json.RawMessage(`{"in":"1"}`))
		})
		if _, err := fn(callCtx, types.ToolUse{ID: "c1", Name: "echo"}); err != nil {
			t.Fatalf("chain run: %v", err)
		}
		if !reflect.DeepEqual(order, []string{"outer", "inner", "tool"}) {
			t.Fatalf("step order = %v, want [outer inner tool]", order)
		}
	})
}

// adkCallCtx is a mock agent.Context whose confirmation probe reports none,
// so an adk function tool runs straight to its handler.
type adkCallCtx struct{ mockCtx }

func (*adkCallCtx) ToolConfirmation() *toolconfirmation.ToolConfirmation { return nil }

// governedFake is a governed types.Tool recording each Call.
type governedFake struct {
	spec types.ToolSpec
	ctx  context.Context
	args json.RawMessage
	res  types.ToolResult
	err  error
}

func (f *governedFake) Spec() types.ToolSpec { return f.spec }

func (f *governedFake) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	f.ctx, f.args = ctx, args
	return f.res, f.err
}

func TestForeignToolAdkTool(t *testing.T) {
	spec := declaredSpec()
	spec.Schema = json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)

	t.Run("declaration_matches_spec", func(t *testing.T) {
		gov := &governedFake{spec: spec}
		got, err := NewAdkTool(gov)
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		if got.Name() != spec.Name {
			t.Fatalf("name = %q, want %q", got.Name(), spec.Name)
		}
		if got.Description() != spec.Description {
			t.Fatalf("description = %q, want %q", got.Description(), spec.Description)
		}
		declarer, ok := got.(interface {
			Declaration() *genai.FunctionDeclaration
		})
		if !ok {
			t.Fatalf("built tool %T does not expose Declaration", got)
		}
		var declared jsonschema.Schema
		if err := json.Unmarshal(spec.Schema, &declared); err != nil {
			t.Fatalf("decode declared schema: %v", err)
		}
		want, err := json.Marshal(&declared)
		if err != nil {
			t.Fatalf("encode declared schema: %v", err)
		}
		gotRaw, err := json.Marshal(declarer.Declaration().ParametersJsonSchema)
		if err != nil {
			t.Fatalf("encode declaration schema: %v", err)
		}
		var round jsonschema.Schema
		if err := json.Unmarshal(gotRaw, &round); err != nil {
			t.Fatalf("decode declaration schema: %v", err)
		}
		gotJSON, err := json.Marshal(&round)
		if err != nil {
			t.Fatalf("encode declaration schema: %v", err)
		}
		if !bytes.Equal(gotJSON, want) {
			t.Fatalf("parameters schema = %s, want %s", gotJSON, want)
		}
	})

	t.Run("construction_refusals", func(t *testing.T) {
		t.Run("nil_governed", func(t *testing.T) {
			if _, err := NewAdkTool(nil); err == nil {
				t.Fatal("construction passed a nil governed tool")
			}
		})
		t.Run("empty_name", func(t *testing.T) {
			if _, err := NewAdkTool(&governedFake{spec: types.ToolSpec{Schema: json.RawMessage(`{"type":"object"}`)}}); err == nil {
				t.Fatal("construction passed an empty declared name")
			}
		})
		t.Run("empty_schema", func(t *testing.T) {
			if _, err := NewAdkTool(&governedFake{spec: types.ToolSpec{Name: "echo"}}); err == nil {
				t.Fatal("construction passed an empty schema")
			}
		})
		t.Run("unusable_schema", func(t *testing.T) {
			s := spec
			s.Schema = json.RawMessage(`{invalid`)
			if _, err := NewAdkTool(&governedFake{spec: s}); err == nil {
				t.Fatal("construction passed an unusable schema")
			}
		})
	})

	t.Run("call_carries_governed_result", func(t *testing.T) {
		gov := &governedFake{
			spec: spec,
			res:  types.ToolResult{Content: []types.Block{types.Text{Text: "hello"}}},
		}
		got, err := NewAdkTool(gov)
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		run, ok := got.(runnable)
		if !ok {
			t.Fatalf("built tool %T does not expose Run", got)
		}
		out, err := run.Run(&adkCallCtx{mockCtx{&agent.StrictContextMock{Ctx: context.Background()}}}, map[string]any{"text": "hi"})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if string(gov.args) != `{"text":"hi"}` {
			t.Fatalf("governed args = %s, want marshalled call args", gov.args)
		}
		enc, ok := out[foreignToolResultKey]
		if !ok {
			t.Fatalf("result map %v lacks the %s envelope", out, foreignToolResultKey)
		}
		raw, err := json.Marshal(enc)
		if err != nil {
			t.Fatalf("encode envelope: %v", err)
		}
		var res types.ToolResult
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		if len(res.Content) != 1 {
			t.Fatalf("envelope content = %+v, want one block", res.Content)
		}
		if text, isText := res.Content[0].(types.Text); !isText || text.Text != "hello" {
			t.Fatalf("envelope block = %#v, want Text hello", res.Content[0])
		}
		if gov.ctx == nil {
			t.Fatal("governed Call received no context")
		}
	})

	t.Run("sentinel_error_wraps", func(t *testing.T) {
		gov := &governedFake{spec: spec, err: errBridgeSentinel}
		got, err := NewAdkTool(gov)
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		run, ok := got.(runnable)
		if !ok {
			t.Fatalf("built tool %T does not expose Run", got)
		}
		_, err = run.Run(&adkCallCtx{mockCtx{&agent.StrictContextMock{Ctx: context.Background()}}}, map[string]any{"text": "hi"})
		if err == nil || !errors.Is(err, errBridgeSentinel) {
			t.Fatalf("err = %v, want errors.Is errBridgeSentinel", err)
		}
	})

	t.Run("plain_context_refused", func(t *testing.T) {
		foreign, err := NewForeignTool(context.Background(), &countingTool{}, types.ToolSpec{Name: "echo"})
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		_, err = foreign.Call(context.Background(), json.RawMessage("null"))
		if !errors.Is(err, ErrAdkContextRequired) {
			t.Fatalf("err = %v, want errors.Is ErrAdkContextRequired", err)
		}
	})
}
