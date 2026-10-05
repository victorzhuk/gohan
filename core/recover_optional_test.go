package gohan

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
)

// noPreemptRuns wraps Runs without the optional PreemptedLister, standing
// in for a store built before preemption existed.
type noPreemptRuns struct{ stores.Runs }

// storelessMemoryNotes hides MemoryNotes' optional MemoryStore so the
// session-notes path is proven on a store that only has NotesStore.
type storelessMemoryNotes struct{ inner *stores.MemoryNotes }

func (s *storelessMemoryNotes) Read(ctx context.Context, key stores.NotesKey) (string, int64, error) {
	return s.inner.Read(ctx, key)
}

func (s *storelessMemoryNotes) Write(ctx context.Context, key stores.NotesKey, expectedVersion int64, notes string) (int64, error) {
	return s.inner.Write(ctx, key, expectedVersion, notes)
}

// MemoryNotes implements the optional subject-memory surface.
var _ stores.MemoryStore = (*stores.MemoryNotes)(nil)

func TestOptionalInterfaceFallbacks(t *testing.T) {
	t.Run("stores.optional-preempted-lister-fallback", func(t *testing.T) {
		s := &Stack{}
		s.stores.Runs = noPreemptRuns{}
		if _, ok := s.stores.Runs.(stores.PreemptedLister); ok {
			t.Fatal("noPreemptRuns must not implement PreemptedLister")
		}
		if err := s.recoverPreempted(context.Background(), 1); err != nil {
			t.Fatalf("recoverPreempted without PreemptedLister: %v", err)
		}
	})

	t.Run("working-state.memory-store-optional", func(t *testing.T) {
		ctx := context.Background()
		key := stores.NotesKey{Scope: stores.ScopeSession, Tenant: "t", Subject: "u1", Session: "s1"}

		store := &storelessMemoryNotes{inner: &stores.MemoryNotes{}}
		var notesStore stores.NotesStore = store
		if _, ok := notesStore.(stores.MemoryStore); ok {
			t.Fatal("storelessMemoryNotes must not implement MemoryStore")
		}
		if _, err := store.Write(ctx, key, 0, "progress: booking done"); err != nil {
			t.Fatalf("write session notes: %v", err)
		}
		got, version, err := store.Read(ctx, key)
		if err != nil || got != "progress: booking done" || version != 1 {
			t.Fatalf("session notes without MemoryStore: %q v%d err %v", got, version, err)
		}
	})
}
