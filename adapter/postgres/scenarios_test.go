//go:build full

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
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

func TestStores(t *testing.T) {
	t.Run("stores.same-transaction-journal", func(t *testing.T) {
		ctx := context.Background()
		db := newDB(t, "tx_journal")
		j := postgres.NewJournal(db)
		if _, err := db.Pool().Exec(ctx, `CREATE TABLE IF NOT EXISTS booking (id text PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		res := types.ToolResult{ID: "call-1"}

		// A rollback leaves neither the booking nor the journal entry.
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		if _, _, err := j.ReserveTx(ctx, tx, k, "book", "fp1", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO booking (id) VALUES ('b1')`); err != nil {
			t.Fatal(err)
		}
		if err := j.CompleteTx(ctx, tx, k, res); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if got := count(t, db, `SELECT count(*) FROM booking`); got != 0 {
			t.Fatalf("booking rows after rollback = %d, want 0", got)
		}
		if got := count(t, db, `SELECT count(*) FROM journal_record WHERE session_id = 's1' AND state = 1`); got != 0 {
			t.Fatalf("completed journal entries after rollback = %d, want 0", got)
		}

		// The committed case keeps both, and a re-Reserve replays the
		// recorded result instead of a second execution.
		k2 := types.CallKey{SessionID: "s1", CallID: "c2"}
		tx2, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := j.ReserveTx(ctx, tx2, k2, "book", "fp2", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx2.Exec(ctx, `INSERT INTO booking (id) VALUES ('b2')`); err != nil {
			t.Fatal(err)
		}
		if err := j.CompleteTx(ctx, tx2, k2, res); err != nil {
			t.Fatal(err)
		}
		if err := tx2.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if got := count(t, db, `SELECT count(*) FROM booking WHERE id = 'b2'`); got != 1 {
			t.Fatalf("booking rows after commit = %d, want 1", got)
		}
		replay, created, err := j.Reserve(ctx, k2, "book", "fp2", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if created || replay.State != stores.Completed {
			t.Fatalf("re-Reserve = (created=%v, state=%v), want replay of the completed entry", created, replay.State)
		}
		got, err := json.Marshal(replay.Result)
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("replayed result = %s, want %s", got, want)
		}

		// A key with no live reservation is refused on the same guarded
		// path Complete uses.
		if err := j.CompleteTx(ctx, mustTx(t, db), types.CallKey{SessionID: "s1", CallID: "missing"}, res); !errors.Is(err, stores.ErrJournalCompleteMissed) {
			t.Fatalf("CompleteTx on an unknown key = %v, want ErrJournalCompleteMissed", err)
		}
	})

	t.Run("stores.pg-schema-owned", func(t *testing.T) {
		ctx := context.Background()
		a := newDB(t, "own_a")
		b := newDB(t, "own_b")
		ja, jb := postgres.NewJournal(a), postgres.NewJournal(b)
		if _, _, err := ja.Reserve(ctx, types.CallKey{SessionID: "s", CallID: "c"}, "t", "fp", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := jb.Reserve(ctx, types.CallKey{SessionID: "other", CallID: "c"}, "t", "fp", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if got := count(t, a, `SELECT count(*) FROM own_a.journal_record`); got != 1 {
			t.Fatalf("schema a rows = %d, want 1", got)
		}
		if got := count(t, a, `SELECT count(*) FROM own_b.journal_record WHERE session_id = 's'`); got != 0 {
			t.Fatalf("schema a must not see schema b's session, rows = %d", got)
		}
	})

	t.Run("stores.pg-migrate-serialised", func(t *testing.T) {
		ctx := context.Background()
		pool, err := pgxpoolFor(t, "migrate_race")
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		const pods = 5
		results := make([][]int64, pods)
		var wg sync.WaitGroup
		var first error
		for i := range pods {
			wg.Add(1)
			go func() {
				defer wg.Done()
				applied, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: "migrate_race"})
				results[i] = applied
				if err != nil && first == nil {
					first = err
				}
			}()
		}
		wg.Wait()
		if first != nil {
			t.Fatalf("concurrent Migrate: %v", first)
		}
		seen := make(map[int64]int)
		for _, applied := range results {
			for _, v := range applied {
				seen[v]++
			}
		}
		for v, n := range seen {
			if n != 1 {
				t.Fatalf("version %d applied %d times across %d pods, want exactly once", v, n, pods)
			}
		}
	})

	t.Run("stores.pg-schema-too-old", func(t *testing.T) {
		_, err := postgres.New(context.Background(), dsn(t), postgres.WithSchema("too_old_empty"))
		if !errors.Is(err, types.ErrSchemaTooOld) {
			t.Fatalf("New below MinSchema = %v, want ErrSchemaTooOld", err)
		}
	})

	t.Run("stores.pg-schema-ahead-warns", func(t *testing.T) {
		ctx := context.Background()
		pool, err := pgxpoolFor(t, "ahead_warns")
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		if _, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: "ahead_warns"}); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO ahead_warns.schema_migrations (version) VALUES ($1)`, postgres.HeadVersion()+1); err != nil {
			t.Fatal(err)
		}
		var ahead int
		db, err := postgres.New(ctx, dsn(t), postgres.WithSchema("ahead_warns"), postgres.WithTelemetry(postgres.Telemetry{
			SchemaAhead: func(delta int) { ahead = delta },
		}))
		if err != nil {
			t.Fatalf("New on a schema one version ahead: %v", err)
		}
		defer db.Close()
		if ahead != 1 {
			t.Fatalf("gohan.postgres.schema_ahead = %d, want 1", ahead)
		}
	})
}

func mustTx(t *testing.T, db *postgres.DB) pgx.Tx {
	t.Helper()
	tx, err := db.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
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
