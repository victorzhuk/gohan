package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"strings"

	"github.com/jackc/pgx/v5"
	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Stores bundles the store set one stack wires, so a service that depends
// on the adapter directly can run a subject erasure across all of them.
// Audit records are never touched: they remain as checksums.
type Stores struct {
	Sessions    *SessionLog
	Runs        *Runs
	Events      *EventLog
	Checkpoints *Checkpoints
	Journal     *Journal
	Outputs     *Outputs
	Notes       *Notes
	Redaction   *RedactionMaps
	Audit       *AuditLog
}

// EraseSubject removes every record the subject owns: its sessions with
// their dependent records, the sessions' runs, events, checkpoints,
// journal entries, outputs, notes and redaction maps. It returns the
// report of what was removed and which sessions a legal hold kept.
func (s *Stores) EraseSubject(ctx context.Context, tenant, subject string) (gohan.EraseReport, error) {
	report := gohan.EraseReport{}
	if s.Sessions == nil {
		return report, errors.New("erase subject: no session log wired")
	}
	refs, err := blobRefs(ctx, s.Sessions.db, tenant, subject)
	if err != nil {
		return report, err
	}
	report.BlobRefs = refs

	erased, err := s.Sessions.EraseSessions(ctx, tenant, subject)
	if err != nil {
		return report, err
	}
	report.Sessions = len(erased.Deleted)
	report.Held = erased.Held
	if s.Audit != nil {
		for _, id := range erased.Deleted {
			if err := s.Audit.Append(ctx, stores.AuditRecord{Kind: stores.AuditErasure, SessionID: id, Tenant: tenant, Subject: subject, Reason: "erased"}); err != nil {
				return report, fmt.Errorf("audit erasure for session %s: %w", id, err)
			}
		}
		for _, id := range erased.Held {
			if err := s.Audit.Append(ctx, stores.AuditRecord{Kind: stores.AuditErasure, SessionID: id, Tenant: tenant, Subject: subject, Reason: "held"}); err != nil {
				return report, fmt.Errorf("audit erasure for session %s: %w", id, err)
			}
		}
	}

	if s.Runs != nil {
		runIDs, err := s.Runs.EraseSessions(ctx, erased.Deleted)
		if err != nil {
			return report, fmt.Errorf("erase runs: %w", err)
		}
		if s.Events != nil {
			if _, err := s.Events.EraseRuns(ctx, runIDs); err != nil {
				return report, fmt.Errorf("erase events: %w", err)
			}
		}
	}
	if s.Checkpoints != nil {
		cps, err := s.Checkpoints.EraseSessions(ctx, erased.Deleted)
		if err != nil {
			return report, fmt.Errorf("erase checkpoints: %w", err)
		}
		_ = cps
	}
	if s.Journal != nil {
		n, err := s.Journal.EraseSessions(ctx, erased.Deleted)
		if err != nil {
			return report, fmt.Errorf("erase journal: %w", err)
		}
		report.Entries += n
	}
	if s.Outputs != nil {
		n, err := s.Outputs.EraseSubject(ctx, tenant, subject)
		if err != nil {
			return report, fmt.Errorf("erase outputs: %w", err)
		}
		report.Outputs = n
	}
	if s.Notes != nil {
		ids, err := s.Notes.EraseSubject(ctx, tenant, subject, erased.Deleted)
		if err != nil {
			return report, fmt.Errorf("erase notes: %w", err)
		}
		report.Memory = ids
	}
	if s.Redaction != nil {
		for _, sessionID := range erased.Deleted {
			if m, err := s.Redaction.Get(ctx, tenant, sessionID); err == nil {
				report.Entries += len(m.Entries)
			}
			if err := s.Redaction.Delete(ctx, tenant, sessionID); err != nil {
				return report, fmt.Errorf("erase redaction map for session %s: %w", sessionID, err)
			}
		}
	}
	return report, nil
}

