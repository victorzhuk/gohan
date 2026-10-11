package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Runs persists run rows, store-clock leases, the signal mailbox and the
// notice outbox on Postgres. RunForSession is the per-session lookup the
// lifecycle lane's Conversation.ActiveRun consumes; this store's lookup
// acceptance precedes that lane's cross-pod acceptance.
type Runs struct {
	db    *DB
	reg   *stores.SchemaRegistry
	info  stores.RunInfoSource
	notic *noticeOutbox
}

// RunOption configures NewRuns.
type RunOption func(*Runs)

// WithRunInfo injects the run info extractor used to scope an operation id
// to its tenant and to stamp the notice tenant.
func WithRunInfo(src stores.RunInfoSource) RunOption {
	return func(s *Runs) { s.info = src }
}

// NewRuns builds the runs store on db.
func NewRuns(db *DB, opts ...RunOption) *Runs {
	s := &Runs{db: db, reg: stores.NewSchemaRegistry(), notic: &noticeOutbox{db: db}}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Runs) tenant(ctx context.Context) string {
	if s.info == nil {
		return ""
	}
	ri, _ := s.info(ctx)
	return ri.Principal.Tenant
}

type runRow struct {
	runID       string
	sessionID   string
	tenant      string
	operationID *string
	state       stores.RunState
	seq         int64
	resultRef   string
	payload     []byte
	startedAt   time.Time
	heartbeatAt time.Time
	generation  uint64
	leaseExpiry *time.Time
	token       *string
}

const runColumns = `run_id, session_id, tenant, operation_id, state, seq, result_ref, payload, started_at, heartbeat_at, generation, lease_expires, resume_token`

