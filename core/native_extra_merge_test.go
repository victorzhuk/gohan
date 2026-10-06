package gohan

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestNativeExtraMerge(t *testing.T) {
	staticTemp := 0.7
	build := func(spec NativeSpec) *Stack {
		t.Helper()
		s, err := Build(
			WithModels(fakeModel{profile: profile("primary", true)}),
			WithNativeAgent(spec),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return s
	}
	assembled := func(s *Stack) types.ModelOptions {
		t.Helper()
		cfg, ok := s.resolvedNative("agent")
		if !ok {
			t.Fatal("flow not resolved")
		}
		req, err := s.nativeTurnConfig(cfg).assemble(context.Background(), types.AssembleInput{})
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		return req.Options
	}

	t.Run("static-fills-unset", func(t *testing.T) {
		spec := nativeSpec("agent", "primary")
		spec.ModelOptions = types.ModelOptions{
			MaxTokens:   512,
			Temperature: &staticTemp,
			ToolChoice:  types.ToolChoiceAuto,
			Extra:       map[string]any{"top_p": 0.9, "user_tag": "flow"},
		}
		spec.Assemble = func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{}, nil
		}
		opts := assembled(build(spec))
		if opts.MaxTokens != 512 || opts.Temperature != &staticTemp || opts.ToolChoice != types.ToolChoiceAuto {
			t.Fatalf("options = %+v, want static fields merged", opts)
		}
		if opts.Extra["top_p"] != 0.9 || opts.Extra["user_tag"] != "flow" {
			t.Fatalf("extra = %v, want static keys merged", opts.Extra)
		}
	})

	t.Run("per-request-wins", func(t *testing.T) {
		reqTemp := 0.2
		spec := nativeSpec("agent", "primary")
		spec.ModelOptions = types.ModelOptions{
			MaxTokens:   512,
			Temperature: &staticTemp,
			Extra:       map[string]any{"top_p": 0.9, "user_tag": "flow"},
		}
		spec.Assemble = func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{Options: types.ModelOptions{
				MaxTokens:   256,
				Temperature: &reqTemp,
				Extra:       map[string]any{"top_p": 0.5},
			}}, nil
		}
		opts := assembled(build(spec))
		if opts.MaxTokens != 256 || opts.Temperature != &reqTemp {
			t.Fatalf("options = %+v, want per-request values kept", opts)
		}
		if opts.Extra["top_p"] != 0.5 || opts.Extra["user_tag"] != "flow" {
			t.Fatalf("extra = %v, want per-request key kept and static key filled", opts.Extra)
		}
	})
}
