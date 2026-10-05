package stores

import (
	"context"
	"errors"
	"fmt"
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

// ErrStaleCursor is the error a bounded log reports for a Read cursor older
// than what it still retains: the events between the cursor and the
// retained window are gone, so serving the window would hand the client a
// gap. The client reattaches from the oldest retained sequence instead.
var ErrStaleCursor = errors.New("gohan: event log no longer retains events before the requested sequence")

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
	rings    map[string]*eventRing
	next     map[string]int64
	capacity int
	now      func() time.Time
}

// eventRing holds one run's retained window in a fixed slice: head is the
// oldest slot, count the retained events. Saturated Appends overwrite in
// place, so the slice is allocated once per run.
type eventRing struct {
	slots []Event
	head  int
	count int
	// first is the Seq of slots[head]; zero while the ring holds nothing.
	// Read needs it to tell a stale cursor from a served window: once
	// events are evicted, a cursor below first-1 would silently skip them.
	first int64
}

func NewMemoryEventLog(opts ...MemoryEventLogOption) *MemoryEventLog {
	s := &MemoryEventLog{
		rings:    map[string]*eventRing{},
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
	if s.capacity <= 0 {
		return nil
	}
	e.Meta.Seq = seq
	e.Meta.RunID = runID
	if e.Meta.Time.IsZero() {
		e.Meta.Time = s.now()
	}
	ring := s.rings[runID]
	if ring == nil {
		ring = &eventRing{slots: make([]Event, s.capacity)}
		s.rings[runID] = ring
	}
	if ring.count == 0 {
		ring.first = seq
	}
	if ring.count < s.capacity {
		ring.slots[(ring.head+ring.count)%s.capacity] = e
		ring.count++
		return nil
	}
	ring.slots[ring.head] = e
	ring.head = (ring.head + 1) % s.capacity
	ring.first = ring.slots[ring.head].Meta.Seq
	return nil
}

// Read yields the run's retained events with Seq greater than afterSeq, in
// Seq order. A cursor older than the retained window is refused with an
// error wrapping ErrStaleCursor: the events between the cursor and the
// window were evicted, and serving the window would hand the client a gap
// it cannot see. Breaking early stops the scan.
func (s *MemoryEventLog) Read(ctx context.Context, runID string, afterSeq int64) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		s.mu.Lock()
		var first int64
		ring := s.rings[runID]
		count := 0
		switch {
		case ring != nil && ring.count > 0:
			first, count = ring.first, ring.count
		case s.next[runID] > 0:
			// Every recorded event was expired; the next sequence is the
			// first one a fresh cursor may resume from.
			first = s.next[runID] + 1
		}
		if first > 0 && afterSeq+1 < first {
			err := fmt.Errorf("gohan: oldest retained seq is %d: %w", first, ErrStaleCursor)
			s.mu.Unlock()
			var zero Event
			yield(zero, err)
			return
		}
		out := make([]Event, 0, count)
		for i := range count {
			e := ring.slots[(ring.head+i)%s.capacity]
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
// every run. It clears the payload references it removes and compacts the
// ring in place; a run's sequence counter survives so later appends for the
// same run keep increasing.
func (s *MemoryEventLog) Expire(ctx context.Context, olderThan time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for runID, ring := range s.rings {
		kept := 0
		for i := range ring.count {
			idx := (ring.head + i) % s.capacity
			e := ring.slots[idx]
			if e.Meta.Time.Before(olderThan) {
				ring.slots[idx] = Event{}
				continue
			}
			if idx != kept {
				ring.slots[kept] = e
				ring.slots[idx] = Event{}
			}
			kept++
		}
		ring.head = 0
		ring.count = kept
		if kept == 0 {
			delete(s.rings, runID)
			continue
		}
		ring.first = ring.slots[0].Meta.Seq
	}
	return nil
}
