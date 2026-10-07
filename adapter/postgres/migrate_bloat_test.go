package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/storetest"
)

func TestMigrations(t *testing.T) {
	t.Run("stores.pg-fresh-equals-upgraded", func(t *testing.T) {
		storetest.Migrations(t, migrationsHarness(t))
	})

	t.Run("stores.bloat-bound", func(t *testing.T) {
		db := newDB(t, fmt.Sprintf("bloat_%d", time.Now().UnixNano()))
		cycles := 1_000
		if !testing.Short() {
			cycles = 20_000
		}
		storetest.Bloat(t, bloatHarness(db), cycles, 500*time.Millisecond, int64(2*cycles+64))
	})
}

func migrationsHarness(t *testing.T) storetest.MigrationsHarness {
	t.Helper()
	return func(ctx context.Context, target storetest.SchemaTarget) (storetest.SchemaDump, error) {
		pool, err := pgxpool.New(ctx, dsn(t))
		if err != nil {
			return nil, err
		}
		defer pool.Close()
		schema := fmt.Sprintf("mig_%d_%d", target.FromVersion, time.Now().UnixNano())
		if target.FromVersion > 0 {
			for v := target.FromVersion; v <= postgres.HeadVersion(); v++ {
				if _, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: schema, TargetVersion: v}); err != nil {
					return nil, err
				}
			}
		} else if _, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: schema}); err != nil {
			return nil, err
		}
		dump, err := schemaDump(ctx, pool, schema)
		if err != nil {
			return nil, err
		}
		if len(dump) == 0 {
			return nil, fmt.Errorf("schema %s has no dumped objects", schema)
		}
		return dump, nil
	}
}

func schemaDump(ctx context.Context, pool *pgxpool.Pool, schema string) (storetest.SchemaDump, error) {
	dump := storetest.SchemaDump{}
	rows, err := pool.Query(ctx, `
SELECT c.relname,
       a.attname || ' ' || format_type(a.atttypid, a.atttypmod) ||
       CASE WHEN a.attnotnull THEN ' not null' ELSE '' END
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid
WHERE n.nspname = $1 AND c.relkind IN ('r', 'v') AND NOT c.relispartition
  AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY c.relname, a.attnum`, schema)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			rows.Close()
			return nil, err
		}
		dump[name] += def + " "
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	crows, err := pool.Query(ctx, `
SELECT c.relname, pg_get_constraintdef(con.oid)
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1
ORDER BY c.relname, con.conname`, schema)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var name, def string
		if err := crows.Scan(&name, &def); err != nil {
			crows.Close()
			return nil, err
		}
		dump[name+" constraint"] += def + " "
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}
	return dump, nil
}

func bloatHarness(db *postgres.DB) storetest.BloatHarness {
	return func(ctx context.Context, cycles int) (storetest.BloatResult, error) {
		j := postgres.NewJournal(db)
		lat := make([]time.Duration, 0, cycles)
		for i := range cycles {
			k := types.CallKey{SessionID: "bloat", CallID: fmt.Sprintf("c%d", i)}
			start := time.Now()
			if _, _, err := j.Reserve(ctx, k, "tool", "fp", json.RawMessage(`{}`)); err != nil {
				return storetest.BloatResult{}, err
			}
			lat = append(lat, time.Since(start))
			if err := j.Complete(ctx, k, types.ToolResult{ID: k.CallID}); err != nil {
				return storetest.BloatResult{}, err
			}
		}
		sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
		var stored int
		if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM journal_record WHERE session_id = 'bloat'`).Scan(&stored); err != nil {
			return storetest.BloatResult{}, err
		}
		if stored != cycles {
			return storetest.BloatResult{}, fmt.Errorf("journal rows = %d, want %d", stored, cycles)
		}
		var dead int64
		if err := db.Pool().QueryRow(ctx, `SELECT coalesce(n_dead_tup, 0) FROM pg_stat_user_tables WHERE schemaname = $1 AND relname = 'journal_record'`, db.Schema()).Scan(&dead); err != nil {
			return storetest.BloatResult{}, err
		}
		return storetest.BloatResult{Cycles: cycles, P99Latency: lat[(len(lat)*99)/100], DeadTuples: dead}, nil
	}
}
