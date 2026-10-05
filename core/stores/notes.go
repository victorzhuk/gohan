package stores

import (
	"context"
	"fmt"
	"sync"

	"github.com/victorzhuk/gohan/core/types"
)

// NotesScope selects the notes tier. ScopeSession notes live for one
// session; ScopeSubject notes are recalled across a principal's sessions.
type NotesScope int

const (
	ScopeSession NotesScope = iota
	ScopeSubject
)

// NotesKey addresses one notes record. The harness builds it from the
// session owner, so callers cannot construct a key for another tenant,
// subject or session.
type NotesKey struct {
	Scope   NotesScope
	Tenant  string
	Subject string
	Session string
}

// NotesStore is the versioned notes primitive. Read returns the current
// notes and their version; a missing record reads as empty at version 0.
// Write is a compare-and-swap: expectedVersion 0 creates the record, any
// other value must match the stored version.
type NotesStore interface {
	Read(ctx context.Context, key NotesKey) (string, int64, error)
	Write(ctx context.Context, key NotesKey, expectedVersion int64, notes string) (int64, error)
}

// MemoryNotes is the in-memory NotesStore reference implementation.
type MemoryNotes struct {
	mu    sync.Mutex
	byKey map[NotesKey]notesRecord
	// memory backs the optional MemoryStore surface; session notes and
	// subject memory share the key space but not the records.
	memory map[NotesKey][]memoryRecord
}

type notesRecord struct {
	notes   string
	version int64
}

// Read returns the notes and their version for the key.
func (s *MemoryNotes) Read(ctx context.Context, key NotesKey) (string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byKey[key]
	if !ok {
		return "", 0, nil
	}
	return rec.notes, rec.version, nil
}

// Write swaps the notes when expectedVersion still matches. Two writers
// that carry the same expected version race on the mutex: exactly one
// matches and the other fails with ErrVersionConflict, which sends the
// loser back to Read.
func (s *MemoryNotes) Write(ctx context.Context, key NotesKey, expectedVersion int64, notes string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.byKey[key]
	if ok && rec.version != expectedVersion {
		return 0, fmt.Errorf("write notes %v at version %d: %w", key, expectedVersion, types.ErrVersionConflict)
	}
	if !ok && expectedVersion != 0 {
		return 0, fmt.Errorf("write notes %v at version %d: %w", key, expectedVersion, types.ErrVersionConflict)
	}
	if s.byKey == nil {
		s.byKey = make(map[NotesKey]notesRecord)
	}
	version := expectedVersion + 1
	s.byKey[key] = notesRecord{notes: notes, version: version}
	return version, nil
}
