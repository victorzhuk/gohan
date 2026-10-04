package stores

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestSessionLifecycle(t *testing.T) {
	t.Run("stores.purge-respects-pinned-and-archived", func(t *testing.T) {
		base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		now := base
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)

		pin := func(ctx context.Context, store *MemorySessionLog, id string) {
			t.Helper()
			yes := true
			if err := store.UpdateSession(ctx, id, SessionPatch{Pinned: &yes}); err != nil {
				t.Fatalf("pin %s: %v", id, err)
			}
		}
		archive := func(ctx context.Context, store *MemorySessionLog, id string) {
			t.Helper()
			yes := true
			if err := store.UpdateSession(ctx, id, SessionPatch{Archived: &yes}); err != nil {
				t.Fatalf("archive %s: %v", id, err)
			}
		}

		if _, err := store.Append(withTestPrincipal(context.Background(), ownerTenantA), "s-pinned", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append pinned: %v", err)
		}
		now = base.Add(-200 * 24 * time.Hour)
		if _, err := store.Append(withTestPrincipal(context.Background(), ownerTenantA), "s-arch-old", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append old archived: %v", err)
		}
		now = base.Add(-100 * 24 * time.Hour)
		if _, err := store.Append(withTestPrincipal(context.Background(), ownerTenantA), "s-arch-recent", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append recent archived: %v", err)
		}
		now = base

		pin(ctx, store, "s-pinned")
		archive(ctx, store, "s-arch-old")
		archive(ctx, store, "s-arch-recent")

		n, err := store.Purge(ctx, now.Add(-30*24*time.Hour))
		if err != nil {
			t.Fatalf("purge: %v", err)
		}
		if n != 1 {
			t.Fatalf("purged %d sessions; want 1 (the 200-day archived one)", n)
		}
		if _, err := store.Load(ctx, "s-pinned"); err != nil {
			t.Fatalf("pinned session must survive purge: %v", err)
		}
		if _, err := store.Load(ctx, "s-arch-recent"); err != nil {
			t.Fatalf("100-day archived session must survive the 180-day retention: %v", err)
		}
		if _, err := store.Load(ctx, "s-arch-old"); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("load purged session err = %v; want ErrSessionNotFound", err)
		}
	})

	t.Run("identity.sessions-owner-checked", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)

		if _, err := store.Append(withTestPrincipal(context.Background(), ownerTenantA), "u1-s", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append u1 session: %v", err)
		}
		if _, err := store.Append(withTestPrincipal(context.Background(), peerTenantA), "u2-s", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append u2 session: %v", err)
		}

		// u2 has no session:read; the query names u1's owner but the listing
		// must be scoped to u2's own subject.
		ctx := withTestPrincipal(context.Background(), peerTenantA)
		rows, _, err := store.Sessions(ctx, types.SessionOwner{Tenant: ownerTenantA.Tenant, Subject: ownerTenantA.Subject}, SessionQuery{})
		if err != nil {
			t.Fatalf("sessions: %v", err)
		}
		got := ids(rows)
		if len(got) != 1 || got[0] != "u2-s" {
			t.Fatalf("listing = %v; want only the caller's own session [u2-s]", got)
		}
	})

	t.Run("permission.grants-not-inherited-on-fork", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, deps, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)

		v1, err := store.Append(ctx, "parent", 0, types.Message{Role: types.RoleUser})
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if _, err := store.Append(ctx, "parent", v1, types.Message{Role: types.RoleAssistant}); err != nil {
			t.Fatalf("append: %v", err)
		}
		h, err := store.Load(ctx, "parent")
		if err != nil {
			t.Fatalf("load: %v", err)
		}

		child, err := store.Fork(ctx, "parent", h.Messages[0].ID)
		if err != nil {
			t.Fatalf("fork: %v", err)
		}
		// The child inherits nothing but what CopySessionDependents carries:
		// one copy call, parent to child, and no other propagation. The grant
		// store registers no dependent, so a fork child starts with no grants.
		if len(deps.copied) != 1 {
			t.Fatalf("dependent copies = %v; want exactly one parent-to-child copy", deps.copied)
		}
		if deps.copied[0] != [2]string{"parent", child} {
			t.Fatalf("copy = %v; want [parent %s]", deps.copied[0], child)
		}
		if len(deps.deleted) != 0 {
			t.Fatalf("fork must not delete dependents: %v", deps.deleted)
		}
	})

	t.Run("working-state.fork-copies-notes-and-state", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, deps, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)

		v1, err := store.Append(ctx, "parent", 0, types.Message{Role: types.RoleUser})
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if _, err := store.Append(ctx, "parent", v1, types.Message{Role: types.RoleAssistant}); err != nil {
			t.Fatalf("append: %v", err)
		}
		h, err := store.Load(ctx, "parent")
		if err != nil {
			t.Fatalf("load: %v", err)
		}

		child, err := store.Fork(ctx, "parent", h.Messages[1].ID)
		if err != nil {
			t.Fatalf("fork: %v", err)
		}
		if len(deps.copied) != 1 || deps.copied[0] != [2]string{"parent", child} {
			t.Fatalf("dependent copies = %v; want [parent %s]", deps.copied, child)
		}

		childHist, err := store.Load(ctx, child)
		if err != nil {
			t.Fatalf("load fork: %v", err)
		}
		wantUpTo := h.Messages[1].ID
		if childHist.ForkedFrom == nil || childHist.ForkedFrom.SessionID != "parent" || childHist.ForkedFrom.UpTo != wantUpTo {
			t.Fatalf("forkedFrom = %+v; want parent up to %s", childHist.ForkedFrom, wantUpTo)
		}
		if childHist.Version != 2 || len(childHist.Messages) != 2 {
			t.Fatalf("fork version/messages = %d/%d; want 2/2", childHist.Version, len(childHist.Messages))
		}
		parentHist, err := store.Load(ctx, "parent")
		if err != nil {
			t.Fatalf("load parent: %v", err)
		}
		for i := range childHist.Messages {
			if childHist.Messages[i].ID != parentHist.Messages[i].ID {
				t.Fatalf("fork message %d id = %s; want parent's %s", i, childHist.Messages[i].ID, parentHist.Messages[i].ID)
			}
		}

		// Writes after the fork stay local to each session.
		if _, err := store.Append(ctx, "parent", 2, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append parent: %v", err)
		}
		if _, err := store.Append(ctx, child, 2, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append fork: %v", err)
		}
		parentHist, err = store.Load(ctx, "parent")
		if err != nil {
			t.Fatalf("load parent: %v", err)
		}
		childHist, err = store.Load(ctx, child)
		if err != nil {
			t.Fatalf("load fork: %v", err)
		}
		if len(parentHist.Messages) != 3 || len(childHist.Messages) != 3 {
			t.Fatalf("post-fork lengths parent/fork = %d/%d; want 3/3", len(parentHist.Messages), len(childHist.Messages))
		}
		if parentHist.Messages[2].ID == childHist.Messages[2].ID {
			t.Fatalf("post-fork writes share a message id %s; sessions must be isolated", parentHist.Messages[2].ID)
		}
	})
}
