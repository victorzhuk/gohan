package postgres_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/victorzhuk/gohan/adapter/postgres"
)

var (
	containerOnce sync.Once
	container     *tcpostgres.PostgresContainer
	containerDSN  string
	containerErr  error
)

func waitReady(ctx context.Context, d string) error {
	deadline := time.Now().Add(60 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		pool, err := pgxpool.New(ctx, d)
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

func TestMain(m *testing.M) {
	code := m.Run()
	if container != nil {
		_ = container.Terminate(context.Background())
	}
	os.Exit(code)
}

func dsn(t *testing.T) string {
	t.Helper()
	containerOnce.Do(func() {
		ctx := context.Background()
		ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("gohan"),
			tcpostgres.WithUsername("gohan"),
			tcpostgres.WithPassword("gohan"),
		)
		if err != nil {
			containerErr = err
			return
		}
		containerDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			containerErr = err
			return
		}
		containerErr = waitReady(ctx, containerDSN)
		container = ctr
	})
	if containerErr != nil {
		t.Fatalf("postgres container: %v", containerErr)
	}
	return containerDSN
}

func newDB(t *testing.T, schema string, opts ...postgres.Option) *postgres.DB {
	t.Helper()
	opts = append([]postgres.Option{postgres.WithSchema(schema), postgres.WithAutoMigrate()}, opts...)
	db, err := postgres.New(context.Background(), dsn(t), opts...)
	if err != nil {
		t.Fatalf("postgres.New(%s): %v", schema, err)
	}
	t.Cleanup(db.Close)
	return db
}

func count(t *testing.T, db *postgres.DB, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Pool().QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func pgxpoolFor(t *testing.T, schema string) (*pgxpool.Pool, error) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn(t))
	if err != nil {
		return nil, err
	}
	t.Cleanup(pool.Close)
	return pool, nil
}
