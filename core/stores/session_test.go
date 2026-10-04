package stores

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type fakeDependents struct {
	deleted []string
	copied  [][2]string
}

func (f *fakeDependents) DeleteSessionDependents(_ context.Context, sessionID string) error {
	f.deleted = append(f.deleted, sessionID)
	return nil
}

func (f *fakeDependents) CopySessionDependents(_ context.Context, from, to string) error {
	f.copied = append(f.copied, [2]string{from, to})
	return nil
}

type fakeLeases struct {
	active map[string]bool
}

func (f *fakeLeases) SessionLeaseActive(_ context.Context, sessionID string) bool {
	return f.active[sessionID]
}

func sessionTestStore(now *time.Time) (*MemorySessionLog, *fakeDependents, *fakeLeases) {
	deps := &fakeDependents{}
	leases := &fakeLeases{active: map[string]bool{}}
	clock := func() time.Time { return *now }
	store := NewMemorySessionLog(
		WithMemorySessionClock(clock),
		WithSessionPrincipals(principalFromTestCtx),
		WithSessionDependents(deps),
		WithSessionLeases(leases),
	)
	return store, deps, leases
}

type testPrincipalKey struct{}

func withTestPrincipal(ctx context.Context, p types.Principal) context.Context {
	return context.WithValue(ctx, testPrincipalKey{}, p)
}

func principalFromTestCtx(ctx context.Context) (types.Principal, bool) {
	p, ok := ctx.Value(testPrincipalKey{}).(types.Principal)
	return p, ok
}

var (
	ownerTenantA = types.Principal{Tenant: "t-a", Subject: "u1"}
	peerTenantA  = types.Principal{Tenant: "t-a", Subject: "u2"}
	readerTenant = types.Principal{Tenant: "t-a", Subject: "u2", Scopes: []string{"session:read"}}
	holder       = types.Principal{Tenant: "t-a", Subject: "u2", Scopes: []string{"session:hold", "session:write"}}
	outsider     = types.Principal{Tenant: "t-b", Subject: "u9"}
)

