package postgres

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const DefaultPartitionWindowDays = 14

// MaintainOptions select the window and retention pass of a Maintain run.
type MaintainOptions struct {
	Schema         string
	WindowDays     int           // 0 → DefaultPartitionWindowDays
	Retention      time.Duration // 0 → drop nothing and reap nothing
	ReapBatchSize  int           // 0 → DefaultReapBatchSize
	MaxReapBatches int           // 0 → DefaultMaxReapBatches
	Now            func() time.Time
}

// MaintainReport names what one Maintain run created and dropped.
type MaintainReport struct {
	Created []string
	Dropped []string
	Reaped  int
}

// Maintain ensures the rolling partition window and drops partitions past
// retention. The advisory lock is the same key Migrate takes, so partition
// DDL never runs against a schema a migration is reshaping.
func Maintain(ctx context.Context, pool *pgxpool.Pool, opts MaintainOptions) (report MaintainReport, err error) {
	schema := opts.Schema
	if schema == "" {
		schema = DefaultSchema
	}
	window := opts.WindowDays
	if window <= 0 {
		window = DefaultPartitionWindowDays
	}
	nowFn := opts.Now
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return MaintainReport{}, fmt.Errorf("postgres maintain: %w", err)
	}
	defer conn.Release()
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var unlocked bool
		if qerr := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock(hashtext('gohan:' || $1))`, schema).Scan(&unlocked); qerr != nil {
			if err == nil {
				err = fmt.Errorf("postgres maintain: unlock: %w", qerr)
			}
			return
		}
		if !unlocked && err == nil {
			err = fmt.Errorf("postgres maintain: unlock: lock not held")
		}
	}()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('gohan:' || $1))`, schema); err != nil {
		return MaintainReport{}, fmt.Errorf("postgres maintain: advisory lock: %w", err)
	}
	var now time.Time
	if nowFn != nil {
		now = nowFn()
	} else if err := conn.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return report, fmt.Errorf("postgres maintain: clock: %w", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return report, fmt.Errorf("postgres maintain: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var createdAll []string
	created, err := ensurePartitionsTx(ctx, tx, schema, windowDays("journal_record", now, window))
	if err != nil {
		return report, err
	}
	createdAll = append(createdAll, created...)
	created2, err := ensurePartitionsTx(ctx, tx, schema, windowDays("audit_record", now, window))
	if err != nil {
		return report, err
	}
	createdAll = append(createdAll, created2...)
	created3, err := ensurePartitionsTx(ctx, tx, schema, windowDays("event_record", now, window))
	if err != nil {
		return report, err
	}
	createdAll = append(createdAll, created3...)
	if opts.Retention > 0 {
		dropped, err := dropRetainedPartitions(ctx, tx, schema, now.Add(-opts.Retention))
		if err != nil {
			return report, err
		}
		report.Dropped = dropped
	}
	if err := tx.Commit(ctx); err != nil {
		return report, fmt.Errorf("postgres maintain: %w", err)
	}
	report.Created = createdAll
	if opts.Retention > 0 {
		reaped, err := reapDeadRuns(ctx, conn, schema, opts.ReapBatchSize, opts.MaxReapBatches, now, opts.Retention)
		report.Reaped = reaped
		if err != nil {
			return report, err
		}
	}
	return report, nil
}

// partitionDay reads the UTC day a gohan-created partition name encodes;
// a partition whose name does not match is never a retention candidate.
func partitionDay(name string) (time.Time, bool) {
	_, rest, ok := strings.Cut(name, "_p")
	if !ok || len(rest) != 8 {
		return time.Time{}, false
	}
	y, errY := strconv.Atoi(rest[:4])
	m, errM := strconv.Atoi(rest[4:6])
	d, errD := strconv.Atoi(rest[6:8])
	if errY != nil || errM != nil || errD != nil {
		return time.Time{}, false
	}
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC), true
}

// dropRetainedPartitions drops whole partitions whose upper bound (the
// day after the name encodes) is at or before the retention cutoff,
// ascending by bound, with zero row-level DELETE.
func dropRetainedPartitions(ctx context.Context, tx pgx.Tx, schema string, cutoff time.Time) ([]string, error) {
	var all []string
	for _, parent := range []string{"journal_record", "audit_record", "event_record"} {
		rows, err := tx.Query(ctx, `
SELECT c.relname FROM pg_inherits i
JOIN pg_class c ON c.oid = i.inhrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE i.inhparent = ($1 || '.' || $2)::regclass AND n.nspname = $1`,
			schema, parent)
		if err != nil {
			return nil, fmt.Errorf("postgres maintain: %w", err)
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return nil, fmt.Errorf("postgres maintain: %w", err)
			}
			day, ok := partitionDay(name)
			if ok && !day.AddDate(0, 0, 1).After(cutoff) {
				all = append(all, name)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres maintain: %w", err)
		}
		rows.Close()
	}
	sort.Slice(all, func(i, j int) bool {
		a, _ := partitionDay(all[i])
		b, _ := partitionDay(all[j])
		return a.Before(b)
	})
	for _, name := range all {
		if _, err := tx.Exec(ctx, `DROP TABLE `+quoteIdent(schema)+"."+quoteIdent(name)); err != nil {
			return nil, fmt.Errorf("postgres maintain: drop %s: %w", name, err)
		}
	}
	return all, nil
}
