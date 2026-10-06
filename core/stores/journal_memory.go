package stores

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type journalRecord struct {
	entry    Entry
	raw      []byte
	key      types.CallKey
	expireAt time.Time
}

// MemoryJournal is the in-memory Journal reference implementation. The
// store clock is authoritative for expiry: a caller with a skewed clock
// cannot extend or cut short a replay window.
type MemoryJournal struct {
	mu      sync.Mutex
	byKey   map[types.CallKey]*journalRecord
	now     func() time.Time
	ttl     time.Duration
	schemas *SchemaRegistry
	region  string
}

func (s *MemoryJournal) Region() string { return s.region }

// decodeEntry reads the record's envelope through the registry. The stored
// bytes are never rewritten.
func (s *MemoryJournal) decodeEntry(ctx context.Context, rec *journalRecord) (Entry, error) {
	var env RecordEnvelope
	if err := json.Unmarshal(rec.raw, &env); err != nil {
		return Entry{}, fmt.Errorf("decode Entry envelope: %w", err)
	}
	return DecodeRecord[Entry](ctx, s.schemas, env)
}

// Reserve atomically claims the call key. For a new key it returns
// created=true with a Reserved entry; otherwise it returns the existing
// entry unchanged and created=false, so a concurrent second caller learns
// the outcome is already pinned. A key hit whose fingerprint differs from
// the stored entry's returns ErrJournalFingerprintMismatch. An expired
// entry is dropped and a fresh one created.
func (s *MemoryJournal) Reserve(ctx context.Context, k types.CallKey, tool string, fp Fingerprint, args json.RawMessage) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if rec, ok := s.byKey[k]; ok && now.Before(rec.expireAt) {
		entry, err := s.decodeEntry(ctx, rec)
		if err != nil {
			return Entry{}, false, err
		}
		if entry.Fingerprint != fp {
			return Entry{}, false, ErrJournalFingerprintMismatch
		}
		return entry, false, nil
	}
	entry := Entry{
		State:       Reserved,
		Fingerprint: fp,
		Tool:        tool,
		Key:         k.CallID,
		Args:        append(json.RawMessage(nil), args...),
		At:          now,
	}
	raw, err := encodeEnvelope("Entry", entry)
	if err != nil {
		return Entry{}, false, err
	}
	rec := &journalRecord{
		entry:    entry,
		raw:      raw,
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
// or never-reserved entry returns ErrJournalCompleteMissed: silently
// dropping the result would let a run report clean without a replayable
// outcome.
func (s *MemoryJournal) Complete(ctx context.Context, k types.CallKey, res types.ToolResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.byKey[k]
	if !ok || !s.now().Before(rec.expireAt) {
		return ErrJournalCompleteMissed
	}
	rec.entry.State = Completed
	rec.entry.Result = copyToolResult(res)
	rec.entry.At = s.now()
	raw, err := encodeEnvelope("Entry", rec.entry)
	if err != nil {
		return err
	}
	rec.raw = raw
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
		e, err := s.decodeEntry(ctx, rec)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// Unknown lists non-expired entries whose outcome is unknown.
func (s *MemoryJournal) Unknown(ctx context.Context, sessionID string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var out []Entry
	for _, rec := range s.byKey {
		if rec.key.SessionID != sessionID || !now.Before(rec.expireAt) || rec.entry.Result.Outcome != types.Unknown {
			continue
		}
		e, err := s.decodeEntry(ctx, rec)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
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
