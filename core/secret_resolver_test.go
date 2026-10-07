package gohan

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type testSecretResolver struct {
	name string
}

func (r testSecretResolver) Resolve(context.Context, types.Principal, string) (string, error) {
	return "", nil
}

func TestWithSecretResolver(t *testing.T) {
	resolver := testSecretResolver{name: "primary"}
	stack, err := Build(WithModels(fakeModel{profile: profile("native", true)}), WithSecretResolver(resolver))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if stack.Secrets() != types.SecretResolver(resolver) {
		t.Fatalf("Stack.Secrets = %v, want the bound resolver", stack.Secrets())
	}

	cfg := &resolvedNativeConfig{specs: []types.ToolSpec{{Name: "echo"}}}
	env := &turnEnv{c: stack.nativeTurnConfig(cfg)}
	ctx := env.effectCtx(context.Background())
	got, ok := types.SecretsFrom(ctx)
	if !ok || got != types.SecretResolver(resolver) {
		t.Fatalf("SecretsFrom effect ctx = %v, %v; want bound resolver", got, ok)
	}

	bare, err := Build(WithModels(fakeModel{profile: profile("native", true)}))
	if err != nil {
		t.Fatalf("Build without resolver: %v", err)
	}
	if bare.Secrets() != nil {
		t.Fatalf("Stack.Secrets without option = %v, want nil", bare.Secrets())
	}
	bareEnv := &turnEnv{c: bare.nativeTurnConfig(cfg)}
	if r, ok := types.SecretsFrom(bareEnv.effectCtx(context.Background())); ok || r != nil {
		t.Fatalf("SecretsFrom ctx without option = %v, %v; want none", r, ok)
	}
}
