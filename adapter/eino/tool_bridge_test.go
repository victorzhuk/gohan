package eino

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

// bridgeSentinel is the error identity the imported tool wraps.
var bridgeSentinel = errors.New("bridge: sentinel failure")

// countingTool is an imported eino InvokableTool that counts Info calls
// and invocations and reports a scripted reply or error.
type countingTool struct {
	infoErr   error
	info      *schema.ToolInfo
	reply     string
	runErr    error
	infoCalls int
	runCalls  int
	lastCtx   context.Context
}

func (t *countingTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	t.infoCalls++
	if t.infoErr != nil {
		return nil, t.infoErr
	}
	if t.info != nil {
		return t.info, nil
	}
	return &schema.ToolInfo{Name: "echo", Desc: "echoes its input"}, nil
}

func (t *countingTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	t.runCalls++
	t.lastCtx = ctx
	if t.runErr != nil {
		return "", t.runErr
	}
	return t.reply, nil
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

	t.Run("info_validated_before_execution", func(t *testing.T) {
		t.Run("info_error", func(t *testing.T) {
			tl := &countingTool{infoErr: errors.New("registry offline")}
			if _, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"}); err == nil {
				t.Fatal("construction passed an info error")
			}
			if tl.runCalls != 0 {
				t.Fatalf("invoked %d times during a failed construction", tl.runCalls)
			}
		})
		t.Run("nil_info", func(t *testing.T) {
			tl := &countingTool{info: nil}
			tl.info = nil
			tl.infoErr = nil
			if _, err := NewForeignTool(ctx, &nilInfoTool{}, types.ToolSpec{Name: "echo"}); err == nil {
				t.Fatal("construction passed a nil tool info")
			}
		})
		t.Run("empty_name", func(t *testing.T) {
			if _, err := NewForeignTool(ctx, &countingTool{info: &schema.ToolInfo{}}, types.ToolSpec{Name: "echo"}); err == nil {
				t.Fatal("construction passed an empty upstream name")
			}
		})
		t.Run("name_mismatch", func(t *testing.T) {
			tl := &countingTool{info: &schema.ToolInfo{Name: "other"}}
			if _, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"}); err == nil {
				t.Fatal("construction passed a mis-declared name")
			}
		})
		t.Run("info_called_once", func(t *testing.T) {
			tl := &countingTool{}
			if _, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"}); err != nil {
				t.Fatalf("construct: %v", err)
			}
			if tl.infoCalls != 1 {
				t.Fatalf("Info called %d times, want exactly once", tl.infoCalls)
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
		for _, reply := range []string{
			`{"text":"1"}`,
			`{"ID":"call-1","Content":[{"Kind":"text","Block":{"Text":"parsed"}}],"Outcome":1}`,
			`plain text`,
			`[1,2,3]`,
		} {
			tl := &countingTool{reply: reply}
			got, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"})
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			res, err := got.Call(ctx, json.RawMessage(`{"in":"1"}`))
			if err != nil {
				t.Fatalf("call on %q: %v", reply, err)
			}
			if len(res.Content) != 1 {
				t.Fatalf("reply %q produced %d blocks, want exactly one", reply, len(res.Content))
			}
			txt, ok := res.Content[0].(types.Text)
			if !ok {
				t.Fatalf("reply %q produced %T, want types.Text", reply, res.Content[0])
			}
			if txt.Text != reply {
				t.Fatalf("text = %q, want the verbatim reply %q", txt.Text, reply)
			}
		}
	})

	t.Run("runtime.foreign-tool-envelope-parsed", func(t *testing.T) {
		envelope := `{"gohan_tool_result":{"ID":"call-1","Content":[{"Kind":"text","Block":{"Text":"structured"}}],"Outcome":1}}`
		tl := &countingTool{reply: envelope}
		got, err := NewForeignToolWithEnvelope(ctx, tl, types.ToolSpec{Name: "echo"})
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		res, err := got.Call(ctx, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if res.Outcome != types.Failed || len(res.Content) != 1 {
			t.Fatalf("envelope parsed to outcome %v with %d blocks, want Failed with 1", res.Outcome, len(res.Content))
		}

		for _, reply := range []string{
			`{"gohan_tool_result":{}}`,
			`{"gohan_tool_result":null}`,
			`{"gohan_tool_result":"not an object"}`,
			`{"gohan_tool_result":123}`,
			`{"other":"value","gohan_tool_result":{"outcome":1}}`,
			`{"other":"value"}`,
			`plain text`,
		} {
			tl := &countingTool{reply: reply}
			got, err := NewForeignToolWithEnvelope(ctx, tl, types.ToolSpec{Name: "echo"})
			if err != nil {
				t.Fatalf("construct: %v", err)
			}
			res, err := got.Call(ctx, json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("call on %q failed: %v", reply, err)
			}
			if len(res.Content) != 1 {
				t.Fatalf("reply %q produced %d blocks, want one Text block", reply, len(res.Content))
			}
			txt, ok := res.Content[0].(types.Text)
			if !ok || txt.Text != reply {
				t.Fatalf("reply %q stayed as %T %q, want one verbatim Text block", reply, res.Content[0], txt.Text)
			}
		}
	})

	t.Run("runtime.foreign-tool-error-identity", func(t *testing.T) {
		tl := &countingTool{runErr: fmt.Errorf("wrapped: %w", bridgeSentinel)}
		got, err := NewForeignTool(ctx, tl, types.ToolSpec{Name: "echo"})
		if err != nil {
			t.Fatalf("construct: %v", err)
		}
		_, err = got.Call(ctx, json.RawMessage(`{}`))
		if !errors.Is(err, bridgeSentinel) {
			t.Fatalf("errors.Is(sentinel) = false for %v", err)
		}
	})

	t.Run("middleware_order_preserved", func(t *testing.T) {
		tl := &countingTool{reply: `{"text":"1"}`}
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
		ctxKey := &struct{ name string }{"call-ctx"}
		callCtx := context.WithValue(ctx, ctxKey, "kept")
		fn := chains.ComposeToolChain(chain, func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			order = append(order, "tool")
			return got.Call(ctx, json.RawMessage(`{"in":"1"}`))
		})
		if _, err := fn(callCtx, types.ToolUse{ID: "c1", Name: "echo"}); err != nil {
			t.Fatalf("chain run: %v", err)
		}
		if reflect.DeepEqual(order, []string{"outer", "inner", "tool"}) == false {
			t.Fatalf("step order = %v, want [outer inner tool]", order)
		}
		if tl.lastCtx.Value(ctxKey) != "kept" {
			t.Fatal("the caller ctx did not reach InvokableRun unchanged")
		}
	})
}

// nilInfoTool reports a nil tool info without an error.
type nilInfoTool struct{ countingTool }

func (*nilInfoTool) Info(context.Context) (*schema.ToolInfo, error) { return nil, nil }
