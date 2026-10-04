package stores

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestNotesStore(t *testing.T) {
	ctx := context.Background()

	t.Run("working-state.notes-versioned", func(t *testing.T) {
		store := &MemoryNotes{}
		key := NotesKey{Scope: ScopeSession, Tenant: "t1", Subject: "u1", Session: "s1"}

		if _, err := store.Write(ctx, key, 1, "first"); !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("create with version 1: %v", err)
		}
		v1, err := store.Write(ctx, key, 0, "first")
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		const writers = 2
		results := make([]error, writers)
		versions := make([]int64, writers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				versions[i], results[i] = store.Write(ctx, key, v1, "second")
			}()
		}
		close(start)
		wg.Wait()

		succeeded := 0
		conflicted := 0
		for i := range writers {
			switch {
			case results[i] == nil:
				succeeded++
				if versions[i] != v1+1 {
					t.Fatalf("winner version: got %d, want %d", versions[i], v1+1)
				}
			case errors.Is(results[i], types.ErrVersionConflict):
				conflicted++
			default:
				t.Fatalf("writer %d: unexpected error %v", i, results[i])
			}
		}
		if succeeded != 1 || conflicted != 1 {
			t.Fatalf("concurrent writes: %d succeeded, %d conflicted, want 1 and 1", succeeded, conflicted)
		}

		notes, version, err := store.Read(ctx, key)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if notes != "second" || version != v1+1 {
			t.Fatalf("read after race: got (%q, %d), want (second, %d)", notes, version, v1+1)
		}
		if _, err := store.Write(ctx, key, version, "third"); err != nil {
			t.Fatalf("re-write after re-read: %v", err)
		}
	})

	t.Run("scopes are isolated", func(t *testing.T) {
		store := &MemoryNotes{}
		session := NotesKey{Scope: ScopeSession, Tenant: "t1", Subject: "u1", Session: "s1"}
		subject := NotesKey{Scope: ScopeSubject, Tenant: "t1", Subject: "u1"}
		other := NotesKey{Scope: ScopeSubject, Tenant: "t1", Subject: "u2"}

		if _, err := store.Write(ctx, session, 0, "session notes"); err != nil {
			t.Fatalf("write session notes: %v", err)
		}
		notes, version, err := store.Read(ctx, subject)
		if err != nil || notes != "" || version != 0 {
			t.Fatalf("read subject scope: got (%q, %d, %v), want empty at 0", notes, version, err)
		}
		if _, err := store.Write(ctx, other, 0, "other subject"); err != nil {
			t.Fatalf("write other subject: %v", err)
		}
		if notes, _, _ = store.Read(ctx, subject); notes != "" {
			t.Fatalf("read u2 notes through u1 key: got %q", notes)
		}
	})
}
