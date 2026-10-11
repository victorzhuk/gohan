//go:build full

package postgres_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func newRemediationSessions(t *testing.T, name string, clk *advClock, opts ...postgres.SessionLogOption) (*postgres.SessionLog, *postgres.DB) {
	t.Helper()
	dbOpts := []postgres.Option{}
	if clk != nil {
		dbOpts = append(dbOpts, postgres.WithClock(clk.Now))
	}
	db := newDB(t, bindSchema(name), dbOpts...)
	opts = append([]postgres.SessionLogOption{postgres.WithSessionPrincipals(fixedPrincipal(bindPrincipal))}, opts...)
	return postgres.NewSessionLog(db, opts...), db
}

func remediationAppend(t *testing.T, ctx context.Context, s *postgres.SessionLog, id string) {
	t.Helper()
	if _, err := s.Append(ctx, id, 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}); err != nil {
		t.Fatalf("append session %s: %v", id, err)
	}
}

func remediationOwner() types.SessionOwner {
	return types.SessionOwner{Tenant: bindPrincipal.Tenant, Subject: bindPrincipal.Subject}
}

func invalidCursorPrefix(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "list sessions: invalid cursor")
}

func TestSessions(t *testing.T) {
	ctx := context.Background()

	t.Run("stores.pg-sessions-order-and-filters", func(t *testing.T) {
		s, db := newRemediationSessions(t, "sessorder", nil)
		remediationAppend(t, ctx, s, "so-old")
		remediationAppend(t, ctx, s, "so-new")
		remediationAppend(t, ctx, s, "so-arch")
		remediationAppend(t, ctx, s, "so-fork")
		remediationAppend(t, ctx, s, "so-child")
		remediationAppend(t, ctx, s, "so-shadow")
		if _, err := s.Append(ctx, "so-old", 1, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "bump"}}}); err != nil {
			t.Fatalf("bump oldest: %v", err)
		}
		if _, err := db.Pool().Exec(ctx, `UPDATE session_record SET archived = true WHERE session_id = 'so-arch'`); err != nil {
			t.Fatalf("archive: %v", err)
		}
		for id, kind := range map[string]stores.SessionKind{
			"so-fork":   stores.SessionFork,
			"so-child":  stores.SessionChild,
			"so-shadow": stores.SessionShadow,
		} {
			if _, err := db.Pool().Exec(ctx,
				`UPDATE session_record SET kind = $1, forked_from = '{"SessionID":"so-old"}'::jsonb WHERE session_id = $2`,
				int(kind), id); err != nil {
				t.Fatalf("set kind %s: %v", id, err)
			}
		}
		metas, cursor, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{})
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		if cursor != "" {
			t.Fatalf("default page returned a cursor with %d sessions", len(metas))
		}
		var got []string
		for _, m := range metas {
			got = append(got, m.ID)
		}
		if strings.Join(got, ",") != "so-old,so-fork,so-new" {
			t.Fatalf("default page = %v, want primaries and fork only, bumped oldest first", got)
		}
		arch := true
		am, _, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{Archived: &arch})
		if err != nil {
			t.Fatalf("Sessions archived: %v", err)
		}
		if len(am) != 1 || am[0].ID != "so-arch" || !am[0].Archived {
			t.Fatalf("archived listing = %+v, want [so-arch]", am)
		}
		child := []stores.SessionKind{stores.SessionChild}
		cm, _, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{Kinds: child})
		if err != nil {
			t.Fatalf("Sessions child: %v", err)
		}
		if len(cm) != 1 || cm[0].ID != "so-child" {
			t.Fatalf("child listing = %+v, want [so-child]", cm)
		}
		shadow := []stores.SessionKind{stores.SessionShadow}
		sm, _, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{Kinds: shadow})
		if err != nil {
			t.Fatalf("Sessions shadow: %v", err)
		}
		if len(sm) != 1 || sm[0].ID != "so-shadow" {
			t.Fatalf("shadow listing = %+v, want [so-shadow]", sm)
		}
	})

	t.Run("stores.pg-sessions-cursor-stable", func(t *testing.T) {
		s, _ := newRemediationSessions(t, "sessstable", nil)
		remediationAppend(t, ctx, s, "ss-1")
		remediationAppend(t, ctx, s, "ss-2")
		remediationAppend(t, ctx, s, "ss-3")
		page1, cursor, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{Limit: 1})
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		if len(page1) != 1 || page1[0].ID != "ss-3" || cursor == "" {
			t.Fatalf("page 1 = %+v cursor %q, want [ss-3] with a cursor", page1, cursor)
		}
		// Activity changes between pages must not move the boundary.
		if _, err := s.Append(ctx, "ss-1", 1, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "bump"}}}); err != nil {
			t.Fatalf("bump ss-1: %v", err)
		}
		page2, cursor, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{After: cursor, Limit: 1})
		if err != nil {
			t.Fatalf("page 2: %v", err)
		}
		if len(page2) != 1 || page2[0].ID != "ss-2" || cursor == "" {
			t.Fatalf("page 2 = %+v cursor %q, want [ss-2] with a cursor", page2, cursor)
		}
		page3, cursor, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{After: cursor, Limit: 1})
		if err != nil {
			t.Fatalf("page 3: %v", err)
		}
		if len(page3) != 1 || page3[0].ID != "ss-1" || cursor != "" {
			t.Fatalf("page 3 = %+v cursor %q, want [ss-1] without a cursor", page3, cursor)
		}
	})

	t.Run("stores.pg-sessions-snapshot-complete", func(t *testing.T) {
		s, _ := newRemediationSessions(t, "sessbulk", nil)
		const total = 1601
		for i := range total {
			remediationAppend(t, ctx, s, fmt.Sprintf("sb-%04d", i))
		}
		want := make([]string, 0, total)
		for i := total - 1; i >= 0; i-- {
			want = append(want, fmt.Sprintf("sb-%04d", i))
		}
		seen := make(map[string]int, total)
		var order []string
		cursor := ""
		pages := 0
		for {
			var page []stores.SessionMeta
			var err error
			page, cursor, err = s.Sessions(ctx, remediationOwner(), stores.SessionQuery{After: cursor, Limit: 200})
			if err != nil {
				t.Fatalf("page %d: %v", pages+1, err)
			}
			if len(page) == 0 {
				break
			}
			pages++
			if pages == 4 {
				// Unseen sessions change activity mid-listing.
				if _, err := s.Append(ctx, "sb-0005", 1, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "bump"}}}); err != nil {
					t.Fatalf("bump sb-0005: %v", err)
				}
			}
			for _, m := range page {
				seen[m.ID]++
				order = append(order, m.ID)
			}
			if cursor == "" {
				break
			}
		}
		if len(order) != total {
			t.Fatalf("listed %d sessions, want %d", len(order), total)
		}
		for id, n := range seen {
			if n != 1 {
				t.Fatalf("session %s returned %d times, want exactly once", id, n)
			}
		}
		if strings.Join(order, ",") != strings.Join(want, ",") {
			t.Fatal("listing order drifted from the materialized snapshot order")
		}
	})

	t.Run("stores.pg-sessions-cursor-expiration", func(t *testing.T) {
		clk := &advClock{now: time.Now()}
		s, _ := newRemediationSessions(t, "sessexpire", clk)
		remediationAppend(t, ctx, s, "se-1")
		remediationAppend(t, ctx, s, "se-2")
		_, cursor, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{Limit: 1})
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		clk.Advance(stores.LeaseTTL + time.Minute)
		_, _, err = s.Sessions(ctx, remediationOwner(), stores.SessionQuery{After: cursor})
		if !invalidCursorPrefix(err) {
			t.Fatalf("expired continuation err = %v, want invalid cursor", err)
		}
	})

	t.Run("stores.pg-sessions-cursor-pruned", func(t *testing.T) {
		clk := &advClock{now: time.Now()}
		s, db := newRemediationSessions(t, "sessprune", clk)
		remediationAppend(t, ctx, s, "sp-1")
		remediationAppend(t, ctx, s, "sp-2")
		if _, _, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{Limit: 1}); err != nil {
			t.Fatalf("page 1: %v", err)
		}
		clk.Advance(stores.LeaseTTL + time.Minute)
		if _, _, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{Limit: 1}); err != nil {
			t.Fatalf("first page after expiry: %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM session_cursor`); n != 1 {
			t.Fatalf("Sessions left %d cursors, want the fresh one only", n)
		}
		// Purge prunes expired headers inside its own transaction.
		remediationAppend(t, ctx, s, "sp-3")
		if _, _, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{Limit: 1}); err != nil {
			t.Fatalf("page 1: %v", err)
		}
		clk.Advance(stores.LeaseTTL + time.Minute)
		if _, err := s.Purge(ctx, clk.now.Add(time.Hour)); err != nil {
			t.Fatalf("Purge: %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM session_cursor`); n != 0 {
			t.Fatalf("Purge left %d expired cursors, want 0", n)
		}
		if n := count(t, db, `SELECT count(*) FROM session_cursor_item`); n != 0 {
			t.Fatalf("Purge left %d cursor items, want 0", n)
		}
	})

	t.Run("stores.pg-sessions-cursor-owner-scope", func(t *testing.T) {
		s, _ := newRemediationSessions(t, "sessscope", nil)
		remediationAppend(t, ctx, s, "sc-1")
		remediationAppend(t, ctx, s, "sc-2")
		_, cursor, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{Limit: 1})
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		other := types.SessionOwner{Tenant: bindPrincipal.Tenant, Subject: "someone-else"}
		_, _, err = s.Sessions(ctx, other, stores.SessionQuery{After: cursor})
		if !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("foreign owner continuation err = %v, want ErrSessionForbidden", err)
		}
	})

	t.Run("stores.pg-sessions-cursor-membership", func(t *testing.T) {
		s, db := newRemediationSessions(t, "sessmember", nil)
		remediationAppend(t, ctx, s, "sm-1")
		remediationAppend(t, ctx, s, "sm-2")
		remediationAppend(t, ctx, s, "sm-3")
		_, cursor, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{Limit: 2})
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		// New sessions and filter changes after materialization stay out.
		remediationAppend(t, ctx, s, "sm-new")
		if _, err := db.Pool().Exec(ctx, `UPDATE session_record SET archived = true WHERE session_id = 'sm-2'`); err != nil {
			t.Fatalf("archive mid-listing: %v", err)
		}
		page2, cursor, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{After: cursor})
		if err != nil {
			t.Fatalf("page 2: %v", err)
		}
		if len(page2) != 1 || page2[0].ID != "sm-1" {
			t.Fatalf("page 2 = %+v, want frozen membership [sm-1]", page2)
		}
		if cursor != "" {
			t.Fatal("continuation returned a cursor at the end of the snapshot")
		}
	})

	t.Run("stores.pg-sessions-cursor-invalid", func(t *testing.T) {
		s, _ := newRemediationSessions(t, "sessinvalid", nil)
		remediationAppend(t, ctx, s, "si-1")
		remediationAppend(t, ctx, s, "si-2")
		_, cursor, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{Limit: 1})
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		for name, bad := range map[string]string{
			"malformed": "not-a-cursor",
			"missing":   encodeTestCursor("c0000000000000000missing", 0),
		} {
			if _, _, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{After: bad}); !invalidCursorPrefix(err) {
				t.Fatalf("%s cursor err = %v, want invalid cursor", name, err)
			}
		}
		if _, _, err := s.Sessions(ctx, remediationOwner(), stores.SessionQuery{After: cursor, Kinds: []stores.SessionKind{stores.SessionFork}}); !invalidCursorPrefix(err) {
			t.Fatalf("filter-mismatched cursor err = %v, want invalid cursor", err)
		}
	})
}

