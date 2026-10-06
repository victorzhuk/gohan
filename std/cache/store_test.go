package cache

import (
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestStore(t *testing.T) {
	tenantA := func() string { return key(t, principal("t1", "u1"), input()) }
	tenantB := func() string { return key(t, principal("t2", "u1"), input()) }

	t.Run("hit", func(t *testing.T) {
		s := NewStore()
		prefix := types.ModelRequest{Options: types.ModelOptions{MaxTokens: 7}}
		s.Put(tenantA(), prefix)
		got, ok := s.Get(tenantA())
		if !ok {
			t.Fatal("expected hit")
		}
		if got.Options.MaxTokens != 7 {
			t.Fatalf("got %+v, want the stored prefix", got)
		}
	})

	t.Run("miss", func(t *testing.T) {
		s := NewStore()
		if _, ok := s.Get(tenantA()); ok {
			t.Fatal("expected miss on an empty store")
		}
	})

	t.Run("tenant isolation", func(t *testing.T) {
		s := NewStore()
		s.Put(tenantA(), types.ModelRequest{Options: types.ModelOptions{MaxTokens: 7}})
		if _, ok := s.Get(tenantB()); ok {
			t.Fatal("entry from tenant t1 resolved for tenant t2")
		}
	})

	t.Run("scope isolation", func(t *testing.T) {
		s := NewStore()
		shared := input()
		shared.Scope = ScopeShared
		perUser := input()
		perUser.Scope = ScopeUser
		sharedKey, err := Key(principal("t1", "u1"), shared)
		if err != nil {
			t.Fatalf("Key: %v", err)
		}
		userKey, err := Key(principal("t1", "u1"), perUser)
		if err != nil {
			t.Fatalf("Key: %v", err)
		}
		s.Put(sharedKey, types.ModelRequest{Options: types.ModelOptions{MaxTokens: 7}})
		if _, ok := s.Get(userKey); ok {
			t.Fatal("shared-scope entry resolved for the per-user scope class")
		}
	})

	t.Run("put replaces", func(t *testing.T) {
		s := NewStore()
		k := tenantA()
		s.Put(k, types.ModelRequest{Options: types.ModelOptions{MaxTokens: 7}})
		s.Put(k, types.ModelRequest{Options: types.ModelOptions{MaxTokens: 9}})
		got, ok := s.Get(k)
		if !ok || got.Options.MaxTokens != 9 {
			t.Fatalf("got %+v ok=%v, want the replaced prefix", got, ok)
		}
	})
}
