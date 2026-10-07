package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// headVersion is the newest migration this module embeds.
var headVersion = mustHeadVersion()

var versionRe = regexp.MustCompile(`^0*(\d+)_`)

func mustHeadVersion() int64 {
	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil || len(names) == 0 {
		panic("postgres: no migrations embedded")
	}
	sort.Strings(names)
	m := versionRe.FindStringSubmatch(path.Base(names[len(names)-1]))
	if m == nil {
		panic("postgres: migration file without a version prefix")
	}
	v, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		panic("postgres: bad migration version")
	}
	return v
}

// HeadVersion is the newest migration this module embeds.
func HeadVersion() int64 { return headVersion }

// MigrateOptions select the target of a Migrate run.
type MigrateOptions struct {
	Schema        string
	TargetVersion int64
}

// Migrate applies the embedded migrations under a Postgres advisory lock
// so concurrent callers serialise and each version applies once. It
// returns the versions this call applied; an empty result means the schema
// was already at the target.
func Migrate(ctx context.Context, pool *pgxpool.Pool, opts MigrateOptions) ([]int64, error) {
	schema := opts.Schema
	if schema == "" {
		schema = DefaultSchema
	}
	target := opts.TargetVersion
	if target == 0 {
		target = headVersion
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres migrate: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// One advisory lock per database and schema: concurrent pods wait
	// here, then find the versions already applied.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('gohan:' || $1))", schema); err != nil {
		return nil, fmt.Errorf("postgres migrate: advisory lock: %w", err)
	}
	if _, err := tx.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+quoteIdent(schema)); err != nil {
		return nil, fmt.Errorf("postgres migrate: schema: %w", err)
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+quoteIdent(schema)+`.schema_migrations (
	version bigint PRIMARY KEY,
	applied timestamptz NOT NULL DEFAULT now()
)`); err != nil {
		return nil, fmt.Errorf("postgres migrate: version table: %w", err)
	}
	var current int64
	err = tx.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM `+quoteIdent(schema)+`.schema_migrations`).Scan(&current)
	if err != nil {
		return nil, fmt.Errorf("postgres migrate: read version: %w", err)
	}
	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("postgres migrate: %w", err)
	}
	sort.Strings(files)
	var applied []int64
	for _, name := range files {
		v := fileVersion(name)
		if v <= current || v > target {
			continue
		}
		sql, err := migrationsFS.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("postgres migrate: %w", err)
		}
		if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+quoteIdent(schema)); err != nil {
			return nil, fmt.Errorf("postgres migrate: search_path: %w", err)
		}
		if _, err := tx.Exec(ctx, upSection(string(sql))); err != nil {
			return nil, fmt.Errorf("postgres migrate: version %d: %w", v, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+quoteIdent(schema)+`.schema_migrations (version) VALUES ($1)`, v); err != nil {
			return nil, fmt.Errorf("postgres migrate: record version %d: %w", v, err)
		}
		applied = append(applied, v)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres migrate: %w", err)
	}
	return applied, nil
}

// appliedVersion reports the newest applied migration version in schema.
func appliedVersion(ctx context.Context, pool *pgxpool.Pool, schema string) (int64, bool, error) {
	var v int64
	err := pool.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM `+quoteIdent(schema)+`.schema_migrations`).Scan(&v)
	if err != nil {
		if pgxConnErr(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("postgres: read schema version: %w", err)
	}
	return v, true, nil
}

func pgxConnErr(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "42P01" // undefined_table
}

func upSection(sql string) string {
	const up = "-- +goose Up"
	const down = "-- +goose Down"
	i := indexOf(sql, up)
	j := indexOf(sql, down)
	if j > i && i >= 0 {
		return sql[i+len(up) : j]
	}
	if i >= 0 {
		return sql[i+len(up):]
	}
	return sql
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func fileVersion(name string) int64 {
	m := versionRe.FindStringSubmatch(path.Base(name))
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseInt(m[1], 10, 64)
	return v
}
