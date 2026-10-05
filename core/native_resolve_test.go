package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

func nativeSpec(name, profile string) NativeSpec {
	return NativeSpec{
		Request: FlowRequest{Name: name, Structured: true},
		Profile: profile,
	}
}

func TestResolveNative(t *testing.T) {
	t.Run("valid-registration-resolves", func(t *testing.T) {
		mw := func(next types.ModelFunc) types.ModelFunc {
			return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
				return next(ctx, req)
			}
		}
		tool := &fakeNativeTool{spec: types.ToolSpec{Name: "search", Executor: types.ByHarness}}
		spec := nativeSpec("agent", "primary")
		spec.Tools = []types.Tool{tool}
		s, err := Build(
			WithModels(fakeModel{profile: profile("primary", true)}),
			WithModelMiddleware(mw),
			WithLimits("agent", types.InteractiveLimits),
			WithNativeAgent(spec),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		cfg, ok := s.resolvedNative("agent")
		if !ok {
			t.Fatal("flow not resolved")
		}
		if cfg.model.Profile().Name != "primary" {
			t.Errorf("model profile = %q, want primary", cfg.model.Profile().Name)
		}
		if cfg.profile.Name != "primary" {
			t.Errorf("profile snapshot = %q, want primary", cfg.profile.Name)
		}
		if cfg.plan.Structured != StructuredConstrained {
			t.Errorf("plan = %v, want constrained", cfg.plan.Structured)
		}
		if !reflect.DeepEqual(cfg.request.Tools, []types.ToolSpec{tool.Spec()}) {
			t.Errorf("request tools = %v, want derived from spec", cfg.request.Tools)
		}
		if len(cfg.modelChain) != 1 {
			t.Fatalf("model chain = %d steps, want 1", len(cfg.modelChain))
		}
		step := cfg.modelChain[0]
		if step.Kind != chains.KindUser || step.Name != "model-middleware-1" {
			t.Errorf("step = %s/%d, want model-middleware-1/KindUser", step.Name, step.Kind)
		}
		if reflect.ValueOf(step.Use).Pointer() != reflect.ValueOf(mw).Pointer() {
			t.Error("resolved chain step is not the configured middleware")
		}
		if cfg.limits.MaxTurns <= 0 {
			t.Errorf("limits = %+v, want resolved preset", cfg.limits)
		}
	})

	t.Run("constrained-without-cap-rejected", func(t *testing.T) {
		spec := nativeSpec("agent", "plain")
		spec.Request.Strategy = StructuredConstrained
		_, err := Build(
			WithModels(fakeModel{profile: profile("plain", false)}),
			WithNativeAgent(spec),
		)
		if !errors.Is(err, ErrStrategyUnsupported) {
			t.Fatalf("err = %v, want ErrStrategyUnsupported", err)
		}
	})

	t.Run("unknown-profile-rejected", func(t *testing.T) {
		_, err := Build(WithNativeAgent(nativeSpec("agent", "ghost")))
		if err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("err = %v, want unknown profile", err)
		}
	})

	t.Run("unknown-fallback-rejected", func(t *testing.T) {
		spec := nativeSpec("agent", "primary")
		spec.Request.Fallback = "ghost"
		_, err := Build(
			WithModels(fakeModel{profile: profile("primary", true)}),
			WithNativeAgent(spec),
		)
		if !errors.Is(err, ErrFallbackUnknown) {
			t.Fatalf("err = %v, want ErrFallbackUnknown", err)
		}
	})

	t.Run("rejected-definition-calls-no-provider", func(t *testing.T) {
		model := &countingModel{profile: profile("plain", false)}
		spec := nativeSpec("agent", "plain")
		spec.Request.Strategy = StructuredConstrained
		_, err := Build(
			WithModels(model),
			WithNativeAgent(spec),
		)
		if err == nil {
			t.Fatal("build accepted an invalid definition")
		}
		if got := model.calls.Load(); got != 0 {
			t.Errorf("provider calls = %d, want 0", got)
		}
	})
}

type countingModel struct {
	profile types.ModelProfile
	calls   atomic.Int32
}

func (m *countingModel) Profile() types.ModelProfile { return m.profile }

func (m *countingModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.calls.Add(1)
	return func(yield func(types.ModelChunk, error) bool) {}
}

type fakeNativeTool struct{ spec types.ToolSpec }

func (t *fakeNativeTool) Spec() types.ToolSpec { return t.spec }

func (t *fakeNativeTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	return types.ToolResult{}, nil
}
