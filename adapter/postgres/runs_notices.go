package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// noticeClaimTTL keeps a claimed notice invisible to other dispatchers for
// the same bound a run lease uses; after it the claim lapses and the
// notice returns to pending with its id unchanged.
const noticeClaimTTL = stores.LeaseTTL

var suspendReasonNames = map[types.SuspendReason]string{
	types.HumanApproval:    "HumanApproval",
	types.AwaitingExternal: "AwaitingExternal",
	types.AwaitingBatch:    "AwaitingBatch",
	types.AwaitingTool:     "AwaitingTool",
	types.AwaitingControl:  "AwaitingControl",
	types.AwaitingInput:    "AwaitingInput",
	types.Scheduled:        "Scheduled",
	types.Preempted:        "Preempted",
	types.HumanHandoff:     "HumanHandoff",
}

func noticeFor(state stores.RunState, stop types.StopReason, suspend types.SuspendReason) (types.NoticeKind, string) {
	switch state {
	case stores.Failed:
		return types.NoticeFailed, string(types.StopFailed)
	case stores.Suspended:
		if name, ok := suspendReasonNames[suspend]; ok {
			return types.NoticeSuspended, name
		}
		return types.NoticeSuspended, string(types.StopSuspended)
	default:
		if stop == types.StopCancelled {
			return types.NoticeCancelled, string(types.StopCancelled)
		}
		return types.NoticeFinished, string(types.StopCompleted)
	}
}

// finish releases the lease, records the terminal state and writes the
// notice outbox entry in the same transaction as the state change.
func (s *Runs) finish(ctx context.Context, l stores.Lease, state stores.RunState, uncertain []types.CallKey, resultRef string, stop types.StopReason) error {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finish run %s: %w", l.RunID, err)
	}
	defer tx.Rollback(ctx)

	r, err := scanRunRow(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM run_record WHERE run_id = $1 FOR UPDATE`, l.RunID))
	if errors.Is(err, pgx.ErrNoRows) {
		return runNotFound(l.RunID, "run")
	}
	if err != nil {
		return fmt.Errorf("finish run %s: %w", l.RunID, err)
	}
	if err := s.held(r, l, s.db.clock()); err != nil {
		return fmt.Errorf("finish: %w", err)
	}
	if err := s.refuseSteerPending(ctx, txConn{tx}, r.runID); err != nil {
		return fmt.Errorf("finish run %s: %w", l.RunID, err)
	}
	run, err := s.decode(ctx, r)
	if err != nil {
		return fmt.Errorf("finish run %s: %w", l.RunID, err)
	}
	run.State = state
	run.Uncertain = append([]types.CallKey(nil), uncertain...)
	run.ResultRef = resultRef
	r.state = state
	r.resultRef = resultRef
	r.generation = 0
	r.leaseExpiry = nil
	r.token = nil
	if r.payload, err = s.encode(ctx, run); err != nil {
		return fmt.Errorf("finish run %s: %w", l.RunID, err)
	}
	if err := s.persist(ctx, tx, r); err != nil {
		return fmt.Errorf("finish run %s: %w", l.RunID, err)
	}
	if err := s.notic.append(ctx, txConn{tx}, s.tenant(ctx), run, state, stop, ""); err != nil {
		return fmt.Errorf("finish run %s: %w", l.RunID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("finish run %s: %w", l.RunID, err)
	}
	return nil
}

func (s *Runs) refuseSteerPending(ctx context.Context, q executor, runID string) error {
	var kind stores.SignalKind
	err := q.QueryRow(ctx, `SELECT kind FROM run_signal WHERE run_id = $1 AND kind = $2 LIMIT 1`, runID, stores.SignalSteer).Scan(&kind)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return err
	default:
		return fmt.Errorf("%w: run %s", types.ErrSignalsPending, runID)
	}
}

// Finish records the terminal state and releases the lease.
func (s *Runs) Finish(ctx context.Context, l stores.Lease, state stores.RunState, uncertain []types.CallKey, resultRef string) error {
	return s.finish(ctx, l, state, uncertain, resultRef, "")
}

// FinishWithReason records the stop reason with the terminal notice.
func (s *Runs) FinishWithReason(ctx context.Context, l stores.Lease, state stores.RunState, uncertain []types.CallKey, resultRef string, reason types.StopReason) error {
	return s.finish(ctx, l, state, uncertain, resultRef, reason)
}

func (s *Runs) suspend(ctx context.Context, l stores.Lease, t types.ResumeToken, reason types.SuspendReason) error {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("suspend run %s: %w", l.RunID, err)
	}
	defer tx.Rollback(ctx)

	r, err := scanRunRow(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM run_record WHERE run_id = $1 FOR UPDATE`, l.RunID))
	if errors.Is(err, pgx.ErrNoRows) {
		return runNotFound(l.RunID, "run")
	}
	if err != nil {
		return fmt.Errorf("suspend run %s: %w", l.RunID, err)
	}
	if err := s.held(r, l, s.db.clock()); err != nil {
		return fmt.Errorf("suspend: %w", err)
	}
	run, err := s.decode(ctx, r)
	if err != nil {
		return fmt.Errorf("suspend run %s: %w", l.RunID, err)
	}
	run.State = stores.Suspended
	r.state = stores.Suspended
	r.generation = 0
	r.leaseExpiry = nil
	token := string(t)
	r.token = &token
	if r.payload, err = s.encode(ctx, run); err != nil {
		return fmt.Errorf("suspend run %s: %w", l.RunID, err)
	}
	if err := s.persist(ctx, tx, r); err != nil {
		return fmt.Errorf("suspend run %s: %w", l.RunID, err)
	}
	if err := s.notic.append(ctx, txConn{tx}, s.tenant(ctx), run, stores.Suspended, "", reason); err != nil {
		return fmt.Errorf("suspend run %s: %w", l.RunID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("suspend run %s: %w", l.RunID, err)
	}
	return nil
}

