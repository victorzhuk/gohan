package cache

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func principal(t string, subject string) context.Context {
	return types.WithPrincipal(context.Background(), types.Principal{Tenant: t, Subject: subject})
}

func input() Input {
	return Input{
		Scope:         ScopeShared,
		Manifest:      Manifest{Prompts: "p1", Chains: "c1", Toolset: "t1", ModelVersion: "m1"},
		PromptVersion: "v1",
		Request:       types.ModelRequest{Options: types.ModelOptions{MaxTokens: 128}},
	}
}

func key(t *testing.T, ctx context.Context, in Input) string {
	t.Helper()
	k, err := Key(ctx, in)
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	return k
}

func TestKey(t *testing.T) {
	t.Run("cache.key-includes-manifest", func(t *testing.T) {
		base := input()
		cases := map[string]func(*Manifest){
			"prompts":       func(m *Manifest) { m.Prompts = "p2" },
			"chains":        func(m *Manifest) { m.Chains = "c2" },
			"toolset":       func(m *Manifest) { m.Toolset = "t2" },
			"model version": func(m *Manifest) { m.ModelVersion = "m2" },
		}
		for name, change := range cases {
			t.Run(name, func(t *testing.T) {
				changed := input()
				change(&changed.Manifest)
				if key(t, principal("t1", "u1"), base) == key(t, principal("t1", "u1"), changed) {
					t.Fatalf("%s: changed manifest produced an equal key", name)
				}
				if key(t, principal("t1", "u1"), base) != key(t, principal("t1", "u1"), input()) {
					t.Fatalf("%s: identical inputs produced different keys", name)
				}
			})
		}
	})

	t.Run("deterministic across calls", func(t *testing.T) {
		a := key(t, principal("t1", "u1"), input())
		b := key(t, principal("t1", "u1"), input())
		if a != b {
			t.Fatalf("equal inputs produced %q and %q", a, b)
		}
	})

	t.Run("tenant scopes the key", func(t *testing.T) {
		if key(t, principal("t1", "u1"), input()) == key(t, principal("t2", "u1"), input()) {
			t.Fatal("different tenants produced an equal key")
		}
	})

	t.Run("scope class scopes the key", func(t *testing.T) {
		shared := input()
		shared.Scope = ScopeShared
		perUser := input()
		perUser.Scope = ScopeUser
		if key(t, principal("t1", "u1"), shared) == key(t, principal("t1", "u1"), perUser) {
			t.Fatal("different scope classes produced an equal key")
		}
	})

	t.Run("per-user scope pins the subject", func(t *testing.T) {
		perUser := input()
		perUser.Scope = ScopeUser
		if key(t, principal("t1", "u1"), perUser) == key(t, principal("t1", "u2"), perUser) {
			t.Fatal("per-user scope ignored the subject")
		}
		shared := input()
		shared.Scope = ScopeShared
		if key(t, principal("t1", "u1"), shared) != key(t, principal("t1", "u2"), shared) {
			t.Fatal("shared scope depended on the subject")
		}
	})

	t.Run("absent principal is explicit", func(t *testing.T) {
		anon := key(t, context.Background(), input())
		emptyTenant := key(t, principal("", "u1"), input())
		if anon == emptyTenant {
			t.Fatal("no principal collided with an empty-tenant principal")
		}
	})

	t.Run("prompt version scopes the key", func(t *testing.T) {
		v2 := input()
		v2.PromptVersion = "v2"
		if key(t, principal("t1", "u1"), input()) == key(t, principal("t1", "u1"), v2) {
			t.Fatal("different prompt versions produced an equal key")
		}
		none := input()
		none.PromptVersion = ""
		if key(t, principal("t1", "u1"), input()) == key(t, principal("t1", "u1"), none) {
			t.Fatal("a set prompt version collided with an absent one")
		}
	})

	t.Run("normalized request scopes the key", func(t *testing.T) {
		other := input()
		other.Request.Options.MaxTokens = 256
		if key(t, principal("t1", "u1"), input()) == key(t, principal("t1", "u1"), other) {
			t.Fatal("different requests produced an equal key")
		}
		retagged := input()
		retagged.Request.Metadata = types.RequestMetadata{CostTags: types.CostTags{Feature: "billing"}}
		if key(t, principal("t1", "u1"), input()) != key(t, principal("t1", "u1"), retagged) {
			t.Fatal("run attribution changed the key")
		}
	})
}
