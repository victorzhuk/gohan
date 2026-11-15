package redis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const (
	eventReadPage = 256

	eventStreamField = "e"
	eventTimeField   = "t"
)

// errCompactionPayload rejects a malformed or unauthorized publication
// payload: the caller must present types.Compacted whose PublicationID
// matches the requested identity.
var errCompactionPayload = errors.New("gohan: invalid compaction publication")

// errCompactionConflict rejects a publication that reuses an accepted
// identity with a different fingerprint.
var errCompactionConflict = errors.New("gohan: conflicting compaction publication")

// EventLog persists a run's event stream on Redis streams. Seq values are
// exactly 1..N per run in append order and never repeat, even after the
// retained window evicts its oldest events.
type EventLog struct {
	config *config
	client goredis.UniversalClient
}

// NewEventLog builds the event log on client.
func NewEventLog(client goredis.UniversalClient, opts ...Option) *EventLog {
	c := defaultConfig()
	for _, opt := range opts {
		opt(c)
	}
	if c.eventLogSchemas == nil {
		c.eventLogSchemas = stores.NewSchemaRegistry()
	}
	return &EventLog{config: c, client: client}
}

func (l *EventLog) streamKey(runID string) string {
	return l.config.key("events", "run", runID)
}

func (l *EventLog) seqKey(runID string) string {
	return l.config.key("events", "seq", runID)
}

func (l *EventLog) pubsKey(runID string) string {
	return l.config.key("events", "pubs", runID)
}

// Append assigns the next Seq for the run and records the event. An unset
// Meta.Time is stamped with the store clock; Meta.RunID is forced to the
// partition key so the two cannot drift.
func (l *EventLog) Append(ctx context.Context, runID string, e stores.Event) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("event append: %w", err)
	}
	e.Meta.RunID = runID
	raw, err := encodeEventRecord(e)
	if err != nil {
		return err
	}
	if e.Meta.Time.IsZero() {
		ms, err := l.config.nowMS(ctx, l.client)
		if err != nil {
			return fmt.Errorf("event append: %w", err)
		}
		e.Meta.Time = time.UnixMilli(ms)
		if raw, err = encodeEventRecord(e); err != nil {
			return err
		}
	}
	_, err = l.client.Eval(ctx, eventAppendScript,
		[]string{l.seqKey(runID), l.streamKey(runID), l.config.key("events", "index")},
		strconv.FormatInt(e.Meta.Time.UnixMilli(), 10), string(raw),
		strconv.Itoa(l.config.eventLogCapacity), runID).Result()
	if err != nil {
		return fmt.Errorf("event append: %w", err)
	}
	return nil
}

// Read yields the run's retained events with Seq greater than afterSeq, in
// Seq order. A cursor older than the retained window is refused with an
// error wrapping stores.ErrStaleCursor. Breaking early stops the scan.
func (l *EventLog) Read(ctx context.Context, runID string, afterSeq int64) iter.Seq2[stores.Event, error] {
	return func(yield func(stores.Event, error) bool) {
		first, err := l.firstSeq(ctx, runID)
		if err != nil {
			yield(stores.Event{}, err)
			return
		}
		if first > 0 && afterSeq+1 < first {
			err := fmt.Errorf("gohan: oldest retained seq is %d: %w", first, stores.ErrStaleCursor)
			yield(stores.Event{}, err)
			return
		}
		start := "(" + strconv.FormatInt(afterSeq, 10) + "-0"
		for {
			entries, err := l.client.XRangeN(ctx, l.streamKey(runID), start, "+", eventReadPage).Result()
			if err != nil {
				yield(stores.Event{}, fmt.Errorf("event read: %w", err))
				return
			}
			for _, entry := range entries {
				raw, err := entryField(entry, eventStreamField)
				if err != nil {
					yield(stores.Event{}, fmt.Errorf("event read: %w", err))
					return
				}
				e, err := decodeEventRecord(ctx, l.config.eventLogSchemas, []byte(raw))
				if err != nil {
					yield(stores.Event{}, err)
					return
				}
				e.Meta.Seq = cutEntrySeq(entry.ID)
				if !yield(e, nil) {
					return
				}
			}
			if len(entries) < eventReadPage {
				return
			}
			start = "(" + entries[len(entries)-1].ID
		}
	}
}

