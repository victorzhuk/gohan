package stores

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestSessionHold(t *testing.T) {
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	hold := "case-42"
	none := ""

	t.Run("stores.hold-blocks-delete-and-purge", func(t *testing.T) {
		now := start
		store, _, _ := sessionTestStore(&now)
		audit := NewMemoryAuditLog()
		store.audit = audit
		octx := withTestPrincipal(context.Background(), ownerTenantA)
		hctx := withTestPrincipal(context.Background(), holder)
		if _, err := store.Append(octx, "p", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := store.UpdateSession(hctx, "p", SessionPatch{Hold: &hold}); err != nil {
			t.Fatalf("set hold: %v", err)
		}
		now = start.Add(400 * 24 * time.Hour)

		if err := store.Delete(octx, "p"); !errors.Is(err, types.ErrSessionHeld) {
			t.Fatalf("delete held session: %v", err)
		}
		n, err := store.Purge(octx, now)
		if err != nil {
			t.Fatalf("purge: %v", err)
		}
		if n != 0 {
			t.Fatalf("purge removed %d held sessions", n)
		}
		h, err := store.Load(octx, "p")
		if err != nil || len(h.Messages) != 1 {
			t.Fatalf("held session not intact: %v %d msgs", err, len(h.Messages))
		}
		held := store.HeldSessions()
		if len(held) != 1 || held[0] != "p" {
			t.Fatalf("HeldSessions = %v, want [p]", held)
		}

		if err := store.UpdateSession(hctx, "p", SessionPatch{Hold: &none}); err != nil {
			t.Fatalf("clear hold: %v", err)
		}
		n, err = store.Purge(octx, now)
		if err != nil || n != 1 {
			t.Fatalf("purge after clear: n=%d err=%v", n, err)
		}
		var kinds []AuditKind
		for r, err := range audit.Read(context.Background(), "p") {
			if err != nil {
				t.Fatalf("audit read: %v", err)
			}
			kinds = append(kinds, r.Kind)
		}
		if len(kinds) != 2 || kinds[0] != AuditHoldSet || kinds[1] != AuditHoldCleared {
			t.Fatalf("audit kinds = %v, want [hold_set hold_cleared]", kinds)
		}
	})
	t.Run("hold audit failure is atomic", func(t *testing.T) {
		now := start
		audit := NewMemoryAuditLog()
		store, _, _ := sessionTestStore(&now)
		store.audit = audit
		if _, err := store.Append(withTestPrincipal(context.Background(), ownerTenantA), "atomic", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		ctx, cancel := context.WithCancel(withTestPrincipal(context.Background(), holder))
		cancel()
		title, archived, pinned, hold := "new title", true, true, "case-42"
		err := store.UpdateSession(ctx, "atomic", SessionPatch{
			Title: &title, Archived: &archived, Pinned: &pinned, Hold: &hold,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("UpdateSession error = %v, want canceled audit error", err)
		}
		rec := store.sessions["atomic"].meta
		if rec.Title != "" || rec.TitleLocked || rec.Archived || rec.Pinned || rec.Hold != "" {
			t.Fatalf("metadata changed after audit failure: %+v", rec)
		}
	})

	t.Run("identity.hold-requires-scope", func(t *testing.T) {
		now := start
		store, _, _ := sessionTestStore(&now)
		octx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(octx, "p", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		noHold := types.Principal{Tenant: "t-a", Subject: "u1", Scopes: []string{scopeSessionWrite}}
		for _, p := range []types.Principal{ownerTenantA, noHold} {
			ctx := withTestPrincipal(context.Background(), p)
			err := store.UpdateSession(ctx, "p", SessionPatch{Hold: &hold})
			if !errors.Is(err, ErrHoldScopeMissing) {
				t.Fatalf("%s set hold: %v, want ErrHoldScopeMissing", p.Subject, err)
			}
		}
		hctx := withTestPrincipal(context.Background(), holder)
		if err := store.UpdateSession(hctx, "p", SessionPatch{Hold: &hold}); err != nil {
			t.Fatalf("holder set hold: %v", err)
		}
		if got := store.HeldSessions(); len(got) != 1 || got[0] != "p" {
			t.Fatalf("HeldSessions = %v, want [p]", got)
		}
	})

	t.Run("redaction.erase-reports-held", func(t *testing.T) {
		now := start
		store, deps, _ := sessionTestStore(&now)
		octx := withTestPrincipal(context.Background(), ownerTenantA)
		hctx := withTestPrincipal(context.Background(), holder)
		if _, err := store.Append(octx, "p", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := store.UpdateSession(hctx, "p", SessionPatch{Hold: &hold}); err != nil {
			t.Fatalf("set hold: %v", err)
		}
		if err := store.Delete(octx, "p"); !errors.Is(err, types.ErrSessionHeld) {
			t.Fatalf("delete held session: %v", err)
		}
		if _, err := store.Purge(octx, start.Add(time.Hour)); err != nil {
			t.Fatalf("purge: %v", err)
		}
		if len(store.HeldSessions()) != 1 {
			t.Fatalf("held session not reported for EraseReport.Held")
		}
		if len(deps.deleted) != 0 {
			t.Fatalf("held session dependents deleted: %v", deps.deleted)
		}
	})
}
