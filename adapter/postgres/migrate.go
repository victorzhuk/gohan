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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var postUp = map[int64]func(context.Context, pgx.Tx, string, time.Time) error{
	8: backfillPartitionedTables,
}

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

// srcHeadVersion is the highest migration version present in src.
func srcHeadVersion(src fs.FS) int64 {
	names, err := fs.Glob(src, "migrations/*.sql")
	if err != nil || len(names) == 0 {
		return 0
	}
	var head int64
	for _, name := range names {
		if v := fileVersion(name); v > head {
			head = v
		}
	}
	return head
}

// HeadVersion is the newest migration this module embeds.
func HeadVersion() int64 { return headVersion }

// ErrContractMigrationBlocked reports a migration whose up section carries
// a contract marker that the declared oldest reader does not satisfy.
var ErrContractMigrationBlocked = errors.New("gohan: contract migration blocked: readers older than required may still serve")

// MigrateOptions select the target of a Migrate run.
type MigrateOptions struct {
	Schema        string
	TargetVersion int64
	// MinReaderVersion is the oldest gohan reader still serving this
	// schema, declared by the operator. A migration whose up section
	// carries a contract after-reader marker above it is refused whole.
	MinReaderVersion int64
}

// Migrate applies the embedded migrations under a Postgres advisory lock
// so concurrent callers serialise and each version applies once. It
// returns the versions this call applied; an empty result means the schema
// was already at the target.
func Migrate(ctx context.Context, pool *pgxpool.Pool, opts MigrateOptions) ([]int64, error) {
	return migrateFS(ctx, pool, migrationsFS, opts)
}

// migrateFS applies the migrations read from src under the same advisory
// lock and version bookkeeping as Migrate.
func migrateFS(ctx context.Context, pool *pgxpool.Pool, src fs.FS, opts MigrateOptions) ([]int64, error) {
	schema := opts.Schema
	if schema == "" {
		schema = DefaultSchema
	}
	target := opts.TargetVersion
	if target == 0 {
		target = srcHeadVersion(src)
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
	files, err := fs.Glob(src, "migrations/*.sql")
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
		sql, err := fs.ReadFile(src, name)
		if err != nil {
			return nil, fmt.Errorf("postgres migrate: %w", err)
		}
		need, marked, err := contractAfterReader(string(sql))
		if err != nil {
			return nil, fmt.Errorf("postgres migrate: version %d: %w", v, err)
		}
		if marked && opts.MinReaderVersion < need {
			return nil, fmt.Errorf("postgres migrate: version %d needs readers >= %d, oldest declared %d: %w", v, need, opts.MinReaderVersion, ErrContractMigrationBlocked)
		}
		if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+quoteIdent(schema)); err != nil {
			return nil, fmt.Errorf("postgres migrate: search_path: %w", err)
		}
		if _, err := tx.Exec(ctx, upSection(string(sql))); err != nil {
			return nil, fmt.Errorf("postgres migrate: version %d: %w", v, err)
		}
		if hook := postUp[v]; hook != nil {
			var hookNow time.Time
			if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&hookNow); err != nil {
				return nil, fmt.Errorf("postgres migrate: version %d: %w", v, err)
			}
			if err := hook(ctx, tx, schema, hookNow); err != nil {
				return nil, fmt.Errorf("postgres migrate: version %d: %w", v, err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+quoteIdent(schema)+`.schema_migrations (version) VALUES ($1)`, v); err != nil {
			return nil, fmt.Errorf("postgres migrate: record version %d: %w", v, err)
		}
		applied = append(applied, v)
	}
	parted, err := journalPartitioned(ctx, tx, schema)
	if err != nil {
		return nil, err
	}
	if parted {
		var seedNow time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&seedNow); err != nil {
			return nil, fmt.Errorf("postgres migrate: clock: %w", err)
		}
		if _, err := ensurePartitionsTx(ctx, tx, schema, windowDays("journal_record", seedNow, DefaultPartitionWindowDays)); err != nil {
			return nil, fmt.Errorf("postgres migrate: seed partitions: %w", err)
		}
		if _, err := ensurePartitionsTx(ctx, tx, schema, windowDays("audit_record", seedNow, DefaultPartitionWindowDays)); err != nil {
			return nil, fmt.Errorf("postgres migrate: seed partitions: %w", err)
		}
		if _, err := ensurePartitionsTx(ctx, tx, schema, windowDays("event_record", seedNow, DefaultPartitionWindowDays)); err != nil {
			return nil, fmt.Errorf("postgres migrate: seed partitions: %w", err)
		}
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

var contractRe = regexp.MustCompile(`(?m)^\s*--\s*\+gohan contract after-reader (\d+)\s*$`)
var contractAnyRe = regexp.MustCompile(`(?m)^\s*--\s*\+gohan contract.*$`)

// contractAfterReader parses the reader-floor marker a contracting
// migration declares in its up section; the bool reports the marker
// is present. A line containing "+gohan contract" that does not match
// the strict form is an error, so a typo cannot silently disable the gate.
func contractAfterReader(sql string) (int64, bool, error) {
	up := upSection(sql)
	if m := contractRe.FindStringSubmatch(up); m != nil {
		v, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return 0, true, fmt.Errorf("postgres migrate: bad contract version %q: %w", m[1], err)
		}
		return v, true, nil
	}
	if line := contractAnyRe.FindString(up); line != "" {
		return 0, false, fmt.Errorf("malformed contract marker: %q: %w", line, ErrContractMigrationBlocked)
	}
	return 0, false, nil
}

// journalPartitioned reports whether journal_record is a partitioned
// table in schema, so partition seeding only runs where the parents exist.
func journalPartitioned(ctx context.Context, q execer, schema string) (bool, error) {
	var kind string
	err := q.QueryRow(ctx, `SELECT c.relkind::text FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = 'journal_record'`, schema).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("postgres migrate: relkind: %w", err)
	}
	return kind == "p", nil
}
