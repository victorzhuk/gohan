package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// EventLog persists a run's event stream on Postgres. Seq values are
// exactly 1..N per run in append order and never repeat, even after the
// retained window evicts its oldest events.
type EventLog struct {
	db       *DB
	schemas  *stores.SchemaRegistry
	capacity int
}

// EventLogOption configures NewEventLog.
type EventLogOption func(*EventLog)

// WithEventLogCapacity sets the per-run retained window; the oldest
// events beyond it are evicted on append.
func WithEventLogCapacity(n int) EventLogOption {
	return func(s *EventLog) { s.capacity = n }
}

// NewEventLog builds the event log on db.
func NewEventLog(db *DB, opts ...EventLogOption) *EventLog {
	s := &EventLog{db: db, schemas: stores.NewSchemaRegistry(), capacity: stores.DefaultEventLogCapacity}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Append assigns the next Seq for the run and records the event. An unset
// Meta.Time is stamped with the store clock; Meta.RunID is forced to the
// partition key so the two cannot drift.
func (s *EventLog) Append(ctx context.Context, runID string, e stores.Event) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("event append: %w", err)
	}
	e.Meta.RunID = runID
	if e.Meta.Time.IsZero() {
		e.Meta.Time = s.db.clock()
	}
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("event append: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var seq int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO event_seq (run_id, next) VALUES ($1, 1)
		 ON CONFLICT (run_id) DO UPDATE SET next = event_seq.next + 1
		 RETURNING next`, runID).Scan(&seq); err != nil {
		return fmt.Errorf("event append: %w", err)
	}
	e.Meta.Seq = seq
	raw, err := encodeEventRecord(e)
	if err != nil {
		return err
	}
	kind, err := eventKind(e.Payload)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO event_record (run_id, seq, kind, at, payload) VALUES ($1, $2, $3, $4, $5)`,
		runID, seq, kind, e.Meta.Time, raw); err != nil {
		return fmt.Errorf("event append: %w", err)
	}
	if s.capacity > 0 {
		if _, err := tx.Exec(ctx,
			`DELETE FROM event_record WHERE run_id = $1 AND seq <= $2`,
			runID, seq-int64(s.capacity)); err != nil {
			return fmt.Errorf("event append: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("event append: %w", err)
	}
	return nil
}

// Read yields the run's retained events with Seq greater than afterSeq, in
// Seq order. A cursor older than the retained window is refused with an
// error wrapping stores.ErrStaleCursor. Breaking early stops the scan.
func (s *EventLog) Read(ctx context.Context, runID string, afterSeq int64) iter.Seq2[stores.Event, error] {
	return func(yield func(stores.Event, error) bool) {
		first, err := s.firstSeq(ctx, runID)
		if err != nil {
			yield(stores.Event{}, err)
			return
		}
		if first > 0 && afterSeq+1 < first {
			err := fmt.Errorf("gohan: oldest retained seq is %d: %w", first, stores.ErrStaleCursor)
			yield(stores.Event{}, err)
			return
		}
		rows, err := s.db.pool.Query(ctx,
			`SELECT payload FROM event_record WHERE run_id = $1 AND seq > $2 ORDER BY seq`,
			runID, afterSeq)
		if err != nil {
			yield(stores.Event{}, fmt.Errorf("event read: %w", err))
			return
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				yield(stores.Event{}, fmt.Errorf("event read: %w", err))
				return
			}
			e, err := decodeEventRecord(ctx, s.schemas, raw)
			if err != nil {
				yield(stores.Event{}, err)
				return
			}
			if !yield(e, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(stores.Event{}, fmt.Errorf("event read: %w", err))
		}
	}
}

// firstSeq reports the oldest retained Seq, or, once every recorded event
// is gone, the first sequence a fresh cursor may resume from. Zero means
// the run never recorded an event.
func (s *EventLog) firstSeq(ctx context.Context, runID string) (int64, error) {
	var first, next int64
	err := s.db.pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT MIN(seq) FROM event_record WHERE run_id = $1), 0),
		        COALESCE((SELECT next FROM event_seq WHERE run_id = $1), 0)`, runID).
		Scan(&first, &next)
	if err != nil {
		return 0, fmt.Errorf("event read: %w", err)
	}
	if first == 0 {
		if next > 0 {
			return next + 1, nil
		}
		return 0, nil
	}
	return first, nil
}

// Expire drops events whose recorded instant is before olderThan, across
// every run. A run's sequence counter survives so later appends for the
// same run keep increasing.
func (s *EventLog) Expire(ctx context.Context, olderThan time.Time) error {
	if _, err := s.db.pool.Exec(ctx,
		`DELETE FROM event_record WHERE at < $1`, olderThan); err != nil {
		return fmt.Errorf("event expire: %w", err)
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
