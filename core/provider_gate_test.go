package gohan

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
)

func TestProviderToolGate(t *testing.T) {
	search := types.ToolSpec{Name: "web_search", Executor: types.ByProvider}
	allowDecider := funcDecider(func(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
		return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
	})
	denyDecider := funcDecider(func(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
		return types.Decision[permission.Verdict]{Value: permission.DenyVerdict, Confidence: 1}, nil
	})
	askDecider := funcDecider(func(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
		return types.Decision[permission.Verdict]{Value: permission.Ask, Confidence: 1}, nil
	})
	gate := func(t *testing.T, cfg *resolvedNativeConfig, specs ...types.ToolSpec) types.ModelRequest {
		t.Helper()
		req, err := providerDeclarationGate(cfg)(context.Background(), types.ModelRequest{Tools: specs})
		if err != nil {
			t.Fatalf("gate: %v", err)
		}
		return req
	}
	names := func(req types.ModelRequest) []string {
		var out []string
		for _, s := range req.Tools {
			out = append(out, s.Name)
		}
		return out
	}
	profile := func(caps map[string]types.ProviderToolCap) types.ModelProfile {
		return types.ModelProfile{Caps: types.Caps{ProviderTools: caps}}
	}

	t.Run("tools.provider-tool-gate-before-call", func(t *testing.T) {
		cfg := &resolvedNativeConfig{decider: denyDecider, profile: profile(map[string]types.ProviderToolCap{"web_search": {Approval: true}})}
		req := gate(t, cfg, search)
		if got := names(req); len(got) != 0 {
			t.Fatalf("declared %v, want no search tool", got)
		}
	})
	t.Run("ask declares with approval cap", func(t *testing.T) {
		cfg := &resolvedNativeConfig{decider: askDecider, profile: profile(map[string]types.ProviderToolCap{"web_search": {Approval: true}})}
		if got := names(gate(t, cfg, search)); len(got) != 1 || got[0] != "web_search" {
			t.Fatalf("declared %v, want web_search", got)
		}
	})
	t.Run("ask without approval cap omits", func(t *testing.T) {
		cfg := &resolvedNativeConfig{decider: askDecider, profile: profile(map[string]types.ProviderToolCap{})}
		if got := names(gate(t, cfg, search)); len(got) != 0 {
			t.Fatalf("declared %v, want none", got)
		}
	})
	t.Run("allow declares", func(t *testing.T) {
		cfg := &resolvedNativeConfig{decider: allowDecider, profile: profile(nil)}
		if got := names(gate(t, cfg, search)); len(got) != 1 || got[0] != "web_search" {
			t.Fatalf("declared %v, want web_search", got)
		}
	})
	t.Run("harness tools pass through", func(t *testing.T) {
		local := types.ToolSpec{Name: "local_search", Executor: types.ByHarness}
		cfg := &resolvedNativeConfig{decider: denyDecider, profile: profile(nil)}
		got := names(gate(t, cfg, local, search))
		if len(got) != 1 || got[0] != "local_search" {
			t.Fatalf("declared %v, want local_search only", got)
		}
	})
	t.Run("scope check denies before the decider", func(t *testing.T) {
		cfg := &resolvedNativeConfig{
			decider: allowDecider,
			profile: profile(nil),
		}
		guarded := types.ToolSpec{Name: "web_search", Executor: types.ByProvider, RequiredScopes: []string{"net"}}
		ctx := types.WithRunInfo(context.Background(), types.RunInfo{Principal: types.Principal{Subject: "u1", Tenant: "t1"}})
		req, err := providerDeclarationGate(cfg)(ctx, types.ModelRequest{Tools: []types.ToolSpec{guarded}})
		if err != nil {
			t.Fatalf("gate: %v", err)
		}
		if got := names(req); len(got) != 0 {
			t.Fatalf("declared %v, want none for missing scope", got)
		}
	})
	t.Run("decider failure omits rather than widening", func(t *testing.T) {
		cfg := &resolvedNativeConfig{
			decider: funcDecider(func(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
				return types.Decision[permission.Verdict]{}, errors.New("policy store down")
			}),
			profile: profile(nil),
		}
		if got := names(gate(t, cfg, search)); len(got) != 0 {
			t.Fatalf("declared %v, want none on decider failure", got)
		}
	})
}
