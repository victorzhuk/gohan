package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Checkpoints stores suspension checkpoints keyed by single-use resume
// tokens on Postgres and carries the resumer and ready-lister surfaces.
type Checkpoints struct {
	db   *DB
	reg  *stores.SchemaRegistry
	runs stores.RunInfoSource
}

// CheckpointOption configures NewCheckpoints.
type CheckpointOption func(*Checkpoints)

// WithCheckpointRunInfo injects the run info extractor used to index a
// checkpoint by run id for PendingInput.
func WithCheckpointRunInfo(src stores.RunInfoSource) CheckpointOption {
	return func(s *Checkpoints) { s.runs = src }
}

// NewCheckpoints builds the checkpoint store on db.
func NewCheckpoints(db *DB, opts ...CheckpointOption) *Checkpoints {
	s := &Checkpoints{db: db, reg: stores.NewSchemaRegistry()}
	for _, o := range opts {
		o(s)
	}
	return s
}

type checkpointRow struct {
	token     types.ResumeToken
	consumed  bool
	expiresAt *time.Time
	payload   []byte
	input     []byte
}

const checkpointColumns = `token, consumed, expires_at, payload, input`

func (s *Checkpoints) load(ctx context.Context, q executor, t types.ResumeToken) (*checkpointRow, error) {
	var r checkpointRow
	err := q.QueryRow(ctx, `SELECT `+checkpointColumns+` FROM checkpoint_record WHERE token = $1 FOR UPDATE`, string(t)).
		Scan(&r.token, &r.consumed, &r.expiresAt, &r.payload, &r.input)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenMismatch)
	}
	if err != nil {
		return nil, fmt.Errorf("load checkpoint %s: %w", t, err)
	}
	return &r, nil
}

func (r *checkpointRow) expired(now time.Time) bool {
	return r.expiresAt != nil && !now.Before(*r.expiresAt)
}

func (s *Checkpoints) live(ctx context.Context, q executor, t types.ResumeToken) (*checkpointRow, error) {
	r, err := s.load(ctx, q, t)
	if err != nil {
		return nil, err
	}
	if r.consumed {
		return nil, fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenConsumed)
	}
	now, err := s.db.now(ctx, q)
	if err != nil {
		return nil, err
	}
	if r.expired(now) {
		return nil, fmt.Errorf("gohan: resume token %s: %w", t, types.ErrTokenExpired)
	}
	return r, nil
}

func (s *Checkpoints) decode(ctx context.Context, r *checkpointRow) (stores.Checkpoint, error) {
	cp, err := decode[stores.Checkpoint](ctx, s.reg, r.payload)
	if err != nil {
		return stores.Checkpoint{}, err
	}
	cp.Data = append([]byte(nil), cp.Data...)
	return cp, nil
}

func encodeCheckpoint(cp stores.Checkpoint) ([]byte, error) {
	payload, err := json.Marshal(cp)
	if err != nil {
		return nil, fmt.Errorf("encode Checkpoint: %w", err)
	}
	envelope, err := json.Marshal(stores.RecordEnvelope{Shape: "Checkpoint", SchemaVersion: int(cp.SchemaVersion), Payload: payload})
	if err != nil {
		return nil, fmt.Errorf("encode Checkpoint envelope: %w", err)
	}
	return envelope, nil
}

