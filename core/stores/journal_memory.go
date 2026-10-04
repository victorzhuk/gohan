package stores

import (
	"context"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type journalRecord struct {
	entry    Entry
	key      types.CallKey
	expireAt time.Time
}

// MemoryJournal is the in-memory Journal reference implementation. The
// store clock is authoritative for expiry: a caller with a skewed clock
// cannot extend or cut short a replay window.
type MemoryJournal struct {
	mu    sync.Mutex
	byKey map[types.CallKey]*journalRecord
	now   func() time.Time
	ttl   time.Duration
}

// Reserve atomically claims the call key. For a new key it returns
// created=true with a Reserved entry; otherwise it returns the existing
// entry unchanged and created=false, so a concurrent second caller learns
// the outcome is already pinned. An expired entry is dropped and a fresh
// one created.
func (s *MemoryJournal) Reserve(ctx context.Context, k types.CallKey, fp Fingerprint) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if rec, ok := s.byKey[k]; ok && now.Before(rec.expireAt) {
		return rec.entry, false, nil
	}
	rec := &journalRecord{
		entry: Entry{
			State:       Reserved,
			Fingerprint: fp,
			Key:         k.CallID,
			At:          now,
		},
		key:      k,
		expireAt: now.Add(s.ttl),
	}
	if s.byKey == nil {
		s.byKey = make(map[types.CallKey]*journalRecord)
	}
	s.byKey[k] = rec
	return rec.entry, true, nil
}

// Complete records the result and moves the entry to Completed. The result
// is copied: nothing the caller passed stays aliased. Completing an expired
// or never-reserved entry is a no-op, matching the drop-on-expire reserve.
func (s *MemoryJournal) Complete(ctx context.Context, k types.CallKey, res types.ToolResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.byKey[k]
	if !ok || !s.now().Before(rec.expireAt) {
		return nil
	}
	rec.entry.State = Completed
	rec.entry.Result = copyToolResult(res)
	rec.entry.At = s.now()
	return nil
}

// ByFingerprint lists the session's non-expired entries for a fingerprint.
func (s *MemoryJournal) ByFingerprint(ctx context.Context, sessionID string, fp Fingerprint) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	var out []Entry
	for _, rec := range s.byKey {
		if rec.key.SessionID != sessionID || rec.entry.Fingerprint != fp {
			continue
		}
		if !now.Before(rec.expireAt) {
			continue
		}
		out = append(out, rec.entry)
	}
	return out, nil
}

func copyToolResult(res types.ToolResult) types.ToolResult {
	out := res
	if res.Content != nil {
		out.Content = make([]types.Block, len(res.Content))
		copy(out.Content, res.Content)
	}
	if res.Error != nil {
		e := *res.Error
		out.Error = &e
	}
	return out
}
