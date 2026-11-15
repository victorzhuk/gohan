package maintaincmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/adapter/postgres/internal/maintaincmd"
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

func TestMaintainCommand(t *testing.T) {
	ctx := context.Background()
	schema := fmt.Sprintf("maintaincmd_%d", time.Now().UnixNano())
	d := dsn(t)

	pool, err := pgxpool.New(ctx, d)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: schema}); err != nil {
		t.Fatalf("seed migrate: %v", err)
	}
	// Drop every day partition so the first maintain pass recreates the
	// full window from an otherwise migrated schema.
	if _, err := pool.Exec(ctx, fmt.Sprintf(`DO $$ DECLARE r record; BEGIN
FOR r IN SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = '%s' AND c.relname ~ '_p[0-9]{8}$'
LOOP EXECUTE 'DROP TABLE %s.' || quote_ident(r.relname); END LOOP; END $$;`, schema, schema)); err != nil {
		t.Fatalf("drop partitions: %v", err)
	}

	t.Run("first_run_creates_window", func(t *testing.T) {
		var buf, errBuf bytes.Buffer
		if err := maintaincmd.Run(ctx, []string{"-dsn", d, "-schema", schema}, &buf, &errBuf); err != nil {
			t.Fatalf("maintaincmd.Run: %v (stderr: %s)", err, errBuf.String())
		}
		var out struct {
			Created []string `json:"created"`
			Dropped []string `json:"dropped"`
			Reaped  int      `json:"reaped"`
		}
		if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
			t.Fatalf("decode report %q: %v", buf.String(), err)
		}
		if len(out.Created) != 3*postgres.DefaultPartitionWindowDays {
			t.Fatalf("created %d partitions, want %d", len(out.Created), 3*postgres.DefaultPartitionWindowDays)
		}
		for _, table := range []string{"journal_record", "audit_record", "event_record"} {
			n := 0
			for _, name := range out.Created {
				if len(name) > len(table) && name[:len(table)] == table {
					n++
				}
			}
			if n != postgres.DefaultPartitionWindowDays {
				t.Fatalf("created %d partitions for %s, want %d", n, table, postgres.DefaultPartitionWindowDays)
			}
		}
		if len(out.Dropped) != 0 {
			t.Fatalf("dropped %v, want none", out.Dropped)
		}
		if out.Reaped != 0 {
			t.Fatalf("reaped %d, want 0", out.Reaped)
		}
	})

	t.Run("second_run_is_empty", func(t *testing.T) {
		var buf, errBuf bytes.Buffer
		if err := maintaincmd.Run(ctx, []string{"-dsn", d, "-schema", schema}, &buf, &errBuf); err != nil {
			t.Fatalf("maintaincmd.Run: %v (stderr: %s)", err, errBuf.String())
		}
		want := `{"created":[],"dropped":[],"reaped":0}` + "\n"
		if got := buf.String(); got != want {
			t.Fatalf("second run report: got %q, want %q", got, want)
		}
	})
}
