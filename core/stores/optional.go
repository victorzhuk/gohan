package stores

import (
	"context"
	"encoding/json"

	"github.com/victorzhuk/gohan/core/types"
)

// MemoryEntry is one subject-memory record addressed by a NotesKey.
type MemoryEntry struct {
	ID      string
	Value   json.RawMessage
	Origins []types.Origin
	RunID   string
	Version int64
}

// MemoryStore is the optional subject-memory surface on a NotesStore.
// Session notes work on every NotesStore; subject memory needs this
// interface, and registering the memory tool on a store without it fails
// with types.ErrMemoryStoreRequired.
type MemoryStore interface {
	Entries(ctx context.Context, key NotesKey) ([]MemoryEntry, error)
	Put(ctx context.Context, key NotesKey, e MemoryEntry) error
	Forget(ctx context.Context, key NotesKey, ids ...string) error
}

// memoryRecord is the stored form of one MemoryEntry; the version is
// assigned on Put, mirroring the notes record's compare-and-swap version.
type memoryRecord struct {
	entry   MemoryEntry
	version int64
}

// Entries lists the memory entries stored under the key.
func (s *MemoryNotes) Entries(ctx context.Context, key NotesKey) ([]MemoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]MemoryEntry, 0, len(s.memory[key]))
	for _, rec := range s.memory[key] {
		e := rec.entry
		e.Version = rec.version
		out = append(out, e)
	}
	return out, nil
}

// Put stores one memory entry under the key and assigns its version.
func (s *MemoryNotes) Put(ctx context.Context, key NotesKey, e MemoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.memory == nil {
		s.memory = make(map[NotesKey][]memoryRecord)
	}
	recs := s.memory[key]
	version := int64(1)
	if len(recs) > 0 {
		version = recs[len(recs)-1].version + 1
	}
	e.Version = version
	s.memory[key] = append(recs, memoryRecord{entry: e, version: version})
	return nil
}

// Forget removes the named entries; the survivors keep their versions.
func (s *MemoryNotes) Forget(ctx context.Context, key NotesKey, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	recs := s.memory[key]
	kept := recs[:0]
	for _, rec := range recs {
		if !drop[rec.entry.ID] {
			kept = append(kept, rec)
		}
	}
	s.memory[key] = kept
	return nil
}
