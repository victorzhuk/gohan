package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type workspaceMetaRow struct {
	ref        types.WorkspaceRef
	generation int64
	owner      types.WorkspaceOwner
}

func scanWorkspaceMeta(row pgx.Row) (*workspaceMetaRow, error) {
	var r workspaceMetaRow
	var owner []byte
	err := row.Scan(&r.ref, &r.generation, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(owner, &r.owner); err != nil {
		return nil, fmt.Errorf("decode workspace owner: %w", err)
	}
	return &r, nil
}

const workspaceColumns = `ref, generation, owner`

// WorkspaceMeta reads the session's durable workspace pointer and reports
// whether one is stored. The read is authorized with the session-read
// scope, matching the memory store.
func (s *SessionLog) WorkspaceMeta(ctx context.Context, sessionID string) (stores.WorkspaceMeta, bool, error) {
	if _, _, _, err := s.getSession(ctx, poolConn{s.db.pool}, sessionID, scopeSessionRead); err != nil {
		return stores.WorkspaceMeta{}, false, err
	}
	r, err := scanWorkspaceMeta(s.db.pool.QueryRow(ctx,
		`SELECT `+workspaceColumns+` FROM session_workspace WHERE session_id = $1`, sessionID))
	if err != nil {
		return stores.WorkspaceMeta{}, false, fmt.Errorf("workspace session %s: %w", sessionID, err)
	}
	if r == nil {
		return stores.WorkspaceMeta{}, false, nil
	}
	return stores.WorkspaceMeta{Ref: r.ref, Generation: r.generation, Owner: r.owner}, true, nil
}

// workspaceOwnerTx validates the owner lease under a locked run_record
// row. The caller has already locked session_record, so the lock order is
// session-then-run and a concurrent Heartbeat or Reclaim cannot move the
// lease between the validation and the commit.
func workspaceOwnerTx(ctx context.Context, db *DB, q executor, sessionID string, owner types.WorkspaceOwner) error {
	var runSession string
	var state stores.RunState
	var generation uint64
	var leaseExpiry *time.Time
	err := q.QueryRow(ctx,
		`SELECT session_id, state, generation, lease_expires FROM run_record WHERE run_id = $1 FOR UPDATE`, owner.RunID).
		Scan(&runSession, &state, &generation, &leaseExpiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return runNotActive(owner.RunID)
	}
	if err != nil {
		return fmt.Errorf("workspace run %s: %w", owner.RunID, err)
	}
	now, err := db.now(ctx, q)
	if err != nil {
		return fmt.Errorf("workspace run %s: %w", owner.RunID, err)
	}
	run := runRow{runID: owner.RunID, state: state, leaseExpiry: leaseExpiry}
	if runSession != sessionID || generation != owner.LeaseGeneration || !run.active(now) {
		return runNotActive(owner.RunID)
	}
	return nil
}

// SetWorkspaceMeta writes the durable workspace pointer under the
// generation compare-and-set and the owner proof in one transaction: the
// session advisory lock, then session_record, then the owning run_record
// last. The refusal precedence is the D3 order: authorize, compare the
// workspace token, validate the owner, assign. A refused request writes
// nothing.
func (s *SessionLog) SetWorkspaceMeta(ctx context.Context, sessionID string, expectedGeneration int64, m stores.WorkspaceMeta) (int64, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("workspace session %s: %w", sessionID, err)
	}
	defer tx.Rollback(ctx)
	q := txConn{tx}

	if _, _, _, err := s.getSession(ctx, q, sessionID, scopeSessionWrite); err != nil {
		return 0, err
	}
	stored, err := scanWorkspaceMeta(q.QueryRow(ctx,
		`SELECT `+workspaceColumns+` FROM session_workspace WHERE session_id = $1 FOR UPDATE`, sessionID))
	if err != nil {
		return 0, fmt.Errorf("workspace session %s: %w", sessionID, err)
	}
	switch {
	case stored == nil:
		if expectedGeneration != 0 {
			return 0, fmt.Errorf("workspace session %s at generation %d: %w", sessionID, expectedGeneration, types.ErrVersionConflict)
		}
	case stored.generation != expectedGeneration:
		return 0, fmt.Errorf("workspace session %s at generation %d: %w", sessionID, expectedGeneration, types.ErrVersionConflict)
	case m.Generation == stored.generation && m.Ref != stored.ref:
		return 0, fmt.Errorf("workspace session %s ref %s at generation %d: %w", sessionID, m.Ref, expectedGeneration, types.ErrVersionConflict)
	}
	if err := workspaceOwnerTx(ctx, s.db, q, sessionID, m.Owner); err != nil {
		return 0, err
	}
	owner, err := json.Marshal(m.Owner)
	if err != nil {
		return 0, fmt.Errorf("workspace session %s: encode owner: %w", sessionID, err)
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO session_workspace (session_id, ref, generation, owner) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (session_id) DO UPDATE SET ref = EXCLUDED.ref, generation = EXCLUDED.generation, owner = EXCLUDED.owner`,
		sessionID, m.Ref, expectedGeneration+1, owner); err != nil {
		return 0, fmt.Errorf("workspace session %s: %w", sessionID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("workspace session %s: %w", sessionID, err)
	}
	return expectedGeneration + 1, nil
}

// ImportWorkspaceMeta installs a legacy generation-zero workspace ref as
// generation one under a validated owner lease. It refuses an unauthorized
// caller, a stale owner, a nonzero input generation, an empty ref and
// existing metadata; every refusal leaves the store unchanged.
func (s *SessionLog) ImportWorkspaceMeta(ctx context.Context, sessionID string, owner types.WorkspaceOwner, legacy types.WorkspaceSnapshot) (types.WorkspaceSnapshot, error) {
	if legacy.Generation != 0 {
		return types.WorkspaceSnapshot{}, fmt.Errorf("import session %s: generation %d: %w", sessionID, legacy.Generation, types.ErrInputInvalid)
	}
	if legacy.Ref == "" {
		return types.WorkspaceSnapshot{}, fmt.Errorf("import session %s: empty ref: %w", sessionID, types.ErrInputInvalid)
	}
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return types.WorkspaceSnapshot{}, fmt.Errorf("import session %s: %w", sessionID, err)
	}
	defer tx.Rollback(ctx)
	q := txConn{tx}

	if _, _, _, err := s.getSession(ctx, q, sessionID, scopeSessionWrite); err != nil {
		return types.WorkspaceSnapshot{}, err
	}
	stored, err := scanWorkspaceMeta(q.QueryRow(ctx,
		`SELECT `+workspaceColumns+` FROM session_workspace WHERE session_id = $1 FOR UPDATE`, sessionID))
	if err != nil {
		return types.WorkspaceSnapshot{}, fmt.Errorf("import session %s: %w", sessionID, err)
	}
	if stored != nil {
		return types.WorkspaceSnapshot{}, fmt.Errorf("import session %s: metadata already stored: %w", sessionID, types.ErrVersionConflict)
	}
	if err := workspaceOwnerTx(ctx, s.db, q, sessionID, owner); err != nil {
		return types.WorkspaceSnapshot{}, fmt.Errorf("import session %s: %w", sessionID, err)
	}
	encoded, err := json.Marshal(owner)
	if err != nil {
		return types.WorkspaceSnapshot{}, fmt.Errorf("import session %s: encode owner: %w", sessionID, err)
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO session_workspace (session_id, ref, generation, owner) VALUES ($1, $2, 1, $3)`,
		sessionID, legacy.Ref, encoded); err != nil {
		return types.WorkspaceSnapshot{}, fmt.Errorf("import session %s: %w", sessionID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return types.WorkspaceSnapshot{}, fmt.Errorf("import session %s: %w", sessionID, err)
	}
	return types.WorkspaceSnapshot{Ref: legacy.Ref, Generation: 1}, nil
}
