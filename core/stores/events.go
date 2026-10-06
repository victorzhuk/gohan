package stores

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"strings"
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

// WithMemoryEventLogSchemas injects the schema registry record reads decode
// through.
func WithMemoryEventLogSchemas(reg *SchemaRegistry) MemoryEventLogOption {
	return func(s *MemoryEventLog) { s.schemas = reg }
}

// MemoryEventLog is the in-memory EventLog reference implementation. The
// store clock is authoritative for the instants it persists.
type MemoryEventLog struct {
	mu       sync.Mutex
	rings    map[string]*eventRing
	next     map[string]int64
	capacity int
	now      func() time.Time
	schemas  *SchemaRegistry
}

// eventRing holds one run's retained window in a fixed slice: head is the
// oldest slot, count the retained events. Saturated Appends overwrite in
// place, so the slice is allocated once per run.
// eventSlot is one persisted event: the decoded value the log hands back
// and the envelope bytes the public read decodes through.
type eventSlot struct {
	event Event
	raw   []byte
}

type eventRing struct {
	slots []eventSlot
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
		schemas:  NewSchemaRegistry(),
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
	raw, err := encodeEventRecord(e)
	if err != nil {
		return err
	}
	slot := eventSlot{event: e, raw: raw}
	ring := s.rings[runID]
	if ring == nil {
		ring = &eventRing{slots: make([]eventSlot, s.capacity)}
		s.rings[runID] = ring
	}
	if ring.count == 0 {
		ring.first = seq
	}
	if ring.count < s.capacity {
		ring.slots[(ring.head+ring.count)%s.capacity] = slot
		ring.count++
		return nil
	}
	ring.slots[ring.head] = slot
	ring.head = (ring.head + 1) % s.capacity
	ring.first = ring.slots[ring.head].event.Meta.Seq
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
		out := make([]eventSlot, 0, count)
		for i := range count {
			slot := ring.slots[(ring.head+i)%s.capacity]
			if slot.event.Meta.Seq > afterSeq {
				out = append(out, slot)
			}
		}
		s.mu.Unlock()

		for _, slot := range out {
			e, err := decodeEventRecord(ctx, s.schemas, slot.raw)
			if err != nil {
				var zero Event
				yield(zero, err)
				return
			}
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
		write := ring.head
		for i := range ring.count {
			idx := (ring.head + i) % s.capacity
			slot := ring.slots[idx]
			if slot.event.Meta.Time.Before(olderThan) {
				ring.slots[idx] = eventSlot{}
				continue
			}
			if idx != write {
				ring.slots[write] = slot
				ring.slots[idx] = eventSlot{}
			}
			kept++
			write = (write + 1) % s.capacity
		}
		for i := kept; i < ring.count; i++ {
			ring.slots[(ring.head+i)%s.capacity] = eventSlot{}
		}
		ring.count = kept
		if kept == 0 {
			delete(s.rings, runID)
			continue
		}
		ring.first = ring.slots[ring.head].event.Meta.Seq
	}
	return nil
}

// eventRecord is the Event stored shape: the payload kind selects the
// concrete payload type on decode, since payloads carry no type of their
// own on the wire.
type eventRecord struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
	Meta    types.EventMeta `json:"meta"`
}

func encodeEventRecord(e Event) ([]byte, error) {
	kind, err := eventKind(e.Payload)
	if err != nil {
		return nil, err
	}
	payload, err := encodeEventPayload(e.Payload)
	if err != nil {
		return nil, err
	}
	return encodeEnvelope("Event", eventRecord{Kind: kind, Payload: payload, Meta: e.Meta})
}

// encodeEventPayload serializes the wire-safe projection of a payload kind.
// Some payload fields carry function values the wire cannot represent, so
// the projection stores the summary a reader reconstructs the event from.
func encodeEventPayload(p types.Event) (json.RawMessage, error) {
	switch v := p.(type) {
	case types.ToolFinished:
		r := v.Result
		blocks := make([]types.Block, 0, len(r.Content))
		for _, b := range r.Content {
			if raw, ok := b.(types.Raw); ok {
				if _, err := json.Marshal(raw.Value); err != nil {
					continue
				}
			}
			blocks = append(blocks, b)
		}
		r.Content = blocks
		return json.Marshal(types.ToolFinished{Turn: v.Turn, Result: r, Replayed: v.Replayed})
	case types.Suspended:
		return json.Marshal(types.Suspended{Token: v.Token, Reason: v.Reason, WakeAt: v.WakeAt})
	}
	return json.Marshal(p)
}

func decodeEventRecord(ctx context.Context, reg *SchemaRegistry, raw []byte) (Event, error) {
	var env RecordEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Event{}, fmt.Errorf("decode Event envelope: %w", err)
	}
	rec, err := DecodeRecord[eventRecord](ctx, reg, env)
	if err != nil {
		return Event{}, err
	}
	payload, err := eventPayload(rec.Kind, rec.Payload)
	if err != nil {
		return Event{}, err
	}
	return Event{Payload: payload, Meta: rec.Meta}, nil
}