// firstSeq reports the oldest retained Seq, or, once every recorded event
// is gone, the first sequence a fresh cursor may resume from. Zero means
// the run never recorded an event.
func (l *EventLog) firstSeq(ctx context.Context, runID string) (int64, error) {
	res, err := l.client.Eval(ctx, firstSeqScript,
		[]string{l.seqKey(runID), l.streamKey(runID)}).Result()
	if err != nil {
		return 0, fmt.Errorf("event read: %w", err)
	}
	reply, ok := res.([]interface{})
	if !ok || len(reply) != 2 {
		return 0, fmt.Errorf("event read: unexpected script reply %v", res)
	}
	oldest, _ := reply[0].(int64)
	next, _ := reply[1].(int64)
	if oldest >= 0 {
		return oldest, nil
	}
	if next > 0 {
		return next + 1, nil
	}
	return 0, nil
}

func cutEntrySeq(entryID string) int64 {
	seq, _, ok := strings.Cut(entryID, "-")
	if !ok {
		return 0
	}
	id, err := strconv.ParseInt(seq, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// entryField extracts a stream entry field as a string. A missing or
// non-string value is a malformed entry, reported instead of panicked on.
func entryField(entry goredis.XMessage, field string) (string, error) {
	v, ok := entry.Values[field].(string)
	if !ok {
		return "", fmt.Errorf("entry %s: missing %s", entry.ID, field)
	}
	return v, nil
}

// Expire drops events whose recorded instant is before olderThan, across
// every run. A run's sequence counter and compaction tombstone survive so
// later appends keep increasing and delayed retries still deduplicate.
func (l *EventLog) Expire(ctx context.Context, olderThan time.Time) error {
	cutoff := olderThan.UnixMilli()
	index := l.config.key("events", "index")
	for {
		// The index scores a run with its oldest retained instant, so a run
		// below the cutoff holds at least one expired event. Swept runs
		// leave the [-inf, cutoff) range, so paging from the front always
		// makes progress and never revisits a run.
		runs, err := l.client.ZRangeByScore(ctx, index, &goredis.ZRangeBy{
			Min: "-inf", Max: "(" + strconv.FormatInt(cutoff, 10), Count: 100,
		}).Result()
		if err != nil {
			return fmt.Errorf("event expire: %w", err)
		}
		for _, runID := range runs {
			oldest, empty, err := l.expireRun(ctx, runID, cutoff)
			if err != nil {
				return err
			}
			if empty {
				if err := l.client.ZRem(ctx, index, runID).Err(); err != nil {
					return fmt.Errorf("event expire: %w", err)
				}
				continue
			}
			if err := l.client.ZAdd(ctx, index, goredis.Z{Score: float64(oldest), Member: runID}).Err(); err != nil {
				return fmt.Errorf("event expire: %w", err)
			}
		}
		if len(runs) < 100 {
			return nil
		}
	}
}

// expireRun drops the run's events recorded before cutoff and reports the
// oldest instant left, so the run index can be re-scored to what the next
// sweep must visit. empty reports that the run retains nothing at all.
func (l *EventLog) expireRun(ctx context.Context, runID string, cutoff int64) (oldest int64, empty bool, err error) {
	start := "-"
	for {
		entries, err := l.client.XRangeN(ctx, l.streamKey(runID), start, "+", eventReadPage).Result()
		if err != nil {
			return 0, false, fmt.Errorf("event expire: %w", err)
		}
		var drop []string
		kept := false
		for _, entry := range entries {
			raw, err := entryField(entry, eventTimeField)
			if err != nil {
				return 0, false, fmt.Errorf("event expire: %w", err)
			}
			ms, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return 0, false, fmt.Errorf("event expire: entry %s: unparseable %s", entry.ID, eventTimeField)
			}
			if ms < cutoff {
				drop = append(drop, entry.ID)
				continue
			}
			if !kept || ms < oldest {
				oldest = ms
				kept = true
			}
		}
		if len(drop) > 0 {
			if err := l.client.XDel(ctx, l.streamKey(runID), drop...).Err(); err != nil {
				return 0, false, fmt.Errorf("event expire: %w", err)
			}
		}
		if len(entries) < eventReadPage {
			return oldest, !kept, nil
		}
		start = "(" + entries[len(entries)-1].ID
	}
}

// compactionEventSemantic rebuilds the producer-owned projection of a
// compaction event: the payload plus the meta minus the sink-owned Seq and
// Time, with Meta.RunID forced to the run partition.
func compactionEventSemantic(e stores.Event, runID string) stores.Event {
	return stores.Event{
		Payload: e.Payload,
		Meta: types.EventMeta{
			SessionID:   e.Meta.SessionID,
			RunID:       runID,
			RootRunID:   e.Meta.RootRunID,
			ParentRunID: e.Meta.ParentRunID,
			Depth:       e.Meta.Depth,
			Flow:        e.Meta.Flow,
		},
	}
}

// compactionEventFingerprint hashes the producer-owned fields of a
// compaction event into the fixed 32-byte digest a tombstone stores. The
// digest covers the payload and the meta minus the sink-owned Seq and Time,
// with Meta.RunID forced to the run partition.
func compactionEventFingerprint(e stores.Event) ([]byte, error) {
	encoded, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	return sum[:], nil
}

// AppendCompactionEvent records a compaction event once per (runID,
// publicationID) and reports whether a new event was committed. The
// publication identity lives in its own tombstone hash outside the retained
// window, so window eviction and Expire cannot lift it: a duplicate after
// eviction or expiry stays a no-op, never restores the expired event, and
// never consumes another sequence. A different producer payload under the
// same identity returns errCompactionConflict and changes nothing. The
// payload must be types.Compacted whose PublicationID matches the argument;
// Meta.RunID is forced to the run partition. The sequence is allocated only
// on insert. Tombstone, sequence and event commit in one script.
func (l *EventLog) AppendCompactionEvent(ctx context.Context, publicationID string, runID string, event stores.Event) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("event compaction append: %w", err)
	}
	if publicationID == "" || runID == "" {
		return false, errCompactionPayload
	}
	compacted, ok := event.Payload.(types.Compacted)
	if !ok || compacted.PublicationID != publicationID {
		return false, errCompactionPayload
	}
	semantic := compactionEventSemantic(event, runID)
	fingerprint, err := compactionEventFingerprint(semantic)
	if err != nil {
		return false, errCompactionPayload
	}
	if semantic.Meta.Time.IsZero() {
		ms, err := l.config.nowMS(ctx, l.client)
		if err != nil {
			return false, fmt.Errorf("event compaction append: %w", err)
		}
		semantic.Meta.Time = time.UnixMilli(ms)
	}
	raw, err := encodeEventRecord(semantic)
	if err != nil {
		return false, err
	}
	res, err := l.client.Eval(ctx, eventCompactionScript,
		[]string{l.seqKey(runID), l.streamKey(runID), l.pubsKey(runID), l.config.key("events", "index")},
		publicationID, string(fingerprint), strconv.FormatInt(semantic.Meta.Time.UnixMilli(), 10), string(raw),
		strconv.Itoa(l.config.eventLogCapacity), runID).Result()
	if err != nil {
		return false, fmt.Errorf("event compaction append: %w", err)
	}
	reply, ok := res.([]interface{})
	if !ok || len(reply) != 2 {
		return false, fmt.Errorf("event compaction append: unexpected script reply %v", res)
	}
	inserted, _ := reply[0].(int64)
	stored, _ := reply[1].(string)
	if inserted == 1 {
		return true, nil
	}
	if !bytes.Equal([]byte(stored), fingerprint) {
		return false, fmt.Errorf("event publication %s: %w", publicationID, errCompactionConflict)
	}
	return false, nil
}

// eventRecord is the Event stored shape: the payload kind selects the
// concrete payload type on decode, since payloads carry no type of their
// own on the wire.
type eventRecord struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
	Meta    types.EventMeta `json:"meta"`
}

func encodeEventRecord(e stores.Event) ([]byte, error) {
	kind, err := eventKind(e.Payload)
	if err != nil {
		return nil, err
	}
	payload, err := encodeEventPayload(e.Payload)
	if err != nil {
		return nil, err
	}
	return encodeRecord("Event", eventRecord{Kind: kind, Payload: payload, Meta: e.Meta})
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

func decodeEventRecord(ctx context.Context, reg *stores.SchemaRegistry, raw []byte) (stores.Event, error) {
	env, err := envelope(raw)
	if err != nil {
		return stores.Event{}, fmt.Errorf("decode Event envelope: %w", err)
	}
	rec, err := stores.DecodeRecord[eventRecord](ctx, reg, env)
	if err != nil {
		return stores.Event{}, err
	}
	payload, err := eventPayload(rec.Kind, rec.Payload)
	if err != nil {
		return stores.Event{}, err
	}
	return stores.Event{Payload: payload, Meta: rec.Meta}, nil
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
