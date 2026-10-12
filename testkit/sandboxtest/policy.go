package sandboxtest

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
	"github.com/victorzhuk/gohan/std/sandbox"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// explainHarnessTool is the harness-side tool the rendered-policy case
// explains beside the sandbox tool set.
type explainHarnessTool struct{ spec types.ToolSpec }

func (t explainHarnessTool) Spec() types.ToolSpec { return t.spec }

func (t explainHarnessTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	return types.ToolResult{}, nil
}

// The compile-time half of sandbox.egress-shares-policy-type: one
// egress vocabulary, no second struct.
var (
	_ types.EgressPolicy  = types.SandboxPolicy{}.Egress
	_ *types.EgressPolicy = types.ToolSpec{}.Egress
)

// egressSharesPolicyType proves sandbox.egress-shares-policy-type: the
// shared type at compile time, the rendered policy shape through
// Stack.Explain for both a sandbox tool set and a harness tool, and the
// private-range denial at the same seam.
func egressSharesPolicyType(t *testing.T, f Factory) {
	t.Helper()
	t.Run("shared-type", func(t *testing.T) {
		p := types.SandboxPolicy{Lifecycle: types.PerCall}
		p.Egress.Allow = []string{"api.example.com"}
		ts, err := sandbox.New(f.Sandbox(t), p)
		if err != nil {
			t.Fatalf("build sandbox tool set: %v", err)
		}
		for _, tool := range ts.Tools() {
			if tool.Spec().Egress == nil {
				t.Fatalf("tool %s carries no egress policy, want the shared types.EgressPolicy", tool.Spec().Name)
			}
			if got := tool.Spec().Egress.Allow; len(got) != 1 || got[0] != "api.example.com" {
				t.Fatalf("tool %s egress allow = %v, want [api.example.com]", tool.Spec().Name, got)
			}
		}
	})
	t.Run("private-range-denied", func(t *testing.T) {
		fx := networkFixture(t, f)
		p := types.SandboxPolicy{Lifecycle: types.PerCall}
		p.Egress.Allow = []string{"api.example.com"}
		p.Egress.PrivateRanges = types.DenyPrivate
		w := open(t, f.Sandbox(t), p)
		rec := f.Recorder()
		rec.Reset()
		before := rec.Denied()
		res := exec(t, w, f.Probe().Dial(fx.PrivateHost, fx.PrivatePort))
		if res.ExitCode == 0 {
			t.Fatal("dial into a private range succeeded, want denial")
		}
		denyObserved(t, rec, before)
	})
	t.Run("rendered-policy", func(t *testing.T) {
		p := types.SandboxPolicy{Lifecycle: types.PerCall}
		p.Egress.Allow = []string{"api.example.com"}
		p.Egress.Schemes = []string{"https"}
		ts, err := sandbox.New(f.Sandbox(t), p)
		if err != nil {
			t.Fatalf("build sandbox tool set: %v", err)
		}
		harness := explainHarnessTool{spec: types.ToolSpec{
			Name:   "web_fetch",
			Egress: &types.EgressPolicy{Allow: []string{"api.example.com"}, Schemes: []string{"https"}},
		}}
		tools := append(ts.Tools(), harness)
		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "primary", Caps: types.Caps{Tools: true}})
		stack, err := gohan.Build(append(std.Defaults(),
			gohan.WithModels(model),
			gohan.WithLimits("flow", types.InteractiveLimits),
			gohan.WithEgressClient(func(types.EgressPolicy) *http.Client { return &http.Client{} }),
			gohan.WithNativeAgent(gohan.NativeSpec{
				Request: gohan.FlowRequest{Name: "flow"},
				Profile: "primary",
				Tools:   tools,
			}),
		)...)
		if err != nil {
			t.Fatalf("build stack: %v", err)
		}
		ex := stack.Explain("flow")
		if len(ex.Tools) != len(tools) {
			t.Fatalf("explained tools = %d, want %d", len(ex.Tools), len(tools))
		}
		for _, info := range ex.Tools {
			if info.Egress == nil {
				t.Fatalf("tool %s renders no egress policy, want the declared types.EgressPolicy", info.Name)
			}
			if !reflect.DeepEqual(info.Egress.Allow, []string{"api.example.com"}) {
				t.Errorf("tool %s rendered egress allow = %v, want [api.example.com]", info.Name, info.Egress.Allow)
			}
			if !reflect.DeepEqual(info.Egress.Schemes, []string{"https"}) {
				t.Errorf("tool %s rendered egress schemes = %v, want [https]", info.Name, info.Egress.Schemes)
			}
		}
		if !reflect.DeepEqual(ex.Tools[0].Egress, ex.Tools[len(ex.Tools)-1].Egress) {
			t.Errorf("sandbox tool set and harness tool render different shapes: %+v vs %+v",
				ex.Tools[0].Egress, ex.Tools[len(ex.Tools)-1].Egress)
		}
	})
}
