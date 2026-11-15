package migratecmd_test

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/adapter/postgres/internal/migratecmd"
)

var (
	containerOnce sync.Once
	container     *tcpostgres.PostgresContainer
	containerDSN  string
	containerErr  error
)

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

func TestMigrateCommand(t *testing.T) {
	t.Run("stores.pg-migrate-command", func(t *testing.T) {
		head := postgres.HeadVersion()
		schema := fmt.Sprintf("migratecmd_%d", time.Now().UnixNano())
		d := dsn(t)

		pool, err := pgxpool.New(context.Background(), d)
		if err != nil {
			t.Fatalf("pgxpool.New: %v", err)
		}
		t.Cleanup(pool.Close)

		if _, err := postgres.Migrate(context.Background(), pool, postgres.MigrateOptions{
			Schema:        schema,
			TargetVersion: head - 3,
		}); err != nil {
			t.Fatalf("seed migrate to %d: %v", head-3, err)
		}

		var buf, errBuf bytes.Buffer
		err = migratecmd.Run(context.Background(),
			[]string{"-dsn", d, "-schema", schema, "-target", strconv.FormatInt(head, 10)},
			&buf, &errBuf)
		if err != nil {
			t.Fatalf("migratecmd.Run: %v (stderr: %s)", err, errBuf.String())
		}

		want := fmt.Sprintf(`{"applied":[%d,%d,%d]}`, head-2, head-1, head)
		if got := buf.String(); got != want+"\n" {
			t.Fatalf("applied JSON: got %q, want %q", got, want)
		}

		var newest int64
		if err := pool.QueryRow(context.Background(),
			`SELECT coalesce(max(version), 0) FROM `+quoteIdent(schema)+`.schema_migrations`,
		).Scan(&newest); err != nil {
			t.Fatalf("read newest applied version: %v", err)
		}
		if newest != head {
			t.Fatalf("newest applied version: got %d, want %d", newest, head)
		}

		buf.Reset()
		err = migratecmd.Run(context.Background(),
			[]string{"-dsn", d, "-schema", schema},
			&buf, &errBuf)
		if err != nil {
			t.Fatalf("second migratecmd.Run: %v (stderr: %s)", err, errBuf.String())
		}
		if got := buf.String(); got != `{"applied":[]}`+"\n" {
			t.Fatalf("second run applied JSON: got %q, want {\"applied\":[]}", got)
		}
	})
}

func quoteIdent(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for _, c := range []byte(s) {
		if c == '"' {
			out = append(out, '"')
		}
		out = append(out, c)
	}
	return string(append(out, '"'))
}
