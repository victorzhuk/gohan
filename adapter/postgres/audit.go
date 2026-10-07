package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/victorzhuk/gohan/core/stores"
)

// AuditLog is the append-only decision trail on Postgres with the
// per-session hash chain computed inside Append.
type AuditLog struct {
	db      *DB
	schemas *stores.SchemaRegistry
}

// NewAuditLog builds the audit log on db.
func NewAuditLog(db *DB) *AuditLog {
	return &AuditLog{db: db, schemas: stores.NewSchemaRegistry()}
}

// Append records r at the store clock's instant: PrevHash links the
// session's previous record and Hash seals the record.
func (a *AuditLog) Append(ctx context.Context, r stores.AuditRecord) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("audit append: %w", err)
	}
	rec := r
	rec.At = a.db.clock()
	var prev string
	err := a.db.pool.QueryRow(ctx,
		`SELECT hash FROM audit_record WHERE session_id = $1 ORDER BY seq DESC LIMIT 1`,
		rec.SessionID).Scan(&prev)
	switch {
	case err == nil:
		rec.PrevHash = prev
	case errors.Is(err, pgx.ErrNoRows):
		rec.PrevHash = ""
	default:
		return fmt.Errorf("audit append: %w", err)
	}
	rec.Hash = auditHash(rec)
	raw, err := encodeRecord("AuditRecord", rec)
	if err != nil {
		return err
	}
	if _, err := a.db.pool.Exec(ctx,
		`INSERT INTO audit_record (session_id, seq, kind, at, tenant, prev_hash, hash, payload)
		 SELECT $1, COALESCE(MAX(seq), 0) + 1, $2, $3, $4, $5, $6, $7 FROM audit_record WHERE session_id = $1`,
		rec.SessionID, string(rec.Kind), rec.At, rec.Tenant, rec.PrevHash, rec.Hash, raw); err != nil {
		return fmt.Errorf("audit append: %w", err)
	}
	return nil
}

// Read yields the session's records in append order, decoded through the
// schema registry.
func (a *AuditLog) Read(ctx context.Context, sessionID string) iter.Seq2[stores.AuditRecord, error] {
	return func(yield func(stores.AuditRecord, error) bool) {
		rows, err := a.db.pool.Query(ctx,
			`SELECT payload FROM audit_record WHERE session_id = $1 ORDER BY seq`, sessionID)
		if err != nil {
			yield(stores.AuditRecord{}, fmt.Errorf("audit read: %w", err))
			return
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				yield(stores.AuditRecord{}, fmt.Errorf("audit read: %w", err))
				return
			}
			rec, err := decode[stores.AuditRecord](ctx, a.schemas, raw)
			if err != nil {
				yield(stores.AuditRecord{}, err)
				return
			}
			if !yield(rec, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(stores.AuditRecord{}, fmt.Errorf("audit read: %w", err))
		}
	}
}

// Purge deletes the tenant's records older than the given instant, the only
// delete on the log, and reseals the surviving chains.
func (a *AuditLog) Purge(ctx context.Context, tenant string, olderThan time.Time) (int, error) {
	tag, err := a.db.pool.Exec(ctx,
		`DELETE FROM audit_record WHERE tenant = $1 AND at < $2`, tenant, olderThan)
	if err != nil {
		return 0, fmt.Errorf("audit purge: %w", err)
	}
	removed := int(tag.RowsAffected())
	if removed == 0 {
		return 0, nil
	}
	sessions, err := a.db.pool.Query(ctx,
		`SELECT DISTINCT session_id FROM audit_record WHERE tenant = $1`, tenant)
	if err != nil {
		return 0, fmt.Errorf("audit purge: %w", err)
	}
	var ids []string
	for sessions.Next() {
		var id string
		if err := sessions.Scan(&id); err != nil {
			sessions.Close()
			return 0, fmt.Errorf("audit purge: %w", err)
		}
		ids = append(ids, id)
	}
	if err := sessions.Err(); err != nil {
		return 0, fmt.Errorf("audit purge: %w", err)
	}
	sessions.Close()
	for _, id := range ids {
		if err := a.reseal(ctx, id); err != nil {
			return 0, err
		}
	}
	return removed, nil
}

// reseal recomputes the chain over a session's surviving records, oldest
// first, and persists the resealed envelopes.
func (a *AuditLog) reseal(ctx context.Context, sessionID string) error {
	rows, err := a.db.pool.Query(ctx,
		`SELECT seq, payload FROM audit_record WHERE session_id = $1 ORDER BY seq`, sessionID)
	if err != nil {
		return fmt.Errorf("audit reseal: %w", err)
	}
	type resealed struct {
		seq     int64
		prev    string
		hash    string
		payload []byte
	}
	var out []resealed
	var prev string
	for rows.Next() {
		var seq int64
		var raw []byte
		if err := rows.Scan(&seq, &raw); err != nil {
			rows.Close()
			return fmt.Errorf("audit reseal: %w", err)
		}
		rec, err := decode[stores.AuditRecord](ctx, a.schemas, raw)
		if err != nil {
			rows.Close()
			return err
		}
		rec.PrevHash = prev
		rec.Hash = auditHash(rec)
		payload, err := encodeRecord("AuditRecord", rec)
		if err != nil {
			rows.Close()
			return err
		}
		out = append(out, resealed{seq: seq, prev: rec.PrevHash, hash: rec.Hash, payload: payload})
		prev = rec.Hash
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("audit reseal: %w", err)
	}
	rows.Close()

	tx, err := a.db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("audit reseal: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, r := range out {
		if _, err := tx.Exec(ctx,
			`UPDATE audit_record SET prev_hash = $1, hash = $2, payload = $3 WHERE session_id = $4 AND seq = $5`,
			r.prev, r.hash, r.payload, sessionID, r.seq); err != nil {
			return fmt.Errorf("audit reseal: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("audit reseal: %w", err)
	}
	return nil
}

// auditHash seals the record: SHA-256 over the previous hash and the
// record's fields in a fixed order, excluding Hash itself.
func auditHash(r stores.AuditRecord) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%d\n%s\n%s\n%s\n%s\n%s\n%d\n%s\n%d\n%d\n%s\n%s\n%s\n%s\n",
		r.Kind, r.At.UTC().Format(time.RFC3339Nano), r.SessionID, r.RunID, r.RootRunID,
		r.Flow, r.Subject, r.Tenant, r.Tool, int(r.Effect), r.ArgsChecksum, r.Decision,
		r.Approver, strconv.FormatFloat(r.Confidence, 'b', -1, 64), r.Verdict, int(r.Outcome), r.ResultSHA,
		r.ResultBytes, int(r.Stage), r.Model, r.ModelVersion, r.ManifestHash, r.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}
