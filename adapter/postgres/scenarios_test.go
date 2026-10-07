//go:build full

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/types"
)

func TestStores(t *testing.T) {
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
