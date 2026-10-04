// Package storetest holds conformance suites for the store ports. Each
// suite takes the implementation through an injected factory and stays
// independent of any concrete store package.
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// SessionLogStore is the port under test. It mirrors the consumer-owned
// interface the stores declare; implementations adapt to it in their own
// binding tests.
type SessionLogStore interface {
	Load(ctx context.Context, sessionID string) (SessionHistory, error)
	Append(ctx context.Context, sessionID string, expectedVersion int64, msgs ...types.Message) (int64, error)
	Purge(ctx context.Context, olderThan time.Time) (int, error)
	Delete(ctx context.Context, sessionID string) error
}

// SessionHistory carries the fields the port contract speaks about.
type SessionHistory struct {
	Messages []types.Message
	Version  int64
}

// SessionLogFactory builds a fresh, empty SessionLog per test.
type SessionLogFactory func(ctx context.Context) (SessionLogStore, error)

// SessionLog runs the port conformance suite against one implementation.
func SessionLog(t *testing.T, factory SessionLogFactory) {
	t.Helper()
	t.Run("append_and_load_roundtrip", func(t *testing.T) {
		s := open(t, factory)
		v, err := s.Append(ctx(), "s1", 0, msg(types.RoleUser), msg(types.RoleAssistant))
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if v != 2 {
			t.Fatalf("version after appending 2 messages = %d, want 2", v)
		}
		h, err := s.Load(ctx(), "s1")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if h.Version != 2 {
			t.Fatalf("loaded version = %d, want 2", h.Version)
		}
		if len(h.Messages) != 2 {
			t.Fatalf("loaded %d messages, want 2", len(h.Messages))
		}
		for i, want := range []types.Role{types.RoleUser, types.RoleAssistant} {
			if h.Messages[i].Role != want {
				t.Fatalf("message %d role = %s, want %s", i, h.Messages[i].Role, want)
			}
		}
	})

	t.Run("append_assigns_stable_ids", func(t *testing.T) {
		s := open(t, factory)
		if _, err := s.Append(ctx(), "s1", 0, msg(types.RoleUser)); err != nil {
			t.Fatalf("append: %v", err)
		}
		h1, err := s.Load(ctx(), "s1")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		ids := map[string]bool{}
		for i, m := range h1.Messages {
			if m.ID == "" {
				t.Fatalf("message %d has an empty id", i)
			}
			if ids[m.ID] {
				t.Fatalf("duplicate message id %s", m.ID)
			}
			ids[m.ID] = true
		}
		h2, err := s.Load(ctx(), "s1")
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		for i, m := range h1.Messages {
			if h2.Messages[i].ID != m.ID {
				t.Fatalf("message %d id changed across loads: %s then %s", i, m.ID, h2.Messages[i].ID)
			}
		}
	})

	t.Run("append_conflict", func(t *testing.T) {
		s := open(t, factory)
		// stores.append-conflict: two writers hold the same expected
		// version; exactly one append succeeds.
		if _, err := s.Append(ctx(), "s1", 0, msg(types.RoleUser)); err != nil {
			t.Fatalf("first append: %v", err)
		}
		v2, err := s.Append(ctx(), "s1", 1, msg(types.RoleAssistant))
		if err != nil {
			t.Fatalf("append at current version: %v", err)
		}
		if _, err := s.Append(ctx(), "s1", 1, msg(types.RoleAssistant)); !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("stale append error = %v, want ErrVersionConflict", err)
		}
		h, err := s.Load(ctx(), "s1")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if h.Version != v2 || len(h.Messages) != int(v2) {
			t.Fatalf("loaded version = %d with %d messages, want %d", h.Version, len(h.Messages), v2)
		}
		if _, err := s.Append(ctx(), "s1", h.Version, msg(types.RoleUser)); err != nil {
			t.Fatalf("append after reload at current version: %v", err)
		}
	})

	t.Run("append_to_new_session_needs_version_zero", func(t *testing.T) {
		s := open(t, factory)
		if _, err := s.Append(ctx(), "new", 1, msg(types.RoleUser)); !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("append to new session at version 1 = %v, want ErrVersionConflict", err)
		}
	})

	t.Run("load_missing_session_fails", func(t *testing.T) {
		s := open(t, factory)
		if _, err := s.Load(ctx(), "missing"); err == nil {
			t.Fatal("load of a missing session succeeded, want an error")
		}
	})

	t.Run("delete_removes_session", func(t *testing.T) {
		s := open(t, factory)
		if _, err := s.Append(ctx(), "s1", 0, msg(types.RoleUser)); err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := s.Delete(ctx(), "s1"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := s.Load(ctx(), "s1"); err == nil {
			t.Fatal("load after delete succeeded, want an error")
		}
	})

	t.Run("purge_removes_only_sessions_older_than_cut", func(t *testing.T) {
		s := open(t, factory)
		if _, err := s.Append(ctx(), "old", 0, msg(types.RoleUser)); err != nil {
			t.Fatalf("append old: %v", err)
		}
		if _, err := s.Append(ctx(), "new", 0, msg(types.RoleUser)); err != nil {
			t.Fatalf("append new: %v", err)
		}
		n, err := s.Purge(ctx(), time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("purge: %v", err)
		}
		if n != 2 {
			t.Fatalf("purge count = %d, want 2", n)
		}
		for _, id := range []string{"old", "new"} {
			if _, err := s.Load(ctx(), id); err == nil {
				t.Fatalf("load of purged session %s succeeded, want an error", id)
			}
		}
		n, err = s.Purge(ctx(), time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("purge with past cut: %v", err)
		}
		if n != 0 {
			t.Fatalf("purge count with no candidates = %d, want 0", n)
		}
		if _, err := s.Append(ctx(), "fresh", 0, msg(types.RoleUser)); err != nil {
			t.Fatalf("append after purge: %v", err)
		}
		n, err = s.Purge(ctx(), time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("purge: %v", err)
		}
		if n != 0 {
			t.Fatalf("purge removed %d sessions newer than the cut, want 0", n)
		}
		if _, err := s.Load(ctx(), "fresh"); err != nil {
			t.Fatalf("load after non-purge: %v", err)
		}
	})
}

func open(t *testing.T, factory SessionLogFactory) SessionLogStore {
	t.Helper()
	s, err := factory(context.Background())
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	return s
}

func ctx() context.Context { return context.Background() }

func msg(r types.Role) types.Message {
	return types.Message{Role: r, Blocks: []types.Block{types.Text{Text: "hi"}}}
}
