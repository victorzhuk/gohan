package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"sync"
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
			cycles = 1_000_000
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
WHERE n.nspname = $1 AND c.relkind IN ('r', 'v', 'p') AND NOT c.relispartition
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
LEFT JOIN pg_inherits i ON i.inhrelid = c.oid
LEFT JOIN pg_class p ON p.oid = i.inhparent
WHERE n.nspname = $1 AND NOT c.relispartition
ORDER BY coalesce(p.relname, c.relname), con.conname`, schema)
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
	irows, err := pool.Query(ctx, `
SELECT coalesce(parent.relname, tbl.relname) || ' index', pg_get_indexdef(c.oid)
FROM pg_class c
JOIN pg_index idx ON idx.indexrelid = c.oid
JOIN pg_class tbl ON tbl.oid = idx.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_inherits i ON i.inhrelid = tbl.oid
LEFT JOIN pg_class parent ON parent.oid = i.inhparent
WHERE n.nspname = $1 AND NOT tbl.relispartition
ORDER BY coalesce(parent.relname, tbl.relname), c.relname`, schema)
	if err != nil {
		return nil, err
	}
	for irows.Next() {
		var name, def string
		if err := irows.Scan(&name, &def); err != nil {
			irows.Close()
			return nil, err
		}
		dump[name] += normalizeIndexDef(def) + " "
	}
	irows.Close()
	if err := irows.Err(); err != nil {
		return nil, err
	}
	return dump, nil
}

var indexQualifierRe = regexp.MustCompile(`(\bON\s+(?:ONLY\s+)?)[A-Za-z_][A-Za-z0-9_]*\.`)

func normalizeIndexDef(def string) string {
	return indexQualifierRe.ReplaceAllString(def, "$1")
}

func bloatHarness(db *postgres.DB) storetest.BloatHarness {
	return func(ctx context.Context, cycles int) (storetest.BloatResult, error) {
		var autovacuum string
		if err := db.Pool().QueryRow(ctx, `SELECT setting FROM pg_settings WHERE name = 'autovacuum'`).Scan(&autovacuum); err != nil {
			return storetest.BloatResult{}, err
		}
		if autovacuum != "on" {
			return storetest.BloatResult{}, fmt.Errorf("autovacuum = %q, want on", autovacuum)
		}
		j := postgres.NewJournal(db)
		const workers = 8
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		perWorker := make([][]time.Duration, workers)
		var firstErr error
		var once sync.Once
		var wg sync.WaitGroup
		wg.Add(workers)
		for w := range workers {
			w := w
			go func() {
				defer wg.Done()
				share := make([]time.Duration, 0, cycles/workers+1)
				for i := w; i < cycles; i += workers {
					k := types.CallKey{SessionID: "bloat", CallID: fmt.Sprintf("c%d", i)}
					start := time.Now()
					if _, _, err := j.Reserve(ctx, k, "tool", "fp", json.RawMessage(`{}`)); err != nil {
						once.Do(func() {
							firstErr = err
							cancel()
						})
						return
					}
					share = append(share, time.Since(start))
					if err := j.Complete(ctx, k, types.ToolResult{ID: k.CallID}); err != nil {
						once.Do(func() {
							firstErr = err
							cancel()
						})
						return
					}
				}
				perWorker[w] = share
			}()
		}
		wg.Wait()
		if firstErr != nil {
			return storetest.BloatResult{}, firstErr
		}
		lat := make([]time.Duration, 0, cycles)
		for _, share := range perWorker {
			lat = append(lat, share...)
		}
		if len(lat) != cycles {
			return storetest.BloatResult{}, fmt.Errorf("successful cycles = %d, want %d", len(lat), cycles)
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
		if err := db.Pool().QueryRow(ctx, `SELECT coalesce(sum(s.n_dead_tup), 0) FROM pg_stat_user_tables s WHERE s.schemaname = $1 AND (s.relname = 'journal_record' OR s.relid IN (SELECT i.inhrelid FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhparent JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = 'journal_record'))`, db.Schema()).Scan(&dead); err != nil {
			return storetest.BloatResult{}, err
		}
		return storetest.BloatResult{Cycles: cycles, P99Latency: lat[(len(lat)*99)/100], DeadTuples: dead}, nil
	}
}
