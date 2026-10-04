package stores

import (
	"context"
	"iter"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// Event pairs a payload with the meta the store assigned at Append time.
// The payload types carry no run identity of their own, so the log is where
// payload and meta become one record; Seq is the store's, not the caller's.
type Event struct {
	Payload types.Event
	Meta    types.EventMeta
}

// EventLog persists a run's event stream. A run emits N events and the log
// hands back Seq values exactly 1..N in delivery order; the single-writer
// guarantee comes from the Runs lease, so Append needs no per-run lock
// against other writers. Read delivers persisted events in order, then live
// ones, with no gap or duplicate.
type EventLog interface {
	Append(ctx context.Context, runID string, e Event) error
	Read(ctx context.Context, runID string, afterSeq int64) iter.Seq2[Event, error]
	Expire(ctx context.Context, olderThan time.Time) error
}

// DefaultEventLogCapacity is how many events a MemoryEventLog keeps per run
// before the oldest are evicted.
const DefaultEventLogCapacity = 1024

// MemoryEventLogOption configures NewMemoryEventLog.
type MemoryEventLogOption func(*MemoryEventLog)

// WithMemoryEventLogClock replaces the store clock. Persisted instants come
// from it, so tests drive expiry through it.
func WithMemoryEventLogClock(now func() time.Time) MemoryEventLogOption {
	return func(s *MemoryEventLog) { s.now = now }
}

// WithMemoryEventLogCapacity overrides the per-run ring size.
func WithMemoryEventLogCapacity(n int) MemoryEventLogOption {
	return func(s *MemoryEventLog) { s.capacity = n }
}

// MemoryEventLog is the in-memory EventLog reference implementation. The
// store clock is authoritative for the instants it persists.
type MemoryEventLog struct {
	mu       sync.Mutex
	byRun    map[string][]Event
	next     map[string]int64
	capacity int
	now      func() time.Time
}

func NewMemoryEventLog(opts ...MemoryEventLogOption) *MemoryEventLog {
	s := &MemoryEventLog{
		byRun:    map[string][]Event{},
		next:     map[string]int64{},
		capacity: DefaultEventLogCapacity,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Append assigns the next Seq for the run and records the event. An unset
// Meta.Time is stamped with the store clock; Meta.RunID is forced to the
// partition key so the two cannot drift.
func (s *MemoryEventLog) Append(ctx context.Context, runID string, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	seq := s.next[runID] + 1
	s.next[runID] = seq
	e.Meta.Seq = seq
	e.Meta.RunID = runID
	if e.Meta.Time.IsZero() {
		e.Meta.Time = s.now()
	}
	s.byRun[runID] = append(s.byRun[runID], e)
	if overflow := len(s.byRun[runID]) - s.capacity; overflow > 0 {
		s.byRun[runID] = append([]Event(nil), s.byRun[runID][overflow:]...)
	}
	return nil
}

// Read yields the run's retained events with Seq greater than afterSeq, in
// Seq order. Breaking early stops the scan; the error tuple is reserved for
// a failure this implementation cannot produce, so the sequence simply ends.
func (s *MemoryEventLog) Read(ctx context.Context, runID string, afterSeq int64) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		s.mu.Lock()
		events := s.byRun[runID]
		out := make([]Event, 0, len(events))
		for _, e := range events {
			if e.Meta.Seq > afterSeq {
				out = append(out, e)
			}
		}
		s.mu.Unlock()

		for _, e := range out {
			if !yield(e, nil) {
				return
			}
		}
	}
}

// Expire drops events whose recorded instant is before olderThan, across
// every run.
func (s *MemoryEventLog) Expire(ctx context.Context, olderThan time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for runID, events := range s.byRun {
		kept := events[:0]
		for _, e := range events {
			if e.Meta.Time.Before(olderThan) {
				continue
			}
			kept = append(kept, e)
		}
		if len(kept) == 0 {
			delete(s.byRun, runID)
			delete(s.next, runID)
			continue
		}
		s.byRun[runID] = kept
	}
	return nil
}
