package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/victorzhuk/gohan/core/types"
)

// execer is the DDL surface a migration transaction, a pool connection and
// a plain transaction all share, so partition statements run unchanged on
// each.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type dayBound struct {
	Name string
	From string
	To   string
}

func partitionName(table string, day time.Time) string {
	d := day.UTC()
	return fmt.Sprintf("%s_p%04d%02d%02d", table, d.Year(), int(d.Month()), d.Day())
}

func dayBoundFor(table string, day time.Time) dayBound {
	from := day.UTC()
	to := from.AddDate(0, 0, 1)
	return dayBound{
		Name: partitionName(table, from),
		From: from.Format(time.RFC3339),
		To:   to.Format(time.RFC3339),
	}
}

func windowDays(table string, from time.Time, days int) []dayBound {
	start := time.Date(from.UTC().Year(), from.UTC().Month(), from.UTC().Day(), 0, 0, 0, 0, time.UTC)
	out := make([]dayBound, 0, days)
	for i := range days {
		out = append(out, dayBoundFor(table, start.AddDate(0, 0, i)))
	}
	return out
}

func backfillDays(table string, minDay, maxDay, now time.Time, window int) []dayBound {
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	start := minDay.UTC()
	if today.Before(start) {
		start = today
	}
	end := maxDay.UTC()
	if last := today.AddDate(0, 0, window-1); last.After(end) {
		end = last
	}
	var out []dayBound
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		out = append(out, dayBoundFor(table, d))
	}
	return out
}

func relMissing(ctx context.Context, q execer, qualified string) (bool, error) {
	var missing bool
	if err := q.QueryRow(ctx, `SELECT to_regclass($1) IS NULL`, qualified).Scan(&missing); err != nil {
		return false, fmt.Errorf("postgres partition: %w", err)
	}
	return missing, nil
}

func ensurePartitionsTx(ctx context.Context, q execer, schema string, days []dayBound) ([]string, error) {
	var created []string
	for _, d := range days {
		qualified := schema + "." + d.Name
		missing, err := relMissing(ctx, q, qualified)
		if err != nil {
			return nil, err
		}
		if !missing {
			continue
		}
		table, _, _ := strings.Cut(d.Name, "_p")
		if _, err := q.Exec(ctx, fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s.%s PARTITION OF %s.%s FOR VALUES FROM ('%s') TO ('%s')`,
			quoteIdent(schema), quoteIdent(d.Name), quoteIdent(schema), quoteIdent(table), d.From, d.To,
		)); err != nil {
			return nil, fmt.Errorf("postgres partition: %w", err)
		}
		created = append(created, d.Name)
	}
	return created, nil
}

func dropStagingTable(ctx context.Context, q execer, schema, table string) error {
	if _, err := q.Exec(ctx, `DROP TABLE IF EXISTS `+quoteIdent(schema)+"."+quoteIdent(table+"_v7")); err != nil {
		return fmt.Errorf("postgres backfill: %w", err)
	}
	return nil
}

func backfillTable(ctx context.Context, tx pgx.Tx, schema, table, cols string, now time.Time, window int) error {
	staging := quoteIdent(schema) + "." + quoteIdent(table+"_v7")
	missing, err := relMissing(ctx, tx, schema+"."+table+"_v7")
	if err != nil {
		return err
	}
	if missing {
		return nil
	}
	var minDay, maxDay *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT date_trunc('day', min(at) AT TIME ZONE 'utc'), date_trunc('day', max(at) AT TIME ZONE 'utc') FROM `+staging,
	).Scan(&minDay, &maxDay); err != nil {
		return fmt.Errorf("postgres backfill: %s: %w", table, err)
	}
	if minDay != nil && maxDay != nil {
		if _, err := ensurePartitionsTx(ctx, tx, schema, backfillDays(table, *minDay, *maxDay, now, window)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO `+quoteIdent(schema)+"."+quoteIdent(table)+` (`+cols+`)
			 SELECT `+cols+` FROM `+staging,
		); err != nil {
			return fmt.Errorf("postgres backfill: %s: %w", table, mapPartitionErr(err))
		}
	}
	return dropStagingTable(ctx, tx, schema, table)
}

func backfillPartitionedTables(ctx context.Context, tx pgx.Tx, schema string, now time.Time) error {
	const journalCols = `session_id, call_id, state, fingerprint, tool, args, result, at, attempts, endpoint, expires_at, payload`
	const auditCols = `session_id, seq, kind, at, tenant, prev_hash, hash, payload, publication_id`
	const eventCols = `run_id, seq, kind, at, payload`
	if err := backfillTable(ctx, tx, schema, "journal_record", journalCols, now, DefaultPartitionWindowDays); err != nil {
		return err
	}
	if err := backfillTable(ctx, tx, schema, "audit_record", auditCols, now, DefaultPartitionWindowDays); err != nil {
		return err
	}
	return backfillTable(ctx, tx, schema, "event_record", eventCols, now, DefaultPartitionWindowDays)
}

func mapPartitionErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && strings.HasPrefix(pgErr.Message, "no partition of relation") {
		return fmt.Errorf("postgres: %w (%s)", types.ErrPartitionMissing, pgErr.Message)
	}
	return err
}
