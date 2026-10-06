package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func memoryTestStack(t *testing.T) *Stack {
	t.Helper()
	notes := stores.NewMemoryNotes()
	st, err := Build(WithStores(stores.Stores{Notes: notes}))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return st
}

func memoryOwnerCtx(ctx context.Context, tenant, subject string) context.Context {
	return types.WithPrincipal(ctx, types.Principal{Tenant: tenant, Subject: subject})
}

func memoryPut(t *testing.T, st *Stack, owner types.SessionOwner, ids ...string) {
	t.Helper()
	notes, ok := st.stores.Notes.(stores.MemoryStore)
	if !ok {
		t.Fatal("stack notes store lacks memory surface")
	}
	for _, id := range ids {
		err := notes.Put(context.Background(), stores.NotesKey{
			Scope: stores.ScopeSubject, Tenant: owner.Tenant, Subject: owner.Subject,
		}, stores.MemoryEntry{ID: id, Value: json.RawMessage(`{"k":"v"}`)})
		if err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
}

func TestMemoryControls(t *testing.T) {
	owner := types.SessionOwner{Tenant: "t1", Subject: "u1"}

	t.Run("working-state.memory-forget", func(t *testing.T) {
		st := memoryTestStack(t)
		memoryPut(t, st, owner, "e1", "e2")

		ctx := memoryOwnerCtx(context.Background(), "t1", "u1")
		got, err := st.Memory(ctx, owner)
		if err != nil {
			t.Fatalf("memory: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("entries = %d, want 2", len(got))
		}

		if err := st.ForgetMemory(ctx, owner, "e1"); err != nil {
			t.Fatalf("forget: %v", err)
		}

		got, err = st.Memory(ctx, owner)
		if err != nil {
			t.Fatalf("memory after forget: %v", err)
		}
		if len(got) != 1 || got[0].ID != "e2" {
			t.Fatalf("entries after forget = %v, want only e2", got)
		}
		notes, _ := st.stores.Notes.(stores.MemoryStore)
		storeEntries, err := notes.Entries(ctx, stores.NotesKey{
			Scope: stores.ScopeSubject, Tenant: "t1", Subject: "u1",
		})
		if err != nil {
			t.Fatalf("store entries: %v", err)
		}
		if len(storeEntries) != 1 || storeEntries[0].ID != "e2" {
			t.Fatalf("store entries after forget = %v, want only e2", storeEntries)
		}
	})

	t.Run("non-owner is refused", func(t *testing.T) {
		st := memoryTestStack(t)
		memoryPut(t, st, owner, "e1")

		foreignTenant := memoryOwnerCtx(context.Background(), "t2", "u1")
		if _, err := st.Memory(foreignTenant, owner); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("cross-tenant memory err = %v, want ErrSessionForbidden", err)
		}
		if err := st.ForgetMemory(foreignTenant, owner, "e1"); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("cross-tenant forget err = %v, want ErrSessionForbidden", err)
		}
		peer := memoryOwnerCtx(context.Background(), "t1", "u2")
		if _, err := st.Memory(peer, owner); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("same-tenant other-subject memory err = %v, want ErrSessionForbidden", err)
		}
		if err := st.ForgetMemory(peer, owner, "e1"); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("same-tenant other-subject forget err = %v, want ErrSessionForbidden", err)
		}
		if _, err := st.Memory(context.Background(), owner); !errors.Is(err, types.ErrNoPrincipal) {
			t.Fatalf("principal-less memory err = %v, want ErrNoPrincipal", err)
		}

		got, err := st.Memory(memoryOwnerCtx(context.Background(), "t1", "u1"), owner)
		if err != nil || len(got) != 1 {
			t.Fatalf("owner entries after refusals = %v, err %v; want e1 intact", got, err)
		}
	})
}
