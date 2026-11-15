package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/victorzhuk/gohan/core/stores"
)

const (
	DefaultReapBatchSize  = 100
	DefaultMaxReapBatches = 10
	reapGrace             = 2 * stores.LeaseTTL
)

// reapDeadRuns deletes runs the store clock proves dead: terminal rows
// past the retention age and non-terminal rows whose lease expired more
// than reapGrace ago. Suspended rows never qualify. Each batch takes a
// committed transaction, locking only the rows it deletes with SKIP
// LOCKED, so live workers holding run rows never block or get reaped.
func reapDeadRuns(
	ctx context.Context,
	conn *pgxpool.Conn,
	schema string,
	batch, maxBatches int,
	now time.Time,
	olderThan time.Duration,
) (int, error) {
	if batch <= 0 {
		batch = DefaultReapBatchSize
	}
	if maxBatches <= 0 {
		maxBatches = DefaultMaxReapBatches
	}
	terminal := now.Add(-olderThan)
	stale := now.Add(-reapGrace)
	total := 0
	for range maxBatches {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return total, fmt.Errorf("postgres reap: %w", err)
		}
		rows, err := tx.Query(ctx, fmt.Sprintf(`
WITH dead AS (
	SELECT run_id FROM %[1]s.run_record
	WHERE (state IN (3, 4) AND heartbeat_at < $1)
	   OR (state IN (0, 2) AND lease_expires IS NOT NULL AND lease_expires < $2)
	ORDER BY heartbeat_at
	LIMIT $3
	FOR UPDATE SKIP LOCKED)
DELETE FROM %[1]s.run_record USING dead
WHERE run_record.run_id = dead.run_id
RETURNING run_record.run_id`, quoteIdent(schema)),
			terminal, stale, batch)
		if err != nil {
			_ = tx.Rollback(ctx)
			return total, fmt.Errorf("postgres reap: %w", err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				_ = tx.Rollback(ctx)
				return total, fmt.Errorf("postgres reap: %w", err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			_ = tx.Rollback(ctx)
			return total, fmt.Errorf("postgres reap: %w", err)
		}
		rows.Close()
		// event_seq stays: the counter must outlive the run so a reused run id
		// never reuses a sequence number against the partitioned PK. event_compaction_publication
		// stays too: its tombstones are a publication-idempotence record, not run state.
		for _, table := range []string{"checkpoint_record", "run_notice", "run_signal"} {
			if _, err := tx.Exec(ctx, fmt.Sprintf(
				`DELETE FROM %[1]s.%[2]s WHERE run_id = ANY($1)`, quoteIdent(schema), table), ids); err != nil {
				_ = tx.Rollback(ctx)
				return total, fmt.Errorf("postgres reap: %w", err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return total, fmt.Errorf("postgres reap: %w", err)
		}
		total += len(ids)
		if len(ids) < batch {
			return total, nil
		}
	}
	return total, nil
}