func TestSessionLogContract(t *testing.T) {
	t.Run("append grows version and assigns ids", func(t *testing.T) {
		var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)

		v1, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser})
		if err != nil {
			t.Fatalf("first append: %v", err)
		}
		v2, err := store.Append(ctx, "s1", v1, types.Message{Role: types.RoleAssistant})
		if err != nil {
			t.Fatalf("second append: %v", err)
		}
		if v1 != 1 || v2 != 2 {
			t.Fatalf("versions = %d, %d; want 1, 2", v1, v2)
		}
		h, err := store.Load(ctx, "s1")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if h.Owner != (types.SessionOwner{Tenant: "t-a", Subject: "u1"}) {
			t.Fatalf("owner = %+v", h.Owner)
		}
		if h.Messages[0].ID == "" || h.Messages[0].ID == h.Messages[1].ID {
			t.Fatalf("message ids not unique: %q, %q", h.Messages[0].ID, h.Messages[1].ID)
		}
		if h.Version != 2 || h.ForkedFrom != nil {
			t.Fatalf("history = %+v", h)
		}
	})

	t.Run("stores.append-conflict", func(t *testing.T) {
		var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)

		if _, err := store.Append(ctx, "s1", 3); !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("append on new session with version 3: %v", err)
		}
		if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("first append: %v", err)
		}
		if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("stale append: %v", err)
		}
		if _, err := store.Append(ctx, "s1", 1, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append at current version: %v", err)
		}
	})

	t.Run("identity.session-forbidden-cross-tenant", func(t *testing.T) {
		var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		bctx := withTestPrincipal(context.Background(), outsider)
		if _, err := store.Load(bctx, "s1"); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("cross-tenant load: %v", err)
		}
		if _, err := store.Append(bctx, "s1", 1, types.Message{Role: types.RoleUser}); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("cross-tenant append: %v", err)
		}
	})

	t.Run("identity.session-scope-read", func(t *testing.T) {
		var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		okCtx := withTestPrincipal(context.Background(), readerTenant)
		if _, err := store.Load(okCtx, "s1"); err != nil {
			t.Fatalf("load with session:read: %v", err)
		}
		noCtx := withTestPrincipal(context.Background(), peerTenantA)
		if _, err := store.Load(noCtx, "s1"); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("load without scope: %v", err)
		}
		if _, err := store.Append(noCtx, "s1", 1, types.Message{Role: types.RoleUser}); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("write without session:write: %v", err)
		}
	})

	t.Run("identity.sessions-owner-checked", func(t *testing.T) {
		var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		u1 := withTestPrincipal(context.Background(), ownerTenantA)
		u2 := withTestPrincipal(context.Background(), peerTenantA)
		for _, id := range []string{"a1", "a2"} {
			if _, err := store.Append(u1, id, 0, types.Message{Role: types.RoleUser}); err != nil {
				t.Fatalf("append %s: %v", id, err)
			}
		}
		if _, err := store.Append(u2, "b1", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append b1: %v", err)
		}
		rows, _, err := store.Sessions(u2, types.SessionOwner{Tenant: "t-a", Subject: "u1"}, SessionQuery{})
		if err != nil {
			t.Fatalf("sessions: %v", err)
		}
		for _, m := range rows {
			if m.Owner.Subject != "u2" {
				t.Fatalf("leaked session %s of %s", m.ID, m.Owner.Subject)
			}
		}
	})

	t.Run("index lists by last activity with cursor", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		for _, id := range []string{"old", "mid", "new"} {
			if _, err := store.Append(ctx, id, 0, types.Message{Role: types.RoleUser}); err != nil {
				t.Fatalf("append %s: %v", id, err)
			}
			now = now.Add(time.Hour)
		}
		owner := types.SessionOwner{Tenant: "t-a", Subject: "u1"}
		page1, cur, err := store.Sessions(ctx, owner, SessionQuery{Limit: 2})
		if err != nil {
			t.Fatalf("page1: %v", err)
		}
		if len(page1) != 2 || page1[0].ID != "new" || page1[1].ID != "mid" {
			t.Fatalf("page1 = %v", ids(page1))
		}
		page2, cur2, err := store.Sessions(ctx, owner, SessionQuery{Limit: 2, After: cur})
		if err != nil {
			t.Fatalf("page2: %v", err)
		}
		if len(page2) != 1 || page2[0].ID != "old" {
			t.Fatalf("page2 = %v", ids(page2))
		}
		if cur2 != "" {
			t.Fatalf("cursor past end = %q", cur2)
		}
		// touching the oldest session lifts it to the front
		now = now.Add(time.Hour)
		if _, err := store.Append(ctx, "old", 1, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("touch: %v", err)
		}
		page3, _, err := store.Sessions(ctx, owner, SessionQuery{Limit: 3})
		if err != nil {
			t.Fatalf("page3: %v", err)
		}
		if page3[0].ID != "old" {
			t.Fatalf("page3[0] = %s", page3[0].ID)
		}
	})

	t.Run("default kinds hide children and shadows", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "p", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append parent: %v", err)
		}
		forkID, err := store.Fork(ctx, "p", firstMsgID(t, store, ctx, "p"))
		if err != nil {
			t.Fatalf("fork: %v", err)
		}
		for _, id := range []string{"child", "shadow"} {
			if _, err := store.Append(ctx, id, 0, types.Message{Role: types.RoleUser}); err != nil {
				t.Fatalf("append %s: %v", id, err)
			}
		}
		if err := store.Link(ctx, "p", "child", SessionChild); err != nil {
			t.Fatalf("link child: %v", err)
		}
		if err := store.Link(ctx, "p", "shadow", SessionShadow); err != nil {
			t.Fatalf("link shadow: %v", err)
		}
		owner := types.SessionOwner{Tenant: "t-a", Subject: "u1"}
		rows, _, err := store.Sessions(ctx, owner, SessionQuery{})
		if err != nil {
			t.Fatalf("sessions: %v", err)
		}
		got := ids(rows)
		want := map[string]bool{"p": true, forkID: true}
		if len(got) != 2 || !want[got[0]] || !want[got[1]] {
			t.Fatalf("default listing = %v, want p and fork only", got)
		}
		childKind := SessionChild
		rows, _, err = store.Sessions(ctx, owner, SessionQuery{Kinds: []SessionKind{childKind}})
		if err != nil {
			t.Fatalf("child query: %v", err)
		}
		if len(rows) != 1 || rows[0].ID != "child" {
			t.Fatalf("child query = %v", ids(rows))
		}
	})

	t.Run("fork copies the prefix", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, deps, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "p", 0,
			types.Message{Role: types.RoleUser},
			types.Message{Role: types.RoleAssistant},
			types.Message{Role: types.RoleUser},
		); err != nil {
			t.Fatalf("append: %v", err)
		}
		h, _ := store.Load(ctx, "p")
		upTo := h.Messages[1].ID

		forkID, err := store.Fork(ctx, "p", upTo)
		if err != nil {
			t.Fatalf("fork: %v", err)
		}
		fh, err := store.Load(ctx, forkID)
		if err != nil {
			t.Fatalf("load fork: %v", err)
		}
		if len(fh.Messages) != 2 || fh.Messages[0].ID != h.Messages[0].ID || fh.Messages[1].ID != upTo {
			t.Fatalf("fork messages = %v", fh.Messages)
		}
		if fh.ForkedFrom == nil || fh.ForkedFrom.SessionID != "p" || fh.ForkedFrom.UpTo != upTo {
			t.Fatalf("forkedFrom = %+v", fh.ForkedFrom)
		}
		if fh.Owner != (types.SessionOwner{Tenant: "t-a", Subject: "u1"}) {
			t.Fatalf("fork owner = %+v", fh.Owner)
		}
		if len(deps.copied) != 1 || deps.copied[0] != [2]string{"p", forkID} {
			t.Fatalf("copied dependents = %v", deps.copied)
		}
		if len(deps.deleted) != 0 {
			t.Fatalf("fork deleted dependents: %v", deps.deleted)
		}
	})

	t.Run("fork refuses under a live lease", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, leases := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "p", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		leases.active["p"] = true
		if _, err := store.Fork(ctx, "p", firstMsgID(t, store, ctx, "p")); !errors.Is(err, types.ErrRunActive) {
			t.Fatalf("fork under lease: %v", err)
		}
	})

	t.Run("fork survives parent delete", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "p", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		forkID, err := store.Fork(ctx, "p", firstMsgID(t, store, ctx, "p"))
		if err != nil {
			t.Fatalf("fork: %v", err)
		}
		if err := store.Delete(ctx, "p"); err != nil {
			t.Fatalf("delete parent: %v", err)
		}
		if _, err := store.Load(ctx, forkID); err != nil {
			t.Fatalf("load fork after parent delete: %v", err)
		}
	})

	t.Run("update session patches metadata", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "s1", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		title := "trip plan"
		archived := true
		if err := store.UpdateSession(ctx, "s1", SessionPatch{Title: &title, Archived: &archived}); err != nil {
			t.Fatalf("update: %v", err)
		}
		rows, _, err := store.Sessions(ctx, types.SessionOwner{Tenant: "t-a", Subject: "u1"}, SessionQuery{})
		if err != nil {
			t.Fatalf("sessions: %v", err)
		}
		if len(rows) != 0 {
			t.Fatalf("archived session in default listing: %v", ids(rows))
		}
		yes := true
		rows, _, err = store.Sessions(ctx, types.SessionOwner{Tenant: "t-a", Subject: "u1"}, SessionQuery{Archived: &yes})
		if err != nil {
			t.Fatalf("archived query: %v", err)
		}
		if len(rows) != 1 || rows[0].Title != title || !rows[0].TitleLocked {
			t.Fatalf("archived rows = %+v", rows)
		}
		hold := "case-42"
		if err := store.UpdateSession(ctx, "s1", SessionPatch{Hold: &hold}); !errors.Is(err, ErrHoldScopeMissing) {
			t.Fatalf("hold without scope: %v", err)
		}
	})

	t.Run("stores.delete-cascades", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, deps, leases := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := store.Append(ctx, "p", 0, types.Message{Role: types.RoleUser}); err != nil {
			t.Fatalf("append: %v", err)
		}
		for _, id := range []string{"kid", "shadow"} {
			if _, err := store.Append(ctx, id, 0, types.Message{Role: types.RoleUser}); err != nil {
				t.Fatalf("append %s: %v", id, err)
			}
		}
		if err := store.Link(ctx, "p", "kid", SessionChild); err != nil {
			t.Fatalf("link kid: %v", err)
		}
		if err := store.Link(ctx, "p", "shadow", SessionShadow); err != nil {
			t.Fatalf("link shadow: %v", err)
		}

		hctx := withTestPrincipal(context.Background(), holder)
		hold := "case-42"
		if err := store.UpdateSession(hctx, "p", SessionPatch{Hold: &hold}); err != nil {
			t.Fatalf("set hold: %v", err)
		}
		if err := store.Delete(ctx, "p"); !errors.Is(err, types.ErrSessionHeld) {
			t.Fatalf("delete held: %v", err)
		}
		none := ""
		if err := store.UpdateSession(hctx, "p", SessionPatch{Hold: &none}); err != nil {
			t.Fatalf("clear hold: %v", err)
		}
		leases.active["p"] = true
		if err := store.Delete(ctx, "p"); !errors.Is(err, types.ErrRunActive) {
			t.Fatalf("delete leased: %v", err)
		}
		leases.active["p"] = false

		if err := store.Delete(ctx, "p"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		for _, id := range []string{"p", "kid", "shadow"} {
			if _, err := store.Load(ctx, id); !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("load %s after delete: %v", id, err)
			}
		}
		want := []string{"shadow", "kid", "p"}
		if len(deps.deleted) != len(want) {
			t.Fatalf("deleted dependents = %v, want %v", deps.deleted, want)
		}
		seen := map[string]bool{}
		for _, id := range deps.deleted {
			seen[id] = true
		}
		for _, id := range want {
			if !seen[id] {
				t.Fatalf("dependents of %s not deleted: %v", id, deps.deleted)
			}
		}
	})

	t.Run("stores.purge-respects-pinned-and-archived", func(t *testing.T) {
		now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		store, _, _ := sessionTestStore(&now)
		ctx := withTestPrincipal(context.Background(), ownerTenantA)

		seed := func(id string, age time.Duration, mut func(*SessionPatch)) {
			start := now.Add(-age)
			s := NewMemorySessionLog(
				WithMemorySessionClock(func() time.Time { return start }),
				WithSessionPrincipals(principalFromTestCtx),
			)
			sctx := withTestPrincipal(context.Background(), ownerTenantA)
			if _, err := s.Append(sctx, id, 0, types.Message{Role: types.RoleUser}); err != nil {
				t.Fatalf("seed %s: %v", id, err)
			}
			rec := s.sessions[id]
			store.mu.Lock()
			store.sessions[id] = rec
			store.mu.Unlock()
			if mut != nil {
				var p SessionPatch
				mut(&p)
				if err := store.UpdateSession(sctx, id, p); err != nil {
					t.Fatalf("patch %s: %v", id, err)
				}
			}
		}
		pinned := true
		seed("pinned", 400*24*time.Hour, func(p *SessionPatch) { p.Pinned = &pinned })
		seed("archived-old", 200*24*time.Hour, nil)
		archived := true
		seed("archived-new", 100*24*time.Hour, func(p *SessionPatch) { p.Archived = &archived })
		seed("fresh", 24*time.Hour, nil)

		cut := now.Add(-30 * 24 * time.Hour)
		n, err := store.Purge(ctx, cut)
		if err != nil {
			t.Fatalf("purge: %v", err)
		}
		if n != 1 {
			t.Fatalf("purged = %d, want 1", n)
		}
		if _, err := store.Load(ctx, "archived-old"); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("archived-old survived purge: %v", err)
		}
		for _, id := range []string{"pinned", "archived-new", "fresh"} {
			if _, err := store.Load(ctx, id); err != nil {
				t.Fatalf("%s purged: %v", id, err)
			}
		}
	})
}

func ids(rows []SessionMeta) []string {
	out := make([]string, len(rows))
	for i, m := range rows {
		out[i] = m.ID
	}
	return out
}

func firstMsgID(t *testing.T, store *MemorySessionLog, ctx context.Context, sessionID string) string {
	t.Helper()
	h, err := store.Load(ctx, sessionID)
	if err != nil || len(h.Messages) == 0 {
		t.Fatalf("load %s: %v", sessionID, err)
	}
	return h.Messages[0].ID
}
