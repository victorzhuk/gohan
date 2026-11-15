package postgres

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var (
	internalOnce sync.Once
	internalDSN  string
	internalErr  error
)

func waitReadyInternal(ctx context.Context, d string) error {
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

// dsn boots the shared internal test container once. The postgres_test
// TestMain owns process exit; this container is reclaimed by the
// testcontainers reaper when the process ends.
func dsn(t *testing.T) string {
	t.Helper()
	internalOnce.Do(func() {
		ctx := context.Background()
		ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("gohan"),
			tcpostgres.WithUsername("gohan"),
			tcpostgres.WithPassword("gohan"),
		)
		if err != nil {
			internalErr = err
			return
		}
		internalDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			internalErr = err
			return
		}
		internalErr = waitReadyInternal(ctx, internalDSN)
	})
	if internalErr != nil {
		t.Fatalf("postgres container: %v", internalErr)
	}
	return internalDSN
}

func poolFor(t *testing.T, schema string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn(t))
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