func encodeTestCursor(cursorID string, ordinal int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%d", cursorID, ordinal)))
}

func TestSessionCascadeRemediation(t *testing.T) {
	ctx := context.Background()

	type cascadeStores struct {
		sessions *postgres.SessionLog
		runs     *postgres.Runs
		outputs  *postgres.Outputs
		cps      *postgres.Checkpoints
	}
	build := func(t *testing.T, name string, failing bool) (*postgres.DB, cascadeStores) {
		t.Helper()
		db := newDB(t, bindSchema(name))
		runs := postgres.NewRuns(db)
		outputs := postgres.NewOutputs(db)
		cps := postgres.NewCheckpoints(db)
		dependents := []stores.SessionDependent{runs, outputs, cps}
		if failing {
			dependents = append(dependents, dependentFunc(func(context.Context, string) error {
				return errors.New("injected cascade failure")
			}))
		}
		sessions := postgres.NewSessionLog(db,
			postgres.WithSessionPrincipals(fixedPrincipal(bindPrincipal)),
			postgres.WithSessionDependents(dependents...))
		return db, cascadeStores{sessions: sessions, runs: runs, outputs: outputs, cps: cps}
	}
	seed := func(t *testing.T, ctx context.Context, cs cascadeStores) {
		t.Helper()
		remediationAppend(t, ctx, cs.sessions, "cs-1")
		if _, err := cs.outputs.Put(ctx, types.RunInfo{SessionID: "cs-1", Principal: bindPrincipal}, []types.Block{types.Text{Text: "bytes"}}); err != nil {
			t.Fatalf("Put output: %v", err)
		}
		if _, err := cs.runs.Start(ctx, stores.Run{RunID: "cs-r1", SessionID: "cs-1"}, stores.LeaseTTL); err != nil {
			t.Fatalf("Start run: %v", err)
		}
		if _, err := cs.cps.Put(ctx, stores.Checkpoint{SessionID: "cs-1", RunID: "cs-r1", Flow: "f"}); err != nil {
			t.Fatalf("Put checkpoint: %v", err)
		}
	}
	survivorCounts := map[string]string{
		"session_record":    `SELECT count(*) FROM session_record`,
		"run_record":        `SELECT count(*) FROM run_record`,
		"output_record":     `SELECT count(*) FROM output_record`,
		"output_claim":      `SELECT count(*) FROM output_claim`,
		"checkpoint_record": `SELECT count(*) FROM checkpoint_record`,
	}
	assertCounts := func(t *testing.T, db *postgres.DB, want map[string]int) {
		t.Helper()
		for name, q := range survivorCounts {
			if got := count(t, db, q); got != want[name] {
				t.Fatalf("%s rows = %d, want %d", name, got, want[name])
			}
		}
	}
	alive := make(map[string]int, len(survivorCounts))
	for name := range survivorCounts {
		alive[name] = 1
	}
	gone := make(map[string]int, len(survivorCounts))
	for name := range survivorCounts {
		gone[name] = 0
	}

	t.Run("stores.pg-cascade-in-one-transaction", func(t *testing.T) {
		db, cs := build(t, "cascadecommit", false)
		seed(t, ctx, cs)
		if err := cs.sessions.Delete(ctx, "cs-1"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		assertCounts(t, db, gone)

		// A failing dependent rolls the whole delete back: the session and
		// every dependent row survive.
		db, cs = build(t, "cascaderollback", true)
		seed(t, ctx, cs)
		if err := cs.sessions.Delete(ctx, "cs-1"); err == nil {
			t.Fatal("Delete with a failing dependent returned nil, want error")
		}
		assertCounts(t, db, alive)

		// Purge rides the same transaction: a failing dependent rolls the
		// purge back and the candidate session survives.
		if _, err := cs.sessions.Purge(ctx, time.Now().Add(time.Hour)); err == nil {
			t.Fatal("Purge with a failing dependent returned nil error, want error")
		}
		assertCounts(t, db, alive)

		// EraseSessions rides the same transaction too.
		if _, err := cs.sessions.EraseSessions(ctx, bindPrincipal.Tenant, bindPrincipal.Subject); err == nil {
			t.Fatal("EraseSessions with a failing dependent returned nil error, want error")
		}
		assertCounts(t, db, alive)
	})
}