func scanRunRow(row pgx.Row) (*runRow, error) {
	var r runRow
	err := row.Scan(&r.runID, &r.sessionID, &r.tenant, &r.operationID, &r.state, &r.seq, &r.resultRef, &r.payload, &r.startedAt, &r.heartbeatAt, &r.generation, &r.leaseExpiry, &r.token)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func scanRunRows(rows pgx.Rows) ([]*runRow, error) {
	defer rows.Close()
	var out []*runRow
	for rows.Next() {
		r, err := scanRunRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Runs) decode(ctx context.Context, r *runRow) (stores.Run, error) {
	run, err := decode[stores.Run](ctx, s.reg, r.payload)
	if err != nil {
		return stores.Run{}, err
	}
	run.Uncertain = append([]types.CallKey(nil), run.Uncertain...)
	run.Pending = append([]types.ToolUse(nil), run.Pending...)
	return run, nil
}

func (s *Runs) encode(ctx context.Context, run stores.Run) ([]byte, error) {
	raw, err := encodeRecord("Run", run)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (r *runRow) expiredAt(now time.Time) bool {
	return r.leaseExpiry != nil && !now.Before(*r.leaseExpiry)
}

func (r *runRow) active(now time.Time) bool {
	switch r.state {
	case stores.Running, stores.Resuming:
		return !r.expiredAt(now)
	default:
		return false
	}
}

func runNotFound(id, kind string) error {
	return fmt.Errorf("%w: %s %s", stores.ErrRunNotFound, kind, id)
}

func runNotActive(id string) error {
	return fmt.Errorf("%w: run %s", types.ErrRunNotActive, id)
}

func (s *Runs) held(r *runRow, l stores.Lease, now time.Time) error {
	if l.Generation == 0 || r.generation != l.Generation || r.leaseExpiry == nil || r.expiredAt(now) {
		return runNotActive(r.runID)
	}
	return nil
}

// nextGeneration mints the next lease generation from the counter row; a
// generation never repeats across restarts.
func nextGeneration(ctx context.Context, q executor) (uint64, error) {
	var generation uint64
	err := q.QueryRow(ctx, `UPDATE run_counter SET generation = generation + 1 WHERE id = 1 RETURNING generation`).Scan(&generation)
	if err != nil {
		return 0, fmt.Errorf("mint lease generation: %w", err)
	}
	return generation, nil
}

func lockSession(ctx context.Context, q executor, sessionID string) error {
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, sessionID); err != nil {
		return fmt.Errorf("lock session %s: %w", sessionID, err)
	}
	return nil
}

// operationLockKey encodes (tenant, operation_id) as a length-prefixed
// tuple so no tenant and operation id pair from a different split hashes to
// the same key.
func operationLockKey(tenant, operationID string) string {
	return strconv.Itoa(len(tenant)) + ":" + tenant + strconv.Itoa(len(operationID)) + ":" + operationID
}

// lockOperation takes the transaction advisory lock that serialises Start
// calls racing one tenant-scoped operation id across sessions. Every Start
// path takes it before the session lock, so the fixed order prevents
// deadlock.
func lockOperation(ctx context.Context, q executor, tenant, operationID string) error {
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, operationLockKey(tenant, operationID)); err != nil {
		return fmt.Errorf("lock operation %s: %w", operationID, err)
	}
	return nil
}

// Start records a new run and mints its lease. A live lease on the session
// refuses with types.ErrRunActive; a recorded operation id refuses with
// stores.OperationExistsError naming the recorded run. For a non-empty
// OperationID the operation lock is taken before the session lock and the
// duplicate lookup runs only under it, so run_record_operation stays the
// uniqueness authority and a cross-session race never surfaces a raw
// unique-violation.
func (s *Runs) Start(ctx context.Context, r stores.Run, ttl time.Duration) (stores.Lease, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return stores.Lease{}, fmt.Errorf("start run %s: %w", r.RunID, err)
	}
	defer tx.Rollback(ctx)

	tenant := s.tenant(ctx)
	if r.OperationID != "" {
		if err := lockOperation(ctx, txConn{tx}, tenant, r.OperationID); err != nil {
			return stores.Lease{}, fmt.Errorf("start run %s: %w", r.RunID, err)
		}
	}
	if err := lockSession(ctx, txConn{tx}, r.SessionID); err != nil {
		return stores.Lease{}, fmt.Errorf("start run %s: %w", r.RunID, err)
	}
	if r.OperationID != "" {
		var recorded string
		err := tx.QueryRow(ctx, `SELECT run_id FROM run_record WHERE tenant = $1 AND operation_id = $2`, tenant, r.OperationID).Scan(&recorded)
		switch {
		case err == nil:
			return stores.Lease{}, stores.OperationExistsError{RunID: recorded}
		case !errors.Is(err, pgx.ErrNoRows):
			return stores.Lease{}, fmt.Errorf("start run %s: %w", r.RunID, err)
		}
	}
	var liveID string
	var liveState stores.RunState
	var liveExpiry *time.Time
	err = tx.QueryRow(ctx,
		`SELECT run_id, state, lease_expires FROM run_record WHERE session_id = $1 ORDER BY started_at DESC LIMIT 1`, r.SessionID).
		Scan(&liveID, &liveState, &liveExpiry)
	switch {
	case err == nil:
		live := runRow{runID: liveID, state: liveState, leaseExpiry: liveExpiry}
		if live.active(s.db.clock()) {
			return stores.Lease{}, fmt.Errorf("%w: run %s", types.ErrRunActive, liveID)
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return stores.Lease{}, fmt.Errorf("start run %s: %w", r.RunID, err)
	}

	generation, err := nextGeneration(ctx, txConn{tx})
	if err != nil {
		return stores.Lease{}, fmt.Errorf("start run %s: %w", r.RunID, err)
	}
	now := s.db.clock()
	r.State = stores.Running
	r.StartedAt = now
	r.Heartbeat = now
	expires := now.Add(ttl)
	payload, err := s.encode(ctx, r)
	if err != nil {
		return stores.Lease{}, fmt.Errorf("start run %s: %w", r.RunID, err)
	}
	var opID *string
	if r.OperationID != "" {
		opID = &r.OperationID
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO run_record (run_id, session_id, tenant, operation_id, state, seq, result_ref, payload, started_at, heartbeat_at, generation, lease_expires)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		r.RunID, r.SessionID, tenant, opID, r.State, r.Seq, r.ResultRef, payload, r.StartedAt, r.Heartbeat, generation, expires); err != nil {
		return stores.Lease{}, fmt.Errorf("start run %s: %w", r.RunID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return stores.Lease{}, fmt.Errorf("start run %s: %w", r.RunID, err)
	}
	return stores.Lease{RunID: r.RunID, Generation: generation, Expires: expires}, nil
}

// Heartbeat refreshes the lease by store time; an expired or foreign lease
// refuses the refresh.
func (s *Runs) Heartbeat(ctx context.Context, l stores.Lease) (stores.Lease, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return stores.Lease{}, fmt.Errorf("heartbeat run %s: %w", l.RunID, err)
	}
	defer tx.Rollback(ctx)

	r, err := scanRunRow(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM run_record WHERE run_id = $1 FOR UPDATE`, l.RunID))
	if errors.Is(err, pgx.ErrNoRows) {
		return stores.Lease{}, runNotFound(l.RunID, "run")
	}
	if err != nil {
		return stores.Lease{}, fmt.Errorf("heartbeat run %s: %w", l.RunID, err)
	}
	now := s.db.clock()
	if err := s.held(r, l, now); err != nil {
		return stores.Lease{}, fmt.Errorf("heartbeat: %w", err)
	}
	ttl := r.leaseExpiry.Sub(r.heartbeatAt)
	if ttl <= 0 {
		ttl = stores.LeaseTTL
	}
	expires := now.Add(ttl)
	run, err := s.decode(ctx, r)
	if err != nil {
		return stores.Lease{}, fmt.Errorf("heartbeat run %s: %w", l.RunID, err)
	}
	run.Heartbeat = now
	r.heartbeatAt = now
	r.leaseExpiry = &expires
	if r.payload, err = s.encode(ctx, run); err != nil {
		return stores.Lease{}, fmt.Errorf("heartbeat run %s: %w", l.RunID, err)
	}
	if err := s.persist(ctx, tx, r); err != nil {
		return stores.Lease{}, fmt.Errorf("heartbeat run %s: %w", l.RunID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return stores.Lease{}, fmt.Errorf("heartbeat run %s: %w", l.RunID, err)
	}
	return stores.Lease{RunID: l.RunID, Generation: l.Generation, Expires: expires}, nil
}

func (s *Runs) persist(ctx context.Context, tx pgx.Tx, r *runRow) error {
	if _, err := tx.Exec(ctx,
		`UPDATE run_record SET state = $1, seq = $2, result_ref = $3, payload = $4, heartbeat_at = $5, generation = $6, lease_expires = $7, resume_token = $8 WHERE run_id = $9`,
		r.state, r.seq, r.resultRef, r.payload, r.heartbeatAt, r.generation, r.leaseExpiry, r.token, r.runID); err != nil {
		return err
	}
	return nil
}

// ByOperation returns the run recorded for the tenant and operation id;
// the mapping survives Finish.
func (s *Runs) ByOperation(ctx context.Context, tenant, operationID string) (stores.Run, error) {
	r, err := scanRunRow(s.db.pool.QueryRow(ctx,
		`SELECT `+runColumns+` FROM run_record WHERE tenant = $1 AND operation_id = $2`, tenant, operationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return stores.Run{}, runNotFound(operationID, "operation")
	}
	if err != nil {
		return stores.Run{}, fmt.Errorf("run for operation %s: %w", operationID, err)
	}
	return s.decode(ctx, r)
}

// ByID returns the run with the given id in any state.
func (s *Runs) ByID(ctx context.Context, runID string) (stores.Run, error) {
	r, err := scanRunRow(s.db.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM run_record WHERE run_id = $1`, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return stores.Run{}, runNotFound(runID, "run")
	}
	if err != nil {
		return stores.Run{}, fmt.Errorf("run %s: %w", runID, err)
	}
	return s.decode(ctx, r)
}

// Resuming takes a fresh lease on a suspended run and marks it Resuming, so
// a crash after this point leaves a stale Resuming run Stale lists.
func (s *Runs) Resuming(ctx context.Context, runID string, ttl time.Duration) (stores.Lease, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return stores.Lease{}, fmt.Errorf("resume run %s: %w", runID, err)
	}
	defer tx.Rollback(ctx)

	r, err := scanRunRow(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM run_record WHERE run_id = $1 FOR UPDATE`, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return stores.Lease{}, runNotFound(runID, "run")
	}
	if err != nil {
		return stores.Lease{}, fmt.Errorf("resume run %s: %w", runID, err)
	}
	if r.state != stores.Suspended {
		return stores.Lease{}, runNotActive(runID)
	}
	generation, err := nextGeneration(ctx, txConn{tx})
	if err != nil {
		return stores.Lease{}, fmt.Errorf("resume run %s: %w", runID, err)
	}
	now := s.db.clock()
	run, err := s.decode(ctx, r)
	if err != nil {
		return stores.Lease{}, fmt.Errorf("resume run %s: %w", runID, err)
	}
	run.State = stores.Resuming
	run.Heartbeat = now
	r.state = stores.Resuming
	r.heartbeatAt = now
	r.generation = generation
	expires := now.Add(ttl)
	r.leaseExpiry = &expires
	if r.payload, err = s.encode(ctx, run); err != nil {
		return stores.Lease{}, fmt.Errorf("resume run %s: %w", runID, err)
	}
	if err := s.persist(ctx, tx, r); err != nil {
		return stores.Lease{}, fmt.Errorf("resume run %s: %w", runID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return stores.Lease{}, fmt.Errorf("resume run %s: %w", runID, err)
	}
	return stores.Lease{RunID: runID, Generation: generation, Expires: expires}, nil
}

// Stale lists up to limit runs whose heartbeat is older than staleAfter by
// the store's clock, in heartbeat order, covering Running and Resuming.
func (s *Runs) Stale(ctx context.Context, staleAfter time.Duration, limit int) ([]stores.Run, error) {
	cutoff := s.db.clock().Add(-staleAfter)
	rows, err := s.db.pool.Query(ctx,
		`SELECT `+runColumns+` FROM run_record WHERE state IN (0, 2) AND heartbeat_at <= $1 ORDER BY heartbeat_at LIMIT $2`,
		cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("list stale runs: %w", err)
	}
	rs, err := scanRunRows(rows)
	if err != nil {
		return nil, fmt.Errorf("list stale runs: %w", err)
	}
	return s.decodeAll(ctx, rs)
}

// Preempted lists up to limit suspended runs in heartbeat order.
func (s *Runs) Preempted(ctx context.Context, limit int) ([]stores.Run, error) {
	rows, err := s.db.pool.Query(ctx,
		`SELECT `+runColumns+` FROM run_record WHERE state = 1 ORDER BY heartbeat_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list preempted runs: %w", err)
	}
	rs, err := scanRunRows(rows)
	if err != nil {
		return nil, fmt.Errorf("list preempted runs: %w", err)
	}
	return s.decodeAll(ctx, rs)
}

