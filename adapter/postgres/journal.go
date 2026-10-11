package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Journal records tool calls so a retried call replays the recorded result
// instead of re-executing.
type Journal struct {
	db      *DB
	schemas *stores.SchemaRegistry
	ttl     time.Duration
}

// JournalOption configures NewJournal.
type JournalOption func(*Journal)

// WithJournalTTL overrides the replay window.
func WithJournalTTL(ttl time.Duration) JournalOption {
	return func(s *Journal) { s.ttl = ttl }
}

// NewJournal builds the journal store on db.
func NewJournal(db *DB, opts ...JournalOption) *Journal {
	j := &Journal{db: db, schemas: stores.NewSchemaRegistry(), ttl: stores.DefaultJournalTTL}
	for _, opt := range opts {
		opt(j)
	}
	return j
}

// Reserve pins the call key to a reserved entry, or returns the live entry
// a retry reuses. The reservation is insert-first: a fresh call key is
// claimed by an insert that conflicts against the (session_id, call_id)
// unique index, so concurrent first reservations serialise on the index
// instead of a missing-row lock, and exactly one caller sees created=true.
// A fingerprint mismatch on a live entry is refused with
// stores.ErrJournalFingerprintMismatch; an expired entry is replaced.
func (j *Journal) Reserve(ctx context.Context, k types.CallKey, tool string, fp stores.Fingerprint, args json.RawMessage) (stores.Entry, bool, error) {
	now := j.db.clock()
	tx, err := j.db.pool.Begin(ctx)
	if err != nil {
		return stores.Entry{}, false, fmt.Errorf("journal reserve: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	entry, created, err := reserveOnTx(ctx, txConn{tx}, j.schemas, k, tool, fp, args, now, j.ttl)
	if err != nil {
		return stores.Entry{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return stores.Entry{}, false, fmt.Errorf("journal reserve: %w", err)
	}
	return entry, created, nil
}

// ReserveTx is the transaction-taking variant of Reserve: the caller-owned
// transaction carries the reservation and the tool's own write to one
// commit. It shares the insert-first statement path with Reserve, so both
// entry points stay atomic inside a caller transaction.
func (j *Journal) ReserveTx(ctx context.Context, tx pgx.Tx, k types.CallKey, tool string, fp stores.Fingerprint, args json.RawMessage) (stores.Entry, bool, error) {
	return reserveOnTx(ctx, txConn{tx}, j.schemas, k, tool, fp, args, j.db.clock(), j.ttl)
}

// Complete records the result on the pool. The entry must be live and
// reserved, else stores.ErrJournalCompleteMissed.
func (j *Journal) Complete(ctx context.Context, k types.CallKey, res types.ToolResult) error {
	return j.completePool(ctx, k, res, stores.ModelCompletion{})
}

// CompleteModel records the result and the model step's extended fields.
func (j *Journal) CompleteModel(ctx context.Context, k types.CallKey, res types.ToolResult, mc stores.ModelCompletion) error {
	return j.completePool(ctx, k, res, mc)
}

// CompleteTx records the result on the caller-owned transaction tx. It
// shares one guarded statement path with Complete and never commits: the
// caller owns the transaction, so a tool's own row and the journal entry
// reach one commit or neither survives.
func (j *Journal) CompleteTx(ctx context.Context, tx pgx.Tx, k types.CallKey, res types.ToolResult) error {
	_, err := completeOnTx(ctx, txConn{tx}, j.schemas, k, res, stores.ModelCompletion{}, j.db.clock(), j.ttl)
	return err
}

// CompleteModelTx is the transaction-taking variant of CompleteModel: a
// model step's extended entry fields and a caller write commit together.
func (j *Journal) CompleteModelTx(ctx context.Context, tx pgx.Tx, k types.CallKey, res types.ToolResult, mc stores.ModelCompletion) error {
	_, err := completeOnTx(ctx, txConn{tx}, j.schemas, k, res, mc, j.db.clock(), j.ttl)
	return err
}

func (j *Journal) completePool(ctx context.Context, k types.CallKey, res types.ToolResult, mc stores.ModelCompletion) error {
	tx, err := j.db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("journal complete: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := completeOnTx(ctx, txConn{tx}, j.schemas, k, res, mc, j.db.clock(), j.ttl); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("journal complete: %w", err)
	}
	return nil
}

// ByFingerprint lists the session's non-expired entries for a fingerprint,
// newest first.
func (j *Journal) ByFingerprint(ctx context.Context, sessionID string, fp stores.Fingerprint) ([]stores.Entry, error) {
	return j.list(ctx, `SELECT payload FROM journal_record WHERE session_id = $1 AND fingerprint = $2 AND expires_at > $3 ORDER BY at DESC`, sessionID, string(fp), j.db.clock())
}

// Unknown lists the reserved entries whose outcome the process never
// recorded, so a crash window is visible per session.
func (j *Journal) Unknown(ctx context.Context, sessionID string) ([]stores.Entry, error) {
	return j.list(ctx, `SELECT payload FROM journal_record WHERE session_id = $1 AND state = 0 AND expires_at > $2 ORDER BY at`, sessionID, j.db.clock())
}

func (j *Journal) list(ctx context.Context, sql string, args ...any) ([]stores.Entry, error) {
	rows, err := j.db.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("journal list: %w", err)
	}
	defer rows.Close()
	var out []stores.Entry
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("journal list: %w", err)
		}
		e, err := decodeEntry(ctx, j.schemas, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// executor is the SQL surface shared by the pool and a caller-owned
// transaction, so the guarded statements run unchanged on either.
type executor interface {
	Exec(ctx context.Context, sql string, args ...any) (commandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type commandTag interface {
	RowsAffected() int64
}

type poolConn struct{ pool *pgxpool.Pool }

type txConn struct{ tx pgx.Tx }

func (c poolConn) Exec(ctx context.Context, sql string, args ...any) (commandTag, error) {
	return c.pool.Exec(ctx, sql, args...)
}

func (c poolConn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return c.pool.QueryRow(ctx, sql, args...)
}

func (c txConn) Exec(ctx context.Context, sql string, args ...any) (commandTag, error) {
	return c.tx.Exec(ctx, sql, args...)
}

func (c txConn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return c.tx.QueryRow(ctx, sql, args...)
}

func encodeEntry(e stores.Entry) ([]byte, error) {
	payload, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("encode Entry: %w", err)
	}
	raw, err := json.Marshal(stores.RecordEnvelope{Shape: "Entry", SchemaVersion: int(stores.CurrentSchemaVersion), Payload: payload})
	if err != nil {
		return nil, fmt.Errorf("encode Entry envelope: %w", err)
	}
	return raw, nil
}

func decodeEntry(ctx context.Context, reg *stores.SchemaRegistry, raw []byte) (stores.Entry, error) {
	var env stores.RecordEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return stores.Entry{}, fmt.Errorf("decode Entry envelope: %w", err)
	}
	return stores.DecodeRecord[stores.Entry](ctx, reg, env)
}

// completeOnTx is the one guarded statement path Complete, CompleteTx,
// CompleteModel and CompleteModelTx share, so the guards cannot diverge.
func completeOnTx(ctx context.Context, q executor, reg *stores.SchemaRegistry, k types.CallKey, res types.ToolResult, mc stores.ModelCompletion, now time.Time, ttl time.Duration) (stores.Entry, error) {
	var raw []byte
	err := q.QueryRow(ctx, `
SELECT payload FROM journal_record
WHERE session_id = $1 AND call_id = $2 AND state = 0 AND expires_at > $3
FOR UPDATE`, k.SessionID, k.CallID, now).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return stores.Entry{}, stores.ErrJournalCompleteMissed
	}
	if err != nil {
		return stores.Entry{}, fmt.Errorf("journal complete: %w", err)
	}
	entry, err := decodeEntry(ctx, reg, raw)
	if err != nil {
		return stores.Entry{}, err
	}
	entry.State = stores.Completed
	entry.Result = res
	entry.Attempts = mc.Attempts
	entry.Endpoint = mc.Endpoint
	entry.At = now
	encoded, err := encodeEntry(entry)
	if err != nil {
		return stores.Entry{}, err
	}
	tag, err := q.Exec(ctx, `
UPDATE journal_record
SET state = 1, result = $3, at = $4, attempts = $5, endpoint = $6, payload = $7
WHERE session_id = $1 AND call_id = $2 AND state = 0`,
		k.SessionID, k.CallID, res, now, mc.Attempts, mc.Endpoint, encoded)
	if err != nil {
		return stores.Entry{}, fmt.Errorf("journal complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return stores.Entry{}, stores.ErrJournalCompleteMissed
	}
	return entry, nil
}

func reserveOnTx(ctx context.Context, q executor, reg *stores.SchemaRegistry, k types.CallKey, tool string, fp stores.Fingerprint, args json.RawMessage, now time.Time, ttl time.Duration) (stores.Entry, bool, error) {
	var raw []byte
	fresh := stores.Entry{
		State:       stores.Reserved,
		Fingerprint: fp,
		Tool:        tool,
		Key:         k.CallID,
		Args:        append(json.RawMessage(nil), args...),
		At:          now,
	}
	encoded, err := encodeEntry(fresh)
	if err != nil {
		return stores.Entry{}, false, err
	}
	// Insert first: the unique index on (session_id, call_id) is the
	// serialisation point, so ten concurrent first reservations of one
	// call key produce one inserted row and nine conflicts, never a
	// unique-violation.
	var one int
	err = q.QueryRow(ctx, `
INSERT INTO journal_record
    (session_id, call_id, state, fingerprint, tool, args, at, expires_at, payload)
VALUES ($1, $2, 0, $3, $4, $5, $6, $7, $8)
ON CONFLICT (session_id, call_id) DO NOTHING
RETURNING 1`,
		k.SessionID, k.CallID, string(fp), tool, args, now, now.Add(ttl), encoded).Scan(&one)
	switch {
	case err == nil:
		return fresh, true, nil
	case errors.Is(err, pgx.ErrNoRows):
		// The row exists: read it under the row lock and take the
		// live/completed/expired branch.
	case err != nil:
		return stores.Entry{}, false, fmt.Errorf("journal reserve: %w", err)
	}
	err = q.QueryRow(ctx, `SELECT payload FROM journal_record WHERE session_id = $1 AND call_id = $2 FOR UPDATE`, k.SessionID, k.CallID).Scan(&raw)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return stores.Entry{}, false, stores.ErrJournalCompleteMissed
	case err != nil:
		return stores.Entry{}, false, fmt.Errorf("journal reserve: %w", err)
	}
	entry, err := decodeEntry(ctx, reg, raw)
	if err != nil {
		return stores.Entry{}, false, err
	}
	if entry.State == stores.Completed && now.Before(entry.At.Add(ttl)) {
		if entry.Fingerprint != fp {
			return stores.Entry{}, false, stores.ErrJournalFingerprintMismatch
		}
		return entry, false, nil
	}
	if entry.State == stores.Reserved && now.Before(entry.At.Add(ttl)) {
		if entry.Fingerprint != fp {
			return stores.Entry{}, false, stores.ErrJournalFingerprintMismatch
		}
		return entry, false, nil
	}
	tag, err := q.Exec(ctx, `
UPDATE journal_record
SET state = 0, fingerprint = $3, tool = $4, args = $5, result = NULL,
    at = $6, attempts = 0, endpoint = '', expires_at = $7, payload = $8
WHERE session_id = $1 AND call_id = $2`,
		k.SessionID, k.CallID, string(fp), tool, args, now, now.Add(ttl), encoded)
	if err != nil {
		return stores.Entry{}, false, fmt.Errorf("journal reserve: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return stores.Entry{}, false, stores.ErrJournalCompleteMissed
	}
	return fresh, true, nil
}
