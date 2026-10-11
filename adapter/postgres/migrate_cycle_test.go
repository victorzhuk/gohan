//go:build full

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorzhuk/gohan/adapter/postgres"
)

// downFile pairs one migration version with its down section. A migration
// without a down marker reverses to nothing, so the cycle asserts on
// table sets, not a byte-equal empty schema.
type downFile struct {
	version int64
	sql     string
}

func downFiles(t *testing.T) []downFile {
	t.Helper()
	names, err := filepath.Glob(filepath.Join("migrations", "*.sql"))
	if err != nil || len(names) == 0 {
		t.Fatalf("read migrations: %v", err)
	}
	sort.Strings(names)
	var out []downFile
	for _, name := range names {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var script string
		if i := strings.Index(string(raw), "-- +goose Down"); i >= 0 {
			script = string(raw[i+len("-- +goose Down"):])
		}
		var version int64
		fmt.Sscanf(filepath.Base(name), "%d", &version)
		out = append(out, downFile{version: version, sql: script})
	}
	// Reverse order: a down run unwinds newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func runDown(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string, files []downFile) {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	// The migration up sections ran under SET LOCAL search_path inside
	// the migrate transaction; the down run pins the same path for the
	// whole sweep on one connection.
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	for _, f := range files {
		for _, stmt := range strings.Split(f.sql, ";") {
			var lines []string
			for _, line := range strings.Split(stmt, "\n") {
				if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "--") {
					lines = append(lines, t)
				}
			}
			if len(lines) == 0 {
				continue
			}
			if _, err := conn.Exec(ctx, strings.Join(lines, "\n")); err != nil {
				t.Fatalf("down version %d: %v", f.version, err)
			}
		}
	}
	if _, err := conn.Exec(ctx, "TRUNCATE schema_migrations"); err != nil {
		t.Fatalf("reset schema_migrations: %v", err)
	}
}

func schemaTables(ctx context.Context, pool *pgxpool.Pool, schema string) ([]string, error) {
	rows, err := pool.Query(ctx, `
SELECT c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind = 'r'
ORDER BY c.relname`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func TestMigrationCycle(t *testing.T) {
	t.Run("stores.pg-migration-down-up-down", func(t *testing.T) {
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, dsn(t))
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer pool.Close()
		stamp := time.Now().UnixNano()
		files := downFiles(t)

		// Fresh install at head.
		freshSchema := fmt.Sprintf("cyc_fresh_%d", stamp)
		if _, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: freshSchema}); err != nil {
			t.Fatalf("fresh install: %v", err)
		}
		fresh, err := schemaDump(ctx, pool, freshSchema)
		if err != nil {
			t.Fatalf("dump fresh: %v", err)
		}

		// Down every version, then up again: the re-up applies the
		// full version range and lands byte-equal with the fresh
		// install.
		downSchema := fmt.Sprintf("cyc_down_%d", stamp)
		if _, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: downSchema}); err != nil {
			t.Fatalf("install for down: %v", err)
		}
		runDown(t, ctx, pool, downSchema, files)
		var current int64
		if err := pool.QueryRow(ctx,
			`SELECT coalesce(max(version), 0) FROM `+downSchema+`.schema_migrations`).Scan(&current); err != nil || current != 0 {
			t.Fatalf("version after down = (%d, %v), want (0, nil)", current, err)
		}
		applied, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: downSchema})
		if err != nil {
			t.Fatalf("re-up: %v", err)
		}
		if len(applied) != len(files) {
			t.Fatalf("re-up applied %v, want every version of %d", applied, len(files))
		}
		reup, err := schemaDump(ctx, pool, downSchema)
		if err != nil {
			t.Fatalf("dump re-up: %v", err)
		}
		if len(reup) != len(fresh) {
			t.Fatalf("re-up objects = %d, fresh = %d", len(reup), len(fresh))
		}
	// Constraint definitions qualify the FK target with the install's
	// own schema, so the comparison strips the qualifier and compares
	// field sets, the same way the fresh-equals-upgraded comparison
	// does.
	for k, def := range fresh {
		if normalizeDef(def) != normalizeDef(reup[k]) {
			t.Fatalf("%s drifted after a down/up cycle:\n fresh: %s\n re-up: %s", k, normalizeDef(def), normalizeDef(reup[k]))
		}
	}

	// Second down: every versioned table is gone, only the
	// version bookkeeping survives.
		runDown(t, ctx, pool, downSchema, files)
		left, err := schemaTables(ctx, pool, downSchema)
		if err != nil {
			t.Fatalf("list tables after final down: %v", err)
		}
		if len(left) != 1 || left[0] != "schema_migrations" {
			t.Fatalf("tables after final down = %v, want [schema_migrations]", left)
		}
	})
}

var schemaQualifierRe = regexp.MustCompile(`(REFERENCES\s+)[A-Za-z_][A-Za-z0-9_]*\.`)

func normalizeDef(def string) string {
	fields := strings.Fields(schemaQualifierRe.ReplaceAllString(def, "$1"))
	sort.Strings(fields)
	return strings.Join(fields, " ")
}
