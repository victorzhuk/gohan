package gohan

import (
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// EventLogCoalesce is the window a detached run uses to coalesce consecutive
// preview deltas into one EventLog record before Append. Zero means the
// default window.
type EventLogCoalesce time.Duration

// DefaultEventLogCoalesce is the coalescing window of a detached run.
const DefaultEventLogCoalesce = EventLogCoalesce(200 * time.Millisecond)

func (w EventLogCoalesce) orDefault() EventLogCoalesce {
	if w <= 0 {
		return DefaultEventLogCoalesce
	}
	return w
}

// DeltaCoalescer merges consecutive preview deltas of one stream — the same
// kind on the same turn, message or call — into a single event while the
// coalescing window is open. A new key or an expired window closes the open
// record; Flush closes it unconditionally. Only previews coalesce: a delta
// is a client preview, never an input, so merging fragments never changes
// what a tool runs or what the run journals as a message.
type DeltaCoalescer struct {
	window EventLogCoalesce
	now    func() time.Time
	open   *openDelta
}

type openDelta struct {
	key   previewKey
	until time.Time
	event types.Event
	delta string
}

type previewKey struct {
	kind      string
	turn      int
	messageID string
	callID    string
	name      string
}

// NewDeltaCoalescer returns a coalescer with the given window; a zero or
// negative window means DefaultEventLogCoalesce. now stamps the window; nil
// means time.Now.
func NewDeltaCoalescer(window EventLogCoalesce, now func() time.Time) *DeltaCoalescer {
	if now == nil {
		now = time.Now
	}
	return &DeltaCoalescer{window: window.orDefault(), now: now}
}

// Add takes one preview delta and returns the records to emit now: the
// record a key change or an expired window closes, if any.
func (c *DeltaCoalescer) Add(ev types.Event) []types.Event {
	key, ok := previewKeyOf(ev)
	if !ok {
		return nil
	}
	now := c.now()
	if c.open != nil && c.open.key == key && now.Before(c.open.until) {
		c.open.delta += deltaOf(ev)
		return nil
	}
	out := c.Flush()
	c.open = &openDelta{key: key, until: now.Add(time.Duration(c.window)), event: ev, delta: deltaOf(ev)}
	return out
}

// Flush closes the open record, if any, and returns it.
func (c *DeltaCoalescer) Flush() []types.Event {
	if c.open == nil {
		return nil
	}
	ev := c.open.event
	switch e := ev.(type) {
	case types.TextDelta:
		e.Delta = c.open.delta
		ev = e
	case types.ToolArgsDelta:
		e.Delta = c.open.delta
		ev = e
	case types.ResultDelta:
		e.Delta = c.open.delta
		ev = e
	}
	c.open = nil
	return []types.Event{ev}
}

func previewKeyOf(ev types.Event) (previewKey, bool) {
	switch e := ev.(type) {
	case types.TextDelta:
		return previewKey{kind: "text", turn: e.Turn, messageID: e.MessageID}, true
	case types.ToolArgsDelta:
		return previewKey{kind: "tool-args", turn: e.Turn, callID: e.CallID, name: e.Name}, true
	case types.ResultDelta:
		return previewKey{kind: "result", turn: e.Turn, messageID: e.MessageID}, true
	}
	return previewKey{}, false
}

func deltaOf(ev types.Event) string {
	switch e := ev.(type) {
	case types.TextDelta:
		return e.Delta
	case types.ToolArgsDelta:
		return e.Delta
	case types.ResultDelta:
		return e.Delta
	}
	return ""
}
