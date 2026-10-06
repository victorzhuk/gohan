package stores

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// MemoryEntry is one subject-memory record addressed by a NotesKey.
type MemoryEntry struct {
	ID       string
	Value    json.RawMessage
	Origins  []types.Origin
	RunID    string
	StoredAt time.Time
	Version  int64
}

// DefaultMemoryMaxEntries is how many entries a key keeps when the policy
// sets no bound.
const DefaultMemoryMaxEntries = 64

// DefaultMemoryTTL is how long an entry lives when the policy sets no TTL.
const DefaultMemoryTTL = 90 * 24 * time.Hour

// MemoryPolicy bounds and shapes subject memory for one key space. An empty
// Schema admits any value; zero bounds fall back to the defaults.
type MemoryPolicy struct {
	Schema     json.RawMessage
	MaxEntries int
	TTL        time.Duration
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

// MemoryNotesOption configures the optional subject-memory surface of
// MemoryNotes.
type MemoryNotesOption func(*MemoryNotes)

// NewMemoryNotes returns a MemoryNotes with subject-memory options applied.
func NewMemoryNotes(opts ...MemoryNotesOption) *MemoryNotes {
	s := &MemoryNotes{}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithMemoryNotesClock replaces the store clock for subject memory. The
// stamp and the TTL are judged against it, so tests drive time through it.
func WithMemoryNotesClock(now func() time.Time) MemoryNotesOption {
	return func(s *MemoryNotes) { s.memNow = now }
}

// WithMemoryNotesPolicy shapes and bounds subject memory. An empty Schema
// admits any value.
func WithMemoryNotesPolicy(policy MemoryPolicy) MemoryNotesOption {
	return func(s *MemoryNotes) { s.memPolicy = policy }
}

// Put stores one memory entry under the key and assigns its version. The
// value must validate against the policy Schema, the stamp comes from the
// store clock, and expired entries plus the oldest past MaxEntries leave
// before the insert. Survivors keep their versions.
func (s *MemoryNotes) Put(ctx context.Context, key NotesKey, e MemoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy := s.memPolicy
	if policy.MaxEntries <= 0 {
		policy.MaxEntries = DefaultMemoryMaxEntries
	}
	if policy.TTL <= 0 {
		policy.TTL = DefaultMemoryTTL
	}
	if len(policy.Schema) > 0 {
		if err := validateMemoryValue(policy.Schema, e.Value); err != nil {
			return fmt.Errorf("put memory %v: %w", key, err)
		}
	}
	if s.memory == nil {
		s.memory = make(map[NotesKey][]memoryRecord)
	}
	now := s.memoryClock()
	recs := s.memory[key]
	kept := recs[:0:0]
	for _, rec := range recs {
		expired := rec.entry.StoredAt.IsZero() || now.Sub(rec.entry.StoredAt) >= policy.TTL
		if expired {
			countMemoryEviction(ctx, key)
			continue
		}
		kept = append(kept, rec)
	}
	for len(kept) >= policy.MaxEntries {
		oldest := 0
		for i, rec := range kept {
			if rec.entry.StoredAt.Before(kept[oldest].entry.StoredAt) {
				oldest = i
			}
		}
		kept = append(kept[:oldest], kept[oldest+1:]...)
		countMemoryEviction(ctx, key)
	}
	version := int64(1)
	if len(kept) > 0 {
		version = kept[len(kept)-1].version + 1
	}
	e.StoredAt = now
	e.Version = version
	s.memory[key] = append(kept, memoryRecord{entry: e, version: version})
	return nil
}

func (s *MemoryNotes) memoryClock() time.Time {
	if s.memNow != nil {
		return s.memNow()
	}
	return time.Now()
}

func countMemoryEviction(ctx context.Context, key NotesKey) {
	if tel := types.TelemetryFrom(ctx); tel != nil {
		tel.Count(ctx, "gohan.memory.evicted", 1, types.String("tenant", key.Tenant))
	}
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
