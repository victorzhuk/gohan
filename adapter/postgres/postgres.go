// Package postgres implements the gohan store ports on Postgres. A tool
// that needs its own row committed with a journal entry holds the pgx.Tx
// itself and calls Journal.CompleteTx on it.
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorzhuk/gohan/core/types"
)

// DefaultSchema is the schema every store table lives in unless
// WithSchema overrides it.
const DefaultSchema = "gohan"

// MinSchema is the lowest schema version this release reads. postgres.New
// refuses a database below it before any store query runs.
const MinSchema = 1

// Telemetry carries the observability hooks the adapter reports through.
type Telemetry struct {
	Logger *slog.Logger
	// SchemaAhead is called with the number of applied versions newer
	// than the code knows.
	SchemaAhead func(delta int)
}

// DB is the connection surface every store in this package is built from.
type DB struct {
	pool        *pgxpool.Pool
	schema      string
	clock       func() time.Time
	autoMigrate bool
	tel         Telemetry
}

// Option configures New.
type Option func(*dbConfig)

type dbConfig struct {
	schema      string
	clock       func() time.Time
	autoMigrate bool
	tel         Telemetry
	poolConfig  *pgxpool.Config
}

// WithSchema moves every table into the named schema. Two stacks on one
// database with different schemas see disjoint data.
func WithSchema(name string) Option {
	return func(c *dbConfig) { c.schema = name }
}

// WithAutoMigrate applies pending migrations at New. Development only: the
// stack builder must refuse it when the environment is production.
func WithAutoMigrate() Option {
	return func(c *dbConfig) { c.autoMigrate = true }
}

// WithClock replaces the store clock.
func WithClock(now func() time.Time) Option {
	return func(c *dbConfig) { c.clock = now }
}

// WithTelemetry wires the adapter's log and metric surface.
func WithTelemetry(t Telemetry) Option {
	return func(c *dbConfig) { c.tel = t }
}

// WithPool hands the adapter a prepared pgxpool config; the adapter applies
// its own AfterConnect search_path on top of it.
func WithPool(cfg *pgxpool.Config) Option {
	return func(c *dbConfig) { c.poolConfig = cfg }
}

// AutoMigrateEnabled reports whether the stack was built with
// WithAutoMigrate, so a production builder can refuse the stack.
func (db *DB) AutoMigrateEnabled() bool { return db.autoMigrate }

// Schema names the schema every store table lives in.
func (db *DB) Schema() string { return db.schema }

// Pool exposes the underlying pool for a caller that runs its own SQL
// beside the stores.
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// Close releases the pool.
func (db *DB) Close() { db.pool.Close() }

// Begin opens the caller-owned transaction a tool joins its own write to
// its journal completion in.
func (db *DB) Begin(ctx context.Context) (pgx.Tx, error) {
	return db.pool.Begin(ctx)
}

// New opens the pool, pins the search_path, and checks the applied schema
// version before any store query runs. A database below MinSchema fails
// with types.ErrSchemaTooOld; a database newer than the embedded
// migrations warns through gohan.postgres.schema_ahead and builds.
func New(ctx context.Context, dsn string, opts ...Option) (*DB, error) {
	c := dbConfig{schema: DefaultSchema, clock: time.Now}
	for _, o := range opts {
		o(&c)
	}
	poolCfg := c.poolConfig
	if poolCfg == nil {
		var err error
		poolCfg, err = pgxpool.ParseConfig(dsn)
		if err != nil {
			return nil, fmt.Errorf("postgres: parse dsn: %w", err)
		}
	}
	schema := c.schema
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET search_path TO "+quoteIdent(schema)+", public")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	db := &DB{pool: pool, schema: schema, clock: c.clock, autoMigrate: c.autoMigrate, tel: c.tel}
	if c.autoMigrate {
		if _, err := Migrate(ctx, pool, MigrateOptions{Schema: schema}); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := db.checkVersion(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) checkVersion(ctx context.Context) error {
	v, ok, err := appliedVersion(ctx, db.pool, db.schema)
	if err != nil {
		return err
	}
	if !ok || v < MinSchema {
		return fmt.Errorf("postgres: schema %s: %w", db.schema, types.ErrSchemaTooOld)
	}
	if ahead := v - headVersion; ahead > 0 {
		if db.tel.Logger != nil {
			db.tel.Logger.Warn("postgres schema is newer than this release", "metric", "gohan.postgres.schema_ahead", "ahead", ahead, "schema", db.schema)
		}
		if db.tel.SchemaAhead != nil {
			db.tel.SchemaAhead(int(ahead))
		}
	}
	return nil
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