// EraseSessions removes every session owned by the subject except the ones
// under a legal hold, which it reports untouched. A live run lease on an
// owned session fails the erasure with types.ErrRunActive.
func (s *SessionLog) EraseSessions(ctx context.Context, tenant, subject string) (stores.ErasedSessions, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return stores.ErasedSessions{}, fmt.Errorf("erase sessions: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`SELECT session_id, payload FROM session_record
		 WHERE owner->>'Tenant' = $1 AND owner->>'Subject' = $2`, tenant, subject)
	if err != nil {
		return stores.ErasedSessions{}, fmt.Errorf("erase sessions: %w", err)
	}
	out := stores.ErasedSessions{}
	for rows.Next() {
		var id string
		var payload []byte
		if err := rows.Scan(&id, &payload); err != nil {
			return stores.ErasedSessions{}, fmt.Errorf("erase sessions: %w", err)
		}
		meta, err := s.meta(ctx, &sessionRow{payload: payload})
		if err != nil {
			return stores.ErasedSessions{}, fmt.Errorf("erase session %s: %w", id, err)
		}
		if meta.Hold != "" {
			out.Held = append(out.Held, id)
			continue
		}
		if s.leases != nil && s.leases.SessionLeaseActive(ctx, id) {
			return stores.ErasedSessions{}, fmt.Errorf("erase session %s: %w", id, types.ErrRunActive)
		}
		out.Deleted = append(out.Deleted, id)
	}
	if err := rows.Err(); err != nil {
		return stores.ErasedSessions{}, fmt.Errorf("erase sessions: %w", err)
	}
	slices.Sort(out.Deleted)
	slices.Sort(out.Held)
	for _, id := range out.Deleted {
		if err := s.removeSession(ctx, tx, id); err != nil {
			return stores.ErasedSessions{}, fmt.Errorf("erase session %s: %w", id, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return stores.ErasedSessions{}, fmt.Errorf("erase sessions: %w", err)
	}
	return out, nil
}

// EraseSessions removes the sessions' run rows with their signals and
// notices, and reports the removed run ids.
func (s *Runs) EraseSessions(ctx context.Context, sessionIDs []string) ([]string, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("erase runs: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`DELETE FROM run_record WHERE session_id = ANY($1) RETURNING run_id`, sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("erase runs: %w", err)
	}
	var runIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("erase runs: %w", err)
		}
		runIDs = append(runIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("erase runs: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM run_signal WHERE run_id = ANY($1)`, runIDs); err != nil {
		return nil, fmt.Errorf("erase run signals: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM run_notice WHERE run_id = ANY($1)`, runIDs); err != nil {
		return nil, fmt.Errorf("erase run notices: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("erase runs: %w", err)
	}
	slices.Sort(runIDs)
	return runIDs, nil
}

// EraseRuns removes every event of the named runs and reports how many
// were removed.
func (s *EventLog) EraseRuns(ctx context.Context, runIDs []string) (int, error) {
	if len(runIDs) == 0 {
		return 0, nil
	}
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("erase events: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `DELETE FROM event_record WHERE run_id = ANY($1)`, runIDs)
	if err != nil {
		return 0, fmt.Errorf("erase events: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM event_compaction_publication WHERE run_id = ANY($1)`, runIDs); err != nil {
		return 0, fmt.Errorf("erase events: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM event_seq WHERE run_id = ANY($1)`, runIDs); err != nil {
		return 0, fmt.Errorf("erase event sequences: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("erase events: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// EraseSessions removes every checkpoint left for the named sessions and
// returns the removed checkpoints, so blob refs can be reported.
func (s *Checkpoints) EraseSessions(ctx context.Context, sessionIDs []string) ([]stores.Checkpoint, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("erase checkpoints: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`SELECT payload FROM checkpoint_record WHERE session_id = ANY($1)`, sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("erase checkpoints: %w", err)
	}
	var erased []stores.Checkpoint
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("erase checkpoints: %w", err)
		}
		cp, err := s.decode(ctx, &checkpointRow{payload: payload})
		if err != nil {
			return nil, fmt.Errorf("erase checkpoints: %w", err)
		}
		erased = append(erased, cp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("erase checkpoints: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM checkpoint_record WHERE session_id = ANY($1)`, sessionIDs); err != nil {
		return nil, fmt.Errorf("erase checkpoints: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("erase checkpoints: %w", err)
	}
	slices.SortFunc(erased, func(a, b stores.Checkpoint) int { return strings.Compare(a.RunID, b.RunID) })
	return erased, nil
}

// EraseSessions removes the journal entries recorded for the named
// sessions and reports how many were removed.
func (j *Journal) EraseSessions(ctx context.Context, sessionIDs []string) (int, error) {
	if len(sessionIDs) == 0 {
		return 0, nil
	}
	tag, err := j.db.pool.Exec(ctx, `DELETE FROM journal_record WHERE session_id = ANY($1)`, sessionIDs)
	if err != nil {
		return 0, fmt.Errorf("erase journal: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// EraseSubject drops the subject-scope notes and the erased sessions'
// session-scope notes.
func (s *Notes) EraseSubject(ctx context.Context, tenant, subject string, sessions []string) ([]string, error) {
	if _, err := s.db.pool.Exec(ctx,
		`DELETE FROM notes_record WHERE (scope = $1 AND tenant = $2 AND subject = $3)
		 OR (scope = $4 AND session = ANY($5))`,
		int(stores.ScopeSubject), tenant, subject, int(stores.ScopeSession), sessions); err != nil {
		return nil, fmt.Errorf("erase notes: %w", err)
	}
	return nil, nil
}

// deleteNotesDependents removes the session-scope notes of one session
// through q. The pool and transaction entries share it.
func deleteNotesDependents(ctx context.Context, q executor, sessionID string) error {
	if _, err := q.Exec(ctx,
		`DELETE FROM notes_record WHERE scope = $1 AND session = $2`,
		int(stores.ScopeSession), sessionID); err != nil {
		return fmt.Errorf("delete notes of session %s: %w", sessionID, err)
	}
	return nil
}

// DeleteSessionDependents drops the session-scope notes of one session on
// the pool, so registering the notes store as a session dependent carries
// them through the delete cascade.
func (s *Notes) DeleteSessionDependents(ctx context.Context, sessionID string) error {
	return deleteNotesDependents(ctx, poolConn{s.db.pool}, sessionID)
}

// deleteSessionDependentsTx drops the session-scope notes of one session
// inside the caller's transaction, so the session delete commits or rolls
// back with them.
func (s *Notes) deleteSessionDependentsTx(ctx context.Context, tx pgx.Tx, sessionID string) error {
	return deleteNotesDependents(ctx, txConn{tx}, sessionID)
}

// CopySessionDependents carries nothing: session-scope notes belong to the
// session that wrote them, so a fork starts with an empty notes map.
func (s *Notes) CopySessionDependents(ctx context.Context, from, to string) error {
	return nil
}

// Delete removes one tenant/session map; a missing map is already erased.
func (s *RedactionMaps) Delete(ctx context.Context, tenant, session string) error {
	if _, err := s.db.pool.Exec(ctx,
		`DELETE FROM redaction_map WHERE tenant = $1 AND session = $2`, tenant, session); err != nil {
		return fmt.Errorf("delete redaction map %s/%s: %w", tenant, session, err)
	}
	return nil
}

func blobRefs(ctx context.Context, db *DB, tenant, subject string) ([]string, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT b.blob_ref FROM session_blob b
		 JOIN session_record s ON s.session_id = b.session_id
		 WHERE s.owner->>'Tenant' = $1 AND s.owner->>'Subject' = $2
		 ORDER BY b.blob_ref`, tenant, subject)
	if err != nil {
		return nil, fmt.Errorf("list blob refs: %w", err)
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("list blob refs: %w", err)
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