// Put mints a fresh single-use token for cp. Without an explicit expiry a
// positive configured TTL bounds the checkpoint from store time; with zero
// TTL it never expires.
func (s *Checkpoints) Put(ctx context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
	if s.db.checkpointTTL < 0 {
		return "", fmt.Errorf("put checkpoint: ttl %s: %w", s.db.checkpointTTL, types.ErrInputInvalid)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("gohan: mint resume token: %w", err)
	}
	t := types.ResumeToken("cp_" + hex.EncodeToString(raw[:]))
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = stores.CurrentSchemaVersion
	}
	payload, err := encodeCheckpoint(cp)
	if err != nil {
		return "", err
	}
	runID := cp.RunID
	if runID == "" && s.runs != nil {
		if ri, ok := s.runs(ctx); ok {
			runID = ri.RunID
		}
	}
	if cp.ExpiresAt.IsZero() && s.db.checkpointTTL > 0 && s.db.clock == nil {
		if _, err := s.db.pool.Exec(ctx,
			`INSERT INTO checkpoint_record (token, run_id, session_id, expires_at, payload, created_at)
			 VALUES ($1, $2, $3, clock_timestamp() + make_interval(secs => $4), $5, clock_timestamp())`,
			string(t), runID, cp.SessionID, s.db.checkpointTTL.Seconds(), payload); err != nil {
			return "", fmt.Errorf("put checkpoint: %w", err)
		}
		return t, nil
	}
	now, err := s.db.now(ctx, poolConn{s.db.pool})
	if err != nil {
		return "", fmt.Errorf("put checkpoint: %w", err)
	}
	var expires *time.Time
	switch {
	case !cp.ExpiresAt.IsZero():
		expires = &cp.ExpiresAt
	case s.db.checkpointTTL > 0:
		e := now.Add(s.db.checkpointTTL)
		expires = &e
	}
	if _, err := s.db.pool.Exec(ctx,
		`INSERT INTO checkpoint_record (token, run_id, session_id, expires_at, payload, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		string(t), runID, cp.SessionID, expires, payload, now); err != nil {
		return "", fmt.Errorf("put checkpoint: %w", err)
	}
	return t, nil
}

// Consume atomically marks the token used and records the resume input;
// the row lock makes exactly one of any set of concurrent callers win.
func (s *Checkpoints) Consume(ctx context.Context, t types.ResumeToken, in stores.ResumeInput) (stores.Checkpoint, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return stores.Checkpoint{}, fmt.Errorf("consume checkpoint: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	r, err := s.live(ctx, txConn{tx}, t)
	if err != nil {
		return stores.Checkpoint{}, err
	}
	cp, err := s.decode(ctx, r)
	if err != nil {
		return stores.Checkpoint{}, err
	}
	input, err := json.Marshal(in)
	if err != nil {
		return stores.Checkpoint{}, fmt.Errorf("encode resume input: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE checkpoint_record SET consumed = true, input = $1 WHERE token = $2`, input, string(t)); err != nil {
		return stores.Checkpoint{}, fmt.Errorf("consume checkpoint %s: %w", t, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return stores.Checkpoint{}, fmt.Errorf("consume checkpoint: %w", err)
	}
	return cp, nil
}

// PendingInput returns the checkpoint a suspended run waits on and the
// input Consume recorded.
func (s *Checkpoints) PendingInput(ctx context.Context, runID string) (stores.Checkpoint, stores.ResumeInput, error) {
	var r checkpointRow
	var input []byte
	err := s.db.pool.QueryRow(ctx,
		`SELECT `+checkpointColumns+` FROM checkpoint_record WHERE run_id = $1 ORDER BY created_at DESC LIMIT 1`, runID).
		Scan(&r.token, &r.consumed, &r.expiresAt, &r.payload, &input)
	if errors.Is(err, pgx.ErrNoRows) {
		return stores.Checkpoint{}, stores.ResumeInput{}, fmt.Errorf("gohan: no checkpoint for run %s", runID)
	}
	if err != nil {
		return stores.Checkpoint{}, stores.ResumeInput{}, fmt.Errorf("pending input for run %s: %w", runID, err)
	}
	cp, err := s.decode(ctx, &r)
	if err != nil {
		return stores.Checkpoint{}, stores.ResumeInput{}, err
	}
	var in stores.ResumeInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return stores.Checkpoint{}, stores.ResumeInput{}, fmt.Errorf("decode resume input: %w", err)
		}
		if string(in.Args) == "null" {
			in.Args = nil
		}
		if string(in.Data) == "null" {
			in.Data = nil
		}
	}
	return cp, in, nil
}

// Peek returns the stored snapshot without changing token state.
func (s *Checkpoints) Peek(ctx context.Context, t types.ResumeToken) (stores.Checkpoint, error) {
	r, err := s.live(ctx, poolConn{s.db.pool}, t)
	if err != nil {
		return stores.Checkpoint{}, err
	}
	return s.decode(ctx, r)
}

// JSON round-trips lose the ExpiresAt clock location; compare it by instant.
func checkpointEqual(a, b stores.Checkpoint) bool {
	ae, be := a.ExpiresAt, b.ExpiresAt
	a.ExpiresAt, b.ExpiresAt = time.Time{}, time.Time{}
	return ae.Equal(be) && reflect.DeepEqual(a, b)
}

