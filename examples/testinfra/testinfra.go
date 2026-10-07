//go:build full

// Package testinfra starts the shared Postgres test container the
// examples' S2 suites run against.
package testinfra

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

var (
	once       sync.Once
	container  *tcpostgres.PostgresContainer
	dsnCache   string
	startErr   error
)

func waitReady(ctx context.Context, dsn string) error {
	deadline := time.Now().Add(60 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			err = pool.Ping(ctx)
			pool.Close()
		}
		if err == nil {
			return nil
		}
		last = err
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("postgres container never accepted connections: %w", last)
}

// DSN returns the shared container's connection string, starting it on
// first use and terminating it when the starting test ends.
func DSN(t *testing.T) string {
	t.Helper()
	once.Do(func() {
		ctx := context.Background()
		ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("gohan"),
			tcpostgres.WithUsername("gohan"),
			tcpostgres.WithPassword("gohan"),
		)
		if err != nil {
			startErr = err
			return
		}
		dsnCache, err = ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			startErr = err
			return
		}
		startErr = waitReady(ctx, dsnCache)
		container = ctr
		// ryuk reaps the container when the test process exits; a
		// per-test cleanup would kill it for later tests.
	})
	if startErr != nil {
		t.Fatalf("postgres container: %v", startErr)
	}
	return dsnCache
}

// Stores carries the postgres handle and the store ports one S2
// conversation binds.
type Stores struct {
	DB          *postgres.DB
	SessionLog  *postgres.SessionLog
	Runs        *postgres.Runs
	EventLog    *postgres.EventLog
	Checkpoints *postgres.Checkpoints
	Journal     *postgres.Journal
}

// Ports returns the stack-level Stores value.
func (s Stores) Ports() stores.Stores {
	return stores.Stores{
		SessionLog:  s.SessionLog,
		Runs:        s.Runs,
		EventLog:    s.EventLog,
		Checkpoints: s.Checkpoints,
		Journal:     s.Journal,
	}
}

// NewStores opens a migrated schema and builds every store the S2
// suites need on it.
func NewStores(t *testing.T, schema string) Stores {
	t.Helper()
	db, err := postgres.New(context.Background(), DSN(t),
		postgres.WithSchema(schema),
		postgres.WithAutoMigrate(),
	)
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	t.Cleanup(db.Close)
	return Stores{
		DB:          db,
		SessionLog:  postgres.NewSessionLog(db, postgres.WithSessionPrincipals(types.PrincipalFrom)),
		Runs:        postgres.NewRuns(db),
		EventLog:    postgres.NewEventLog(db),
		Checkpoints: postgres.NewCheckpoints(db),
		Journal:     postgres.NewJournal(db),
	}
}

// Pool returns the raw pool for asserting committed rows beside the
// stores.
func (s Stores) Pool() *pgxpool.Pool {
	return s.DB.Pool()
}