// Suspend releases the lease and records the resume token; a suspended run
// is not recoverable until Resuming takes a fresh lease.
func (s *Runs) Suspend(ctx context.Context, l stores.Lease, t types.ResumeToken) error {
	return s.suspend(ctx, l, t, "")
}

// SuspendWithReason records the suspension reason with the notice.
func (s *Runs) SuspendWithReason(ctx context.Context, l stores.Lease, t types.ResumeToken, reason types.SuspendReason) error {
	return s.suspend(ctx, l, t, reason)
}

type noticeOutbox struct {
	db *DB
}

func (o *noticeOutbox) append(ctx context.Context, q executor, tenant string, run stores.Run, state stores.RunState, stop types.StopReason, suspend types.SuspendReason) error {
	kind, reason := noticeFor(state, stop, suspend)
	if _, err := q.Exec(ctx,
		`INSERT INTO run_notice (id, run_id, session_id, tenant, kind, reason, at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		noticeID(), run.RunID, run.SessionID, tenant, kind, reason, time.Now()); err != nil {
		return fmt.Errorf("append notice: %w", err)
	}
	return nil
}

func noticeID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return "nt-" + hex.EncodeToString(raw[:])
}

// Notices claims up to limit pending notices for the calling dispatcher for
// noticeClaimTTL; a claim another dispatcher still holds is skipped.
func (s *Runs) Notices(ctx context.Context, limit int) ([]types.RunNotice, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("list notices: %w", err)
	}
	defer tx.Rollback(ctx)

	now := s.db.clock()
	rows, err := tx.Query(ctx,
		`SELECT seq, id, run_id, session_id, tenant, kind, reason, at FROM run_notice
		 WHERE claim IS NULL OR claim <= $1 ORDER BY seq LIMIT $2`,
		now.Add(-noticeClaimTTL), limit)
	if err != nil {
		return nil, fmt.Errorf("list notices: %w", err)
	}
	type pending struct {
		seq int64
		n   types.RunNotice
	}
	var out []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.seq, &p.n.ID, &p.n.RunID, &p.n.SessionID, &p.n.Tenant, &p.n.Kind, &p.n.Reason, &p.n.At); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list notices: %w", err)
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list notices: %w", err)
	}
	claimed := make([]types.RunNotice, 0, len(out))
	for _, p := range out {
		if _, err := tx.Exec(ctx, `UPDATE run_notice SET claim = $1 WHERE seq = $2`, now, p.seq); err != nil {
			return nil, fmt.Errorf("claim notice %s: %w", p.n.ID, err)
		}
		claimed = append(claimed, p.n)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("list notices: %w", err)
	}
	return claimed, nil
}

// AckNotice removes one claimed notice after delivery.
func (s *Runs) AckNotice(ctx context.Context, id string) error {
	tag, err := s.db.pool.Exec(ctx, `DELETE FROM run_notice WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("ack notice %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: notice %s", stores.ErrRunNotFound, id)
	}
	return nil
}
