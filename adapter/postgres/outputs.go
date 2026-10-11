package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Outputs is the content-addressed blob store on Postgres: equal bytes
// yield one ref, and every Put claims the ref for the writing session.
//
// Claim lifetime: a ref is content-addressed, so refs carry no owner of
// their own. One output_claim row exists per (ref, session), and
// output_legacy_claim rows preserve owner claims from before migration
// 00005 for refs whose session attribution did not survive. A ref stays
// live while any claim exists; the last claim to drop deletes the bytes.
// Get is ref-keyed and carries no authorization: any caller holding a ref
// may read it.
type Outputs struct {
	db *DB
}

// NewOutputs builds the output store on db.
func NewOutputs(db *DB) *Outputs {
	return &Outputs{db: db}
}

func encodeOutput(content []types.Block) ([]byte, error) {
	return json.Marshal(types.Message{Blocks: content})
}

// Put encodes content canonically, stores it once under its content hash
// and records one claim for (ref, session). The bytes and the claim commit
// together under a ref-keyed transaction advisory lock, so a Put cannot
// interleave with the removal of the ref's last claim.
func (s *Outputs) Put(ctx context.Context, ri types.RunInfo, content []types.Block) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("output put: %w", err)
	}
	encoded, err := encodeOutput(content)
	if err != nil {
		return "", fmt.Errorf("output encode: %w", err)
	}
	sum := sha256.Sum256(encoded)
	ref := hex.EncodeToString(sum[:])
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("output put: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockRefsTx(ctx, tx, []string{ref}); err != nil {
		return "", fmt.Errorf("output put: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO output_record (ref, tenant, subject, content, payload) VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (ref) DO NOTHING`,
		ref, ri.Principal.Tenant, ri.Principal.Subject, encoded, encoded); err != nil {
		return "", fmt.Errorf("output put: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO output_claim (ref, session_id, tenant, subject) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (ref, session_id) DO NOTHING`,
		ref, ri.SessionID, ri.Principal.Tenant, ri.Principal.Subject); err != nil {
		return "", fmt.Errorf("output put: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("output put: %w", err)
	}
	return ref, nil
}

// Get returns a copy of the stored bytes for ref.
func (s *Outputs) Get(ctx context.Context, ref string) ([]byte, error) {
	var raw []byte
	err := s.db.pool.QueryRow(ctx,
		`SELECT payload FROM output_record WHERE ref = $1`, ref).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("output %s: %w", ref, stores.ErrOutputNotFound)
	}
	return slices.Clone(raw), nil
}

// EraseSubject drops the subject's claims from both claim ledgers and then
// deletes every referenced output_record with no claim left, reporting the
// refs actually removed. A ref another subject or session still claims
// survives. Rows written before migration 00005 fall back to their own
// (tenant, subject) scope on the output_record row.
func (s *Outputs) EraseSubject(ctx context.Context, tenant, subject string) (int, error) {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("erase outputs: %w", err)
	}
	defer tx.Rollback(ctx)
	refs, err := claimedRefsTx(ctx, tx,
		`SELECT ref FROM output_claim WHERE tenant = $1 AND subject = $2
		 UNION SELECT ref FROM output_legacy_claim WHERE tenant = $1 AND subject = $2`,
		tenant, subject)
	if err != nil {
		return 0, fmt.Errorf("erase outputs: %w", err)
	}
	if err := lockRefsTx(ctx, tx, refs); err != nil {
		return 0, fmt.Errorf("erase outputs: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM output_claim WHERE tenant = $1 AND subject = $2`, tenant, subject); err != nil {
		return 0, fmt.Errorf("erase outputs: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM output_legacy_claim WHERE tenant = $1 AND subject = $2`, tenant, subject); err != nil {
		return 0, fmt.Errorf("erase outputs: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`DELETE FROM output_record o
		 WHERE (o.tenant = $1 AND o.subject = $2)
		   AND NOT EXISTS (SELECT 1 FROM output_claim c WHERE c.ref = o.ref)
		   AND NOT EXISTS (SELECT 1 FROM output_legacy_claim l WHERE l.ref = o.ref)`,
		tenant, subject)
	if err != nil {
		return 0, fmt.Errorf("erase outputs: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("erase outputs: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// DeleteSessionDependents drops the session's output claims and the refs
// they were the last claim of, on the pool.
func (s *Outputs) DeleteSessionDependents(ctx context.Context, sessionID string) error {
	tx, err := s.db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("delete outputs of session %s: %w", sessionID, err)
	}
	defer tx.Rollback(ctx)
	if err := s.deleteSessionDependentsTx(ctx, tx, sessionID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("delete outputs of session %s: %w", sessionID, err)
	}
	return nil
}

// deleteSessionDependentsTx drops the session's output claims and the refs
// they were the last claim of, inside the caller's transaction.
func (s *Outputs) deleteSessionDependentsTx(ctx context.Context, tx pgx.Tx, sessionID string) error {
	refs, err := claimedRefsTx(ctx, tx,
		`SELECT ref FROM output_claim WHERE session_id = $1`, sessionID)
	if err != nil {
		return fmt.Errorf("delete outputs of session %s: %w", sessionID, err)
	}
	if err := lockRefsTx(ctx, tx, refs); err != nil {
		return fmt.Errorf("delete outputs of session %s: %w", sessionID, err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM output_claim WHERE session_id = $1`, sessionID); err != nil {
		return fmt.Errorf("delete outputs of session %s: %w", sessionID, err)
	}
	if err := deleteUnclaimedOutputTx(ctx, txConn{tx}); err != nil {
		return fmt.Errorf("delete outputs of session %s: %w", sessionID, err)
	}
	return nil
}

// CopySessionDependents gives the fork a claim on each ref the parent owns,
// so deleting the parent leaves the fork's refs live.
func (s *Outputs) CopySessionDependents(ctx context.Context, from, to string) error {
	if _, err := s.db.pool.Exec(ctx,
		`INSERT INTO output_claim (ref, session_id, tenant, subject)
		 SELECT ref, $2, tenant, subject FROM output_claim WHERE session_id = $1
		 ON CONFLICT (ref, session_id) DO NOTHING`,
		from, to); err != nil {
		return fmt.Errorf("copy outputs of session %s: %w", from, err)
	}
	return nil
}

// claimedRefsTx lists the distinct refs one claim query returns, sorted so
// the advisory locks that follow are always taken in the same order.
func claimedRefsTx(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]string, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Sort(refs)
	return refs, nil
}

// lockRefsTx takes a ref-keyed transaction advisory lock per ref in sorted
// order, so concurrent Put, erasure and cascade paths serialise per ref and
// cannot deadlock across ref sets.
func lockRefsTx(ctx context.Context, tx pgx.Tx, refs []string) error {
	for _, ref := range refs {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, ref); err != nil {
			return err
		}
	}
	return nil
}

// deleteUnclaimedOutputTx deletes every output_record with no claim left.
func deleteUnclaimedOutputTx(ctx context.Context, q executor) error {
	if _, err := q.Exec(ctx,
		`DELETE FROM output_record o
		 WHERE NOT EXISTS (SELECT 1 FROM output_claim c WHERE c.ref = o.ref)
		   AND NOT EXISTS (SELECT 1 FROM output_legacy_claim l WHERE l.ref = o.ref)`); err != nil {
		return err
	}
	return nil
}