// ConsumeIf consumes the token only when the stored snapshot still equals
// the expected one.
func (s *Checkpoints) ConsumeIf(ctx context.Context, t types.ResumeToken, expected stores.Checkpoint, in stores.ResumeInput) (stores.Checkpoint, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return stores.Checkpoint{}, fmt.Errorf("consume if: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	r, err := s.live(ctx, txConn{tx}, t)
	if err != nil {
		return stores.Checkpoint{}, err
	}
	cp, err := s.decode(ctx, r)
	if err != nil {
		return stores.Checkpoint{}, err
	}
	if expected.SchemaVersion == 0 {
		expected.SchemaVersion = cp.SchemaVersion
	}
	if !checkpointEqual(expected, cp) {
		return stores.Checkpoint{}, types.ErrVersionConflict
	}
	input, err := json.Marshal(in)
	if err != nil {
		return stores.Checkpoint{}, fmt.Errorf("encode resume input: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE checkpoint_record SET consumed = true, input = $1 WHERE token = $2`, input, string(t)); err != nil {
		return stores.Checkpoint{}, fmt.Errorf("consume if %s: %w", t, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return stores.Checkpoint{}, fmt.Errorf("consume if: %w", err)
	}
	return cp, nil
}

// UpdatePending moves the pending snapshot's Data forward when the caller
// still held the expected snapshot; every other field is immutable.
func (s *Checkpoints) UpdatePending(ctx context.Context, t types.ResumeToken, expected, next stores.Checkpoint) error {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("update pending: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	r, err := s.live(ctx, txConn{tx}, t)
	if err != nil {
		return err
	}
	candidate, err := s.decode(ctx, r)
	if err != nil {
		return err
	}
	if expected.SchemaVersion == 0 {
		expected.SchemaVersion = candidate.SchemaVersion
	}
	if !checkpointEqual(expected, candidate) {
		return types.ErrVersionConflict
	}
	candidate.Data = append(candidate.Data[:0], next.Data...)
	if next.SchemaVersion == 0 {
		next.SchemaVersion = candidate.SchemaVersion
	}
	if !checkpointEqual(candidate, next) {
		return types.ErrVersionConflict
	}
	payload, err := encodeCheckpoint(candidate)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE checkpoint_record SET payload = $1 WHERE token = $2`, payload, string(t)); err != nil {
		return fmt.Errorf("update pending %s: %w", t, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("update pending: %w", err)
	}
	return nil
}

// ResumeReady lists consumed checkpoints whose run still waits on the same
// token, unexpired by store time, oldest first; the limit applies only to
// the eligible rows.
func (s *Checkpoints) ResumeReady(ctx context.Context, limit int) ([]stores.Checkpoint, error) {
	now, err := s.db.now(ctx, poolConn{s.db.pool})
	if err != nil {
		return nil, fmt.Errorf("list resume ready: %w", err)
	}
	rows, err := s.db.pool.Query(ctx,
		`SELECT c.payload, c.token FROM checkpoint_record c
		 JOIN run_record r ON r.resume_token = c.token AND r.state = $1
		 WHERE c.consumed AND (c.expires_at IS NULL OR c.expires_at > $2)
		 ORDER BY c.created_at LIMIT $3`,
		stores.Suspended, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list resume ready: %w", err)
	}
	defer rows.Close()

	var out []stores.Checkpoint
	for rows.Next() {
		var payload []byte
		var token types.ResumeToken
		if err := rows.Scan(&payload, &token); err != nil {
			return nil, fmt.Errorf("list resume ready: %w", err)
		}
		cp, err := decode[stores.Checkpoint](ctx, s.reg, payload)
		if err != nil {
			return nil, err
		}
		// The record carries the token it was discovered under; the
		// stored checkpoint itself never names it.
		cp.Token = token
		out = append(out, cp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list resume ready: %w", err)
	}
	return out, nil
}

// deleteCheckpointDependents removes the checkpoints of one session
// through q. The pool and transaction entries share it.
func deleteCheckpointDependents(ctx context.Context, q executor, sessionID string) error {
	if _, err := q.Exec(ctx, `DELETE FROM checkpoint_record WHERE session_id = $1`, sessionID); err != nil {
		return fmt.Errorf("delete checkpoints of session %s: %w", sessionID, err)
	}
	return nil
}

// DeleteSessionDependents removes the checkpoints of one session on the
// pool.
func (s *Checkpoints) DeleteSessionDependents(ctx context.Context, sessionID string) error {
	return deleteCheckpointDependents(ctx, poolConn{s.db.pool}, sessionID)
}

// deleteSessionDependentsTx removes the checkpoints of one session inside
// the caller's transaction, so the session delete commits or rolls back
// with them.
func (s *Checkpoints) deleteSessionDependentsTx(ctx context.Context, tx pgx.Tx, sessionID string) error {
	return deleteCheckpointDependents(ctx, txConn{tx}, sessionID)
}

// CopySessionDependents moves the checkpoints of one session to its copy.
func (s *Checkpoints) CopySessionDependents(ctx context.Context, from, to string) error {
	if _, err := s.db.pool.Exec(ctx, `UPDATE checkpoint_record SET session_id = $1 WHERE session_id = $2`, to, from); err != nil {
		return fmt.Errorf("copy checkpoints %s to %s: %w", from, to, err)
	}
	return nil
}
