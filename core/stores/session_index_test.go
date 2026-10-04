package stores

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// requireSessionIndex mirrors the Stack.Sessions façade rule for stores that
// do not carry the optional listing surface.
func requireSessionIndex(l SessionLog) error {
	if _, ok := l.(SessionIndex); !ok {
		return types.ErrSessionIndexRequired
	}
	return nil
}

// bareLog exposes only the SessionLog surface, as an implementation without
// the optional index would.
type bareLog struct{ log *MemorySessionLog }

func (b bareLog) Load(ctx context.Context, id string) (History, error) {
	return b.log.Load(ctx, id)
}

func (b bareLog) Append(ctx context.Context, id string, v int64, msgs ...types.Message) (int64, error) {
	return b.log.Append(ctx, id, v, msgs...)
}

func (b bareLog) Purge(ctx context.Context, olderThan time.Time) (int, error) {
	return b.log.Purge(ctx, olderThan)
}

func (b bareLog) Delete(ctx context.Context, id string) error {
	return b.log.Delete(ctx, id)
}

func TestSessionIndexContract(t *testing.T) {
	type env struct {
		store *MemorySessionLog
		actor types.Principal
		now   time.Time
	}

	newEnv := func(t *testing.T) *env {
		t.Helper()
		e := &env{now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
		e.store = NewMemorySessionLog(
			WithMemorySessionClock(func() time.Time { return e.now }),
			WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) {
				return e.actor, true
			}),
		)
		return e
	}

	// appendSession creates a session owned by the current actor.
	appendSession := func(t *testing.T, e *env, id string, msgs int) {
		t.Helper()
		set := make([]types.Message, msgs)
		if _, err := e.store.Append(context.Background(), id, 0, set...); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}

	tick := func(e *env, d time.Duration) { e.now = e.now.Add(d) }

	t.Run("stores.sessions-listed-by-owner", func(t *testing.T) {
		e := newEnv(t)
		e.actor = types.Principal{Subject: "u1", Tenant: "t1"}
		appendSession(t, e, "s1", 3)
		tick(e, time.Minute)
		appendSession(t, e, "s2", 1)
		tick(e, time.Minute)
		appendSession(t, e, "s3", 2)
		tick(e, time.Minute)
		e.actor = types.Principal{Subject: "u2", Tenant: "t1"}
		appendSession(t, e, "s4", 5)
		tick(e, time.Minute)
		appendSession(t, e, "s5", 7)

		ctx := context.Background()
		e.actor = types.Principal{Subject: "u1", Tenant: "t1"}
		owner := types.SessionOwner{Tenant: "t1", Subject: "u1"}
		page, _, err := e.store.Sessions(ctx, owner, SessionQuery{})
		if err != nil {
			t.Fatalf("sessions: %v", err)
		}
		if len(page) != 3 {
			t.Fatalf("u1 sessions = %d, want 3", len(page))
		}
		gotIDs := map[string]SessionMeta{}
		for _, m := range page {
			gotIDs[m.ID] = m
			if m.Owner.Subject != "u1" || m.Owner.Tenant != "t1" {
				t.Errorf("session %s owner = %+v, want u1/t1", m.ID, m.Owner)
			}
			if m.Title != "" {
				t.Errorf("session %s title = %q, want unset", m.ID, m.Title)
			}
		}
		for id, wantMsgs := range map[string]int{"s1": 3, "s2": 1, "s3": 2} {
			m, ok := gotIDs[id]
			if !ok {
				t.Errorf("session %s missing from u1 listing", id)
				continue
			}
			if m.Messages != wantMsgs {
				t.Errorf("session %s messages = %d, want %d", id, m.Messages, wantMsgs)
			}
			if m.LastActivity.IsZero() {
				t.Errorf("session %s has zero LastActivity", id)
			}
		}
	})

	t.Run("stores.sessions-order-last-activity", func(t *testing.T) {
		e := newEnv(t)
		e.actor = types.Principal{Subject: "u1", Tenant: "t1"}
		appendSession(t, e, "oldest", 1)
		tick(e, time.Minute)
		appendSession(t, e, "middle", 1)
		tick(e, time.Minute)
		appendSession(t, e, "newest", 1)

		ctx := context.Background()
		owner := types.SessionOwner{Tenant: "t1", Subject: "u1"}
		e.actor = types.Principal{Subject: "u1", Tenant: "t1"}

		_, cur, err := e.store.Sessions(ctx, owner, SessionQuery{Limit: 2})
		if err != nil {
			t.Fatalf("page one: %v", err)
		}
		if cur == "" {
			t.Fatal("page one returned no cursor")
		}

		tick(e, time.Minute)
		if _, err := e.store.Append(ctx, "oldest", 1, types.Message{}); err != nil {
			t.Fatalf("append to oldest: %v", err)
		}

		first, _, err := e.store.Sessions(ctx, owner, SessionQuery{Limit: 10})
		if err != nil {
			t.Fatalf("list after append: %v", err)
		}
		if len(first) != 3 || first[0].ID != "oldest" {
			got := []string{}
			for _, m := range first {
				got = append(got, m.ID)
			}
			t.Fatalf("after append to oldest, order = %v, want oldest first", got)
		}

		seen := map[string]bool{}
		for _, m := range first {
			seen[m.ID] = true
		}

		// The pre-append cursor still resumes its page without duplicating
		// rows; the appended session sits above it and is not repeated.
		resumed, cur2, err := e.store.Sessions(ctx, owner, SessionQuery{Limit: 10, After: cur})
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		for _, m := range resumed {
			if seen[m.ID] {
				t.Errorf("duplicate %s after resume", m.ID)
			}
			seen[m.ID] = true
		}
		if cur2 != "" {
			t.Errorf("cursor past end = %q, want empty", cur2)
		}

		// A fresh page taken after the append resumes correctly too.
		fresh, cur3, err := e.store.Sessions(ctx, owner, SessionQuery{Limit: 2})
		if err != nil {
			t.Fatalf("fresh page: %v", err)
		}
		if len(fresh) != 2 || fresh[0].ID != "oldest" || fresh[1].ID != "newest" {
			t.Fatalf("fresh page = %v, want [oldest newest]", fresh)
		}
		tail, cur4, err := e.store.Sessions(ctx, owner, SessionQuery{Limit: 2, After: cur3})
		if err != nil {
			t.Fatalf("fresh resume: %v", err)
		}
		if len(tail) != 1 || tail[0].ID != "middle" {
			t.Fatalf("fresh resume = %v, want [middle]", tail)
		}
		if cur4 != "" {
			t.Errorf("fresh cursor past end = %q, want empty", cur4)
		}
	})

	t.Run("stores.sessions-default-excludes-forks-children", func(t *testing.T) {
		e := newEnv(t)
		e.actor = types.Principal{Subject: "u1", Tenant: "t1"}
		ctx := context.Background()
		appendSession(t, e, "primary", 2)
		tick(e, time.Minute)
		forkID, err := e.store.Fork(ctx, "primary", "m0000000000000001")
		if err != nil {
			t.Fatalf("fork: %v", err)
		}
		tick(e, time.Minute)
		appendSession(t, e, "child", 1)
		tick(e, time.Minute)
		appendSession(t, e, "shadow", 1)
		if err := e.store.Link(ctx, "primary", "child", SessionChild); err != nil {
			t.Fatalf("link child: %v", err)
		}
		if err := e.store.Link(ctx, "primary", "shadow", SessionShadow); err != nil {
			t.Fatalf("link shadow: %v", err)
		}

		owner := types.SessionOwner{Tenant: "t1", Subject: "u1"}
		def, _, err := e.store.Sessions(ctx, owner, SessionQuery{})
		if err != nil {
			t.Fatalf("default listing: %v", err)
		}
		got := map[SessionKind]int{}
		for _, m := range def {
			got[m.Kind]++
		}
		if got[SessionPrimary] != 1 || got[SessionFork] != 1 || got[SessionChild] != 0 || got[SessionShadow] != 0 {
			t.Fatalf("default listing kinds = %v, want one primary and one fork", got)
		}

		kids, _, err := e.store.Sessions(ctx, owner, SessionQuery{Kinds: []SessionKind{SessionChild}})
		if err != nil {
			t.Fatalf("child listing: %v", err)
		}
		if len(kids) != 1 || kids[0].ID != "child" || kids[0].Kind != SessionChild {
			t.Fatalf("child listing = %+v, want [child]", kids)
		}
		if forkID == "" {
			t.Fatal("fork returned empty id")
		}
	})

	t.Run("stores.session-index-optional", func(t *testing.T) {
		e := newEnv(t)
		e.actor = types.Principal{Subject: "u1", Tenant: "t1"}
		ctx := context.Background()

		bl := bareLog{e.store}
		if err := requireSessionIndex(bl); !errors.Is(err, types.ErrSessionIndexRequired) {
			t.Fatalf("listing without index = %v, want ErrSessionIndexRequired", err)
		}

		// Every other operation still works on a log without the index.
		if _, err := bl.Append(ctx, "s1", 0, types.Message{}); err != nil {
			t.Fatalf("append without index: %v", err)
		}
		h, err := bl.Load(ctx, "s1")
		if err != nil {
			t.Fatalf("load without index: %v", err)
		}
		if len(h.Messages) != 1 {
			t.Fatalf("messages = %d, want 1", len(h.Messages))
		}
		if err := bl.Delete(ctx, "s1"); err != nil {
			t.Fatalf("delete without index: %v", err)
		}
	})
}