func (s *Runs) decodeAll(ctx context.Context, rs []*runRow) ([]stores.Run, error) {
	out := make([]stores.Run, 0, len(rs))
	for _, r := range rs {
		run, err := s.decode(ctx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

// Reclaim takes a fresh lease on a run Stale listed. The row lock plus the
// expiry recheck make exactly one concurrent reaper win.
func (s *Runs) Reclaim(ctx context.Context, r stores.Run, ttl time.Duration) (stores.Lease, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return stores.Lease{}, fmt.Errorf("reclaim run %s: %w", r.RunID, err)
	}
	defer tx.Rollback(ctx)

	row, err := scanRunRow(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM run_record WHERE run_id = $1 FOR UPDATE`, r.RunID))
	if errors.Is(err, pgx.ErrNoRows) {
		return stores.Lease{}, runNotFound(r.RunID, "run")
	}
	if err != nil {
		return stores.Lease{}, fmt.Errorf("reclaim run %s: %w", r.RunID, err)
	}
	now := s.db.clock()
	if !row.expiredAt(now) {
		return stores.Lease{}, fmt.Errorf("%w: run %s lease is live", types.ErrRunNotActive, r.RunID)
	}
	generation, err := nextGeneration(ctx, txConn{tx})
	if err != nil {
		return stores.Lease{}, fmt.Errorf("reclaim run %s: %w", r.RunID, err)
	}
	run, err := s.decode(ctx, row)
	if err != nil {
		return stores.Lease{}, fmt.Errorf("reclaim run %s: %w", r.RunID, err)
	}
	run.Heartbeat = now
	row.state = run.State
	row.heartbeatAt = now
	row.generation = generation
	expires := now.Add(ttl)
	row.leaseExpiry = &expires
	if row.payload, err = s.encode(ctx, run); err != nil {
		return stores.Lease{}, fmt.Errorf("reclaim run %s: %w", r.RunID, err)
	}
	if err := s.persist(ctx, tx, row); err != nil {
		return stores.Lease{}, fmt.Errorf("reclaim run %s: %w", r.RunID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return stores.Lease{}, fmt.Errorf("reclaim run %s: %w", r.RunID, err)
	}
	return stores.Lease{RunID: r.RunID, Generation: generation, Expires: expires}, nil
}

// SessionLeaseActive answers whether the session currently holds a live
// run lease.
func (s *Runs) SessionLeaseActive(ctx context.Context, sessionID string) bool {
	var state stores.RunState
	var expiry *time.Time
	err := s.db.pool.QueryRow(ctx,
		`SELECT state, lease_expires FROM run_record WHERE session_id = $1 ORDER BY started_at DESC LIMIT 1`, sessionID).
		Scan(&state, &expiry)
	if err != nil {
		return false
	}
	r := runRow{state: state, leaseExpiry: expiry}
	return r.active(s.db.clock())
}

// Signal appends one mailbox entry for a running run; the bound and the
// state check hold inside the insert transaction.
func (s *Runs) Signal(ctx context.Context, runID string, sig stores.Signal) error {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("signal run %s: %w", runID, err)
	}
	defer tx.Rollback(ctx)

	var state stores.RunState
	err = tx.QueryRow(ctx, `SELECT state FROM run_record WHERE run_id = $1`, runID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return runNotFound(runID, "run")
	}
	if err != nil {
		return fmt.Errorf("signal run %s: %w", runID, err)
	}
	if state != stores.Running {
		return runNotActive(runID)
	}
	var pending int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM run_signal WHERE run_id = $1`, runID).Scan(&pending); err != nil {
		return fmt.Errorf("signal run %s: %w", runID, err)
	}
	if pending >= stores.MaxPendingSignals {
		return fmt.Errorf("%w: run %s", types.ErrMailboxFull, runID)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO run_signal (run_id, kind, message, at) VALUES ($1, $2, $3, $4)`,
		runID, sig.Kind, sig.Message, s.db.clock()); err != nil {
		return fmt.Errorf("signal run %s: %w", runID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("signal run %s: %w", runID, err)
	}
	return nil
}

// Drain returns and removes the run's pending signals in arrival order;
// only the current lease holder may drain.
func (s *Runs) Drain(ctx context.Context, l stores.Lease) ([]stores.Signal, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("drain run %s: %w", l.RunID, err)
	}
	defer tx.Rollback(ctx)

	r, err := scanRunRow(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM run_record WHERE run_id = $1 FOR UPDATE`, l.RunID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, runNotFound(l.RunID, "run")
	}
	if err != nil {
		return nil, fmt.Errorf("drain run %s: %w", l.RunID, err)
	}
	if err := s.held(r, l, s.db.clock()); err != nil {
		return nil, fmt.Errorf("drain: %w", err)
	}
	rows, err := tx.Query(ctx, `DELETE FROM run_signal WHERE run_id = $1 RETURNING kind, message, at`, l.RunID)
	if err != nil {
		return nil, fmt.Errorf("drain run %s: %w", l.RunID, err)
	}
	defer rows.Close()
	var out []stores.Signal
	for rows.Next() {
		var sig stores.Signal
		if err := rows.Scan(&sig.Kind, &sig.Message, &sig.At); err != nil {
			return nil, fmt.Errorf("drain run %s: %w", l.RunID, err)
		}
		out = append(out, sig)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("drain run %s: %w", l.RunID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("drain run %s: %w", l.RunID, err)
	}
	return out, nil
}

// sessionRunFinder is the structural shape of core's optional per-session
// run lookup. The adapter asserts it without importing core.
type sessionRunFinder interface {
	RunForSession(ctx context.Context, sessionID string) (stores.Run, error)
}

var _ sessionRunFinder = (*Runs)(nil)

// RunForSession returns the session's most recent run whose state is
// Running or Resuming and whose non-null lease expires strictly after the
// store clock, or stores.ErrRunNotFound when no row is eligible. It is a
// plain pool read that lets the lifecycle lane's Conversation.ActiveRun
// reach a run another pod started; owner authorization stays with that
// lane, and Signal remains the cross-pod control handle.
func (s *Runs) RunForSession(ctx context.Context, sessionID string) (stores.Run, error) {
	r, err := scanRunRow(s.db.pool.QueryRow(ctx,
		`SELECT `+runColumns+` FROM run_record
		 WHERE session_id = $1 AND state IN ($2, $3) AND lease_expires IS NOT NULL AND lease_expires > $4
		 ORDER BY started_at DESC, run_id DESC LIMIT 1`,
		sessionID, stores.Running, stores.Resuming, s.db.clock()))
	if errors.Is(err, pgx.ErrNoRows) {
		return stores.Run{}, runNotFound(sessionID, "session")
	}
	if err != nil {
		return stores.Run{}, fmt.Errorf("run for session %s: %w", sessionID, err)
	}
	return s.decode(ctx, r)
}

// deleteRunDependents removes the runs, signals and notices of one session
// through q. The pool and transaction entries share it, so both paths run
// the same statements in the same order.
func deleteRunDependents(ctx context.Context, q executor, sessionID string) error {
	if _, err := q.Exec(ctx,
		`DELETE FROM run_signal WHERE run_id IN (SELECT run_id FROM run_record WHERE session_id = $1)`, sessionID); err != nil {
		return fmt.Errorf("delete signals of session %s: %w", sessionID, err)
	}
	if _, err := q.Exec(ctx, `DELETE FROM run_notice WHERE session_id = $1`, sessionID); err != nil {
		return fmt.Errorf("delete notices of session %s: %w", sessionID, err)
	}
	if _, err := q.Exec(ctx, `DELETE FROM run_record WHERE session_id = $1`, sessionID); err != nil {
		return fmt.Errorf("delete runs of session %s: %w", sessionID, err)
	}
	return nil
}

// DeleteSessionDependents removes the runs, signals and notices of one
// session on the pool.
func (s *Runs) DeleteSessionDependents(ctx context.Context, sessionID string) error {
	return deleteRunDependents(ctx, poolConn{s.db.pool}, sessionID)
}

// deleteSessionDependentsTx removes the runs, signals and notices of one
// session inside the caller's transaction, so the session delete commits
// or rolls back with them.
func (s *Runs) deleteSessionDependentsTx(ctx context.Context, tx pgx.Tx, sessionID string) error {
	return deleteRunDependents(ctx, txConn{tx}, sessionID)
}

// CopySessionDependents has no runs to move: a fork copies history, not
// executions.
func (s *Runs) CopySessionDependents(ctx context.Context, from, to string) error {
	return nil
}