func eventKind(p types.Event) (string, error) {
	switch p.(type) {
	case types.TextDelta:
		return "text_delta", nil
	case types.ReasoningDelta:
		return "reasoning_delta", nil
	case types.AssistantMessage:
		return "assistant_message", nil
	case types.ToolArgsDelta:
		return "tool_args_delta", nil
	case types.ResultDelta:
		return "result_delta", nil
	case types.ToolStarted:
		return "tool_started", nil
	case types.ToolFinished:
		return "tool_finished", nil
	case types.Suspended:
		return "suspended", nil
	case types.GuardBlocked:
		return "guard_blocked", nil
	case types.LimitWarning:
		return "limit_warning", nil
	case types.Compacted:
		return "compacted", nil
	case types.StateChanged:
		return "state_changed", nil
	case types.FeedbackRecorded:
		return "feedback_recorded", nil
	case types.SteerApplied:
		return "steer_applied", nil
	case types.Done:
		return "done", nil
	case types.TerminalError:
		return "terminal_error", nil
	}
	return "", fmt.Errorf("encode Event: unknown payload %T", p)
}

func eventPayload(kind string, raw json.RawMessage) (types.Event, error) {
	switch kind {
	case "text_delta":
		var e types.TextDelta
		err := json.Unmarshal(raw, &e)
		return e, err
	case "reasoning_delta":
		var e types.ReasoningDelta
		err := json.Unmarshal(raw, &e)
		return e, err
	case "assistant_message":
		var e types.AssistantMessage
		err := json.Unmarshal(raw, &e)
		return e, err
	case "tool_args_delta":
		var e types.ToolArgsDelta
		err := json.Unmarshal(raw, &e)
		return e, err
	case "result_delta":
		var e types.ResultDelta
		err := json.Unmarshal(raw, &e)
		return e, err
	case "tool_started":
		var e types.ToolStarted
		err := json.Unmarshal(raw, &e)
		return e, err
	case "tool_finished":
		var e types.ToolFinished
		err := json.Unmarshal(raw, &e)
		return e, err
	case "suspended":
		var e types.Suspended
		err := json.Unmarshal(raw, &e)
		return e, err
	case "guard_blocked":
		var e types.GuardBlocked
		err := json.Unmarshal(raw, &e)
		return e, err
	case "limit_warning":
		var e types.LimitWarning
		err := json.Unmarshal(raw, &e)
		return e, err
	case "compacted":
		var e types.Compacted
		err := json.Unmarshal(raw, &e)
		return e, err
	case "state_changed":
		var e types.StateChanged
		err := json.Unmarshal(raw, &e)
		return e, err
	case "feedback_recorded":
		var e types.FeedbackRecorded
		err := json.Unmarshal(raw, &e)
		return e, err
	case "steer_applied":
		var e types.SteerApplied
		err := json.Unmarshal(raw, &e)
		return e, err
	case "done":
		var e types.Done
		err := json.Unmarshal(raw, &e)
		return e, err
	case "terminal_error":
		var e types.TerminalError
		err := json.Unmarshal(raw, &e)
		return e, err
	}
	return nil, fmt.Errorf("decode Event: unknown payload kind %q", kind)
}

// SeedRecord installs envelope as the stored bytes of the run's event at
// seq, so the next public read consumes them as persisted bytes. Seeding
// bypasses partition rules: fixtures replay historical bytes, not client
// writes.
func (s *MemoryEventLog) SeedRecord(_ context.Context, identity string, envelope RecordEnvelope) error {
	raw, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("seed %s: %w", identity, err)
	}
	runID, seq, err := eventSlotIdentity(identity)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	slot, err := s.eventSlotLocked(runID, seq, identity)
	if err != nil {
		return err
	}
	slot.raw = raw
	return nil
}

// StoredRecord returns the stored bytes of identity unchanged.
func (s *MemoryEventLog) StoredRecord(_ context.Context, identity string) ([]byte, error) {
	runID, seq, err := eventSlotIdentity(identity)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	slot, err := s.eventSlotLocked(runID, seq, identity)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), slot.raw...), nil
}

func (s *MemoryEventLog) eventSlotLocked(runID string, seq int64, identity string) (*eventSlot, error) {
	ring, ok := s.rings[runID]
	if !ok {
		return nil, fmt.Errorf("record identity %s: not found", identity)
	}
	for i := range ring.count {
		idx := (ring.head + i) % s.capacity
		if ring.slots[idx].event.Meta.Seq == seq {
			return &ring.slots[idx], nil
		}
	}
	return nil, fmt.Errorf("record identity %s: not found", identity)
}

// eventSlotIdentity resolves an "event/<runID>/<seq>" identity. Run ids
// must not carry a slash.
func eventSlotIdentity(identity string) (string, int64, error) {
	rest, ok := strings.CutPrefix(identity, "event/")
	if !ok {
		return "", 0, fmt.Errorf("record identity %s: not an event identity", identity)
	}
	runID, rest, _ := strings.Cut(rest, "/")
	seq, err := strconv.ParseInt(rest, 10, 64)
	if runID == "" || err != nil {
		return "", 0, fmt.Errorf("record identity %s: bad identity", identity)
	}
	return runID, seq, nil
}
