package stores

import (
	"context"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// EntryState is the state of a journal entry. Reserved means the call
// started but its outcome is unknown; Completed means the result is
// recorded and replayable.
type EntryState int

const (
	Reserved EntryState = iota
	Completed
)

// Fingerprint pins a call's identity across retries: a pure function of the
// tool name and the canonical JSON of its arguments.
type Fingerprint string

// Entry is one journal record. Key is the CallID the entry is pinned to; a
// retried call inherits the key of an existing Reserved entry for the same
// fingerprint in the same session.
type Entry struct {
	State       EntryState
	Fingerprint Fingerprint
	Key         string
	Result      types.ToolResult
	At          time.Time
}

// Journal records tool calls so a retried call replays the recorded result
// instead of re-executing. Results are needed for replay during a run's
// lifetime only; the audit record keeps ResultSHA and ResultBytes.
type Journal interface {
	Reserve(ctx context.Context, k types.CallKey, fp Fingerprint) (Entry, bool, error)
	Complete(ctx context.Context, k types.CallKey, res types.ToolResult) error
	ByFingerprint(ctx context.Context, sessionID string, fp Fingerprint) ([]Entry, error)
}

// DefaultJournalTTL is how long an entry stays replayable after it was
// written.
const DefaultJournalTTL = 24 * time.Hour

// MemoryJournalOption configures NewMemoryJournal.
type MemoryJournalOption func(*MemoryJournal)

// WithMemoryJournalClock replaces the store clock. Expiry is judged
// against it, so tests drive time through it.
func WithMemoryJournalClock(now func() time.Time) MemoryJournalOption {
	return func(s *MemoryJournal) { s.now = now }
}

// WithJournalTTL overrides the replay window.
func WithJournalTTL(ttl time.Duration) MemoryJournalOption {
	return func(s *MemoryJournal) { s.ttl = ttl }
}

func NewMemoryJournal(opts ...MemoryJournalOption) *MemoryJournal {
	s := &MemoryJournal{
		now: time.Now,
		ttl: DefaultJournalTTL,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
