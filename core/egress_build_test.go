package gohan

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"iter"
	"net/http"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type egressBuildModel struct {
	calls *int
}

func (m egressBuildModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "egress", Caps: types.Caps{Tools: true}}
}

func (m egressBuildModel) Generate(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if m.calls != nil {
			*m.calls++
		}
	}
}

func buildEgressTestStack(t *testing.T, tool types.Tool, opts ...Option) (*Stack, error) {
	return buildEgressTestStackUsingModel(t, tool, egressBuildModel{}, opts...)
}

func buildEgressTestStackUsingModel(t *testing.T, tool types.Tool, model types.Model, opts ...Option) (*Stack, error) {
	t.Helper()
	all := []Option{
		WithModels(model),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "egress"},
			Profile: "egress",
			Tools:   []types.Tool{tool},
			Assemble: func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{}, nil
			},
		}),
	}
	all = append(all, opts...)
	return Build(all...)
}

func newEgressBuildTool(t *testing.T, name string, specOptions ...ToolOption) types.Tool {
	t.Helper()
	tool, err := NewTool[struct{}, struct{}](name, "", func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, nil
	}, specOptions...)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func TestEgressBuild(t *testing.T) {
	t.Run("network tool requires policy", func(t *testing.T) {
		providerCalls := 0
		tool := newEgressBuildTool(t, "network", WithHTTP())
		_, err := buildEgressTestStackUsingModel(t, tool, egressBuildModel{calls: &providerCalls})
		if !errors.Is(err, types.ErrEgressPolicyRequired) || !strings.Contains(err.Error(), "network") {
			t.Fatalf("Build error = %v", err)
		}
		if providerCalls != 0 {
			t.Fatalf("provider calls = %d", providerCalls)
		}
	})

	t.Run("private policy derives no exfil", func(t *testing.T) {
		policy := types.EgressPolicy{Allow: []string{"10.0.0.1"}}
		tool := newEgressBuildTool(t, "network", WithHTTP(), WithEgress(policy))
		stack, err := buildEgressTestStack(t, tool, WithEgressClient(func(types.EgressPolicy) *http.Client { return &http.Client{} }))
		if err != nil {
			t.Fatal(err)
		}
		cfg, _ := stack.resolvedNative("egress")
		if cfg.specs[0].Capabilities.Exfil {
			t.Fatalf("private policy derived Exfil = true: %+v", cfg.specs[0])
		}
		policy.Allow = append(policy.Allow, "example.com")
		tool = newEgressBuildTool(t, "network", WithHTTP(), WithEgress(policy))
		stack, err = buildEgressTestStack(t, tool, WithEgressClient(func(types.EgressPolicy) *http.Client { return &http.Client{} }))
		if err != nil {
			t.Fatal(err)
		}
		cfg, _ = stack.resolvedNative("egress")
		if !cfg.specs[0].Capabilities.Exfil {
			t.Fatalf("public allow did not derive Exfil: %+v", cfg.specs[0])
		}
	})

	t.Run("policy requires factory", func(t *testing.T) {
		tool := newEgressBuildTool(t, "policy", WithEgress(types.EgressPolicy{Allow: []string{"example.com"}}))
		_, err := buildEgressTestStack(t, tool)
		var required types.ErrEgressClientRequired
		if !errors.As(err, &required) || required.Name != "policy" {
			t.Fatalf("Build error = %v, want required client for policy", err)
		}
	})
}

func TestNativeEgressClientBinding(t *testing.T) {
	client := &http.Client{}
	var got *http.Client
	tool, err := NewTool[struct{}, struct{}]("bound", "", func(ctx context.Context, _ struct{}) (struct{}, error) {
		got = EgressClient(ctx)
		return struct{}{}, nil
	}, WithEgress(types.EgressPolicy{Allow: []string{"example.com"}}))
	if err != nil {
		t.Fatal(err)
	}
	stack, err := buildEgressTestStack(t, tool, WithEgressClient(func(types.EgressPolicy) *http.Client { return client }))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := stack.resolvedNative("egress")
	turn := stack.nativeTurnConfig(cfg)
	if _, err := turn.exec(context.Background(), types.ToolUse{ID: "call", Name: "bound", Args: jsontext.Value(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if got != client {
		t.Fatalf("tool client = %p, want Build client %p", got, client)
	}

	var plainClient *http.Client
	plain, err := NewTool[struct{}, struct{}]("plain", "", func(ctx context.Context, _ struct{}) (struct{}, error) {
		plainClient = EgressClient(ctx)
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	stack, err = buildEgressTestStack(t, plain)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = stack.resolvedNative("egress")
	turn = stack.nativeTurnConfig(cfg)
	if _, err := turn.exec(context.Background(), types.ToolUse{ID: "call", Name: "plain", Args: jsontext.Value(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if plainClient != nil {
		t.Fatalf("no-policy tool client = %p, want nil", plainClient)
	}
}
