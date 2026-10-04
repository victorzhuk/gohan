package gohan

import (
	"bytes"
	"context"
	"errors"
	"iter"
	"log/slog"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestBuildStrategies(t *testing.T) {
	t.Run("build.impossible-combination", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		_, err = s.ResolveStrategies(FlowRequest{
			Name:     "extract",
			Strategy: StructuredConstrained,
		}, map[string]types.ModelProfile{"small": profile("small", false)}, "small")
		if !errors.Is(err, ErrStrategyUnsupported) {
			t.Fatalf("err = %v, want ErrStrategyUnsupported", err)
		}
		for _, part := range []string{"extract", "small", "constrained"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("err %q misses %q", err, part)
			}
		}
	})

	t.Run("build.provider-tool-unsupported", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		_, err = s.ResolveStrategies(FlowRequest{
			Name:  "browse",
			Tools: []types.ToolSpec{{Name: "code_exec", Executor: types.ByProvider}},
		}, map[string]types.ModelProfile{"small": profile("small", true)}, "small")
		if !errors.Is(err, ErrProviderToolUnsupported) {
			t.Fatalf("err = %v, want ErrProviderToolUnsupported", err)
		}
		for _, part := range []string{"browse", "small", "code_exec"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("err %q misses %q", err, part)
			}
		}
	})

	t.Run("model.incompatible-fallback-rejected-at-build", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		fallback := profile("fallback", true)
		fallback.Caps.Tools = false
		_, err = s.ResolveStrategies(FlowRequest{
			Name:     "agent",
			Tools:    []types.ToolSpec{{Name: "search", Executor: types.ByHarness}},
			Fallback: "fallback",
		}, map[string]types.ModelProfile{
			"primary":  profile("primary", true),
			"fallback": fallback,
		}, "primary")
		if !errors.Is(err, ErrFallbackIncompatible) {
			t.Fatalf("err = %v, want ErrFallbackIncompatible", err)
		}
		for _, part := range []string{"agent", "fallback", "tools"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("err %q misses %q", err, part)
			}
		}
	})

	t.Run("resolution-precedence", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		profiles := map[string]types.ModelProfile{"p": profile("p", true)}

		plan, err := s.ResolveStrategies(FlowRequest{Name: "f", Strategy: StructuredToolSchema}, profiles, "p")
		if err != nil {
			t.Fatalf("explicit tool schema: %v", err)
		}
		if plan.Structured != StructuredToolSchema {
			t.Errorf("flow request lost to caps: %v", plan.Structured)
		}

		plan, err = s.ResolveStrategies(FlowRequest{Name: "f", Structured: true}, profiles, "p")
		if err != nil {
			t.Fatalf("auto on constrained cap: %v", err)
		}
		if plan.Structured != StructuredConstrained {
			t.Errorf("caps default = %v, want constrained", plan.Structured)
		}

		noCaps := profile("plain", false)
		plan, err = s.ResolveStrategies(FlowRequest{Name: "f", Structured: true}, map[string]types.ModelProfile{"plain": noCaps}, "plain")
		if err != nil {
			t.Fatalf("auto without caps: %v", err)
		}
		if plan.Structured != StructuredToolSchema {
			t.Errorf("caps default = %v, want tool schema", plan.Structured)
		}
	})

	t.Run("minimal-build", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if s.logger == nil {
			t.Error("nil default logger")
		}
	})

	t.Run("option-set", func(t *testing.T) {
		var buf bytes.Buffer
		log := slog.New(slog.NewTextHandler(&buf, nil))
		model := fakeModel{profile: profile("m", true)}
		s, err := Build(
			WithModels(model),
			WithProviderKeys(fakeKeySource{}),
			WithModelMiddleware(func(next types.ModelFunc) types.ModelFunc {
				return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
					return next(ctx, req)
				}
			}),
			WithEstimator(fakeEstimator{}),
			WithLogger(log),
			WithPinnedManifest(types.PinnedManifest{Version: 1}),
			MaxParallelTools(3),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if len(s.models) != 1 || s.models[0].Profile().Name != "m" {
			t.Errorf("models = %v, want one %q", s.models, "m")
		}
		if s.keys == nil {
			t.Error("key source not applied")
		}
		if s.estimator == nil {
			t.Error("estimator not applied")
		}
		if len(s.middleware) != 1 {
			t.Errorf("middleware count = %d, want 1", len(s.middleware))
		}
		if s.pinned == nil || s.pinned.Version != 1 {
			t.Errorf("pinned = %v, want version 1", s.pinned)
		}
		if s.maxParallelTools != 3 {
			t.Errorf("maxParallelTools = %d, want 3", s.maxParallelTools)
		}
		if s.logger != log {
			t.Error("logger not applied")
		}

		seq, err := Build(SequentialTools(), MaxParallelTools(4))
		if err != nil {
			t.Fatalf("sequential build: %v", err)
		}
		profiles := map[string]types.ModelProfile{"p": profile("p", true)}
		plan, err := seq.ResolveStrategies(FlowRequest{Name: "f"}, profiles, "p")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if plan.ParallelTools {
			t.Error("SequentialTools did not disable parallel tools")
		}
		if plan.MaxParallelTools != 1 {
			t.Errorf("sequential max = %d, want 1", plan.MaxParallelTools)
		}

		if _, err := Build(MaxParallelTools(0)); !errors.Is(err, ErrNotPositive) {
			t.Errorf("MaxParallelTools(0) err = %v, want ErrNotPositive", err)
		}
		if _, err := Build(WithLogger(nil)); err == nil {
			t.Error("nil logger accepted")
		}
	})
}

func profile(name string, constrained bool) types.ModelProfile {
	return types.ModelProfile{
		Name: name,
		Caps: types.Caps{
			Tools:         true,
			ParallelTools: true,
			Constrained:   constrained,
			ProviderTools: map[string]types.ProviderToolCap{},
		},
	}
}

type fakeModel struct{ profile types.ModelProfile }

func (m fakeModel) Profile() types.ModelProfile { return m.profile }

func (m fakeModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {}
}

type fakeKeySource struct{}

func (fakeKeySource) ProviderKey(ctx context.Context, profile, tenant string) (types.ProviderCredential, error) {
	return types.ProviderCredential{}, nil
}

type fakeEstimator struct{}

func (fakeEstimator) Estimate(req types.ModelRequest, caps types.Caps) int { return 0 }
