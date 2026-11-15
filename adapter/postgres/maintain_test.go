//go:build full

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/types"
)

func partitionNames(t *testing.T, db *postgres.DB, schema, parent string) []string {
	t.Helper()
	rows, err := db.Pool().Query(context.Background(), `
SELECT c.relname FROM pg_inherits i
JOIN pg_class c ON c.oid = i.inhrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE i.inhparent = ($1 || '.' || $2)::regclass AND n.nspname = $1`, schema, parent)
	if err != nil {
		t.Fatalf("partition names: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("partition names: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("partition names: %v", err)
	}
	sort.Strings(names)
	return names
}

func windowNames(parent string, now time.Time, days int) []string {
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	var names []string
	for i := range days {
		names = append(names, fmt.Sprintf("%s_p%s", parent, today.AddDate(0, 0, i).Format("20060102")))
	}
	return names
}

func toRegclass(t *testing.T, db *postgres.DB, qualified string) bool {
	t.Helper()
	var exists bool
	if err := db.Pool().QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, qualified).Scan(&exists); err != nil {
		t.Fatalf("to_regclass(%s): %v", qualified, err)
	}
	return exists
}

func tupDel(t *testing.T, db *postgres.DB, schema, parent string) int64 {
	t.Helper()
	var n int64
	if err := db.Pool().QueryRow(context.Background(), `
SELECT coalesce(sum(s.n_tup_del), 0) FROM pg_stat_user_tables s
WHERE s.schemaname = $1 AND (s.relname = $2 OR s.relname LIKE $2 || '\_p%')`,
		schema, parent).Scan(&n); err != nil {
		t.Fatalf("n_tup_del: %v", err)
	}
	return n
}

func dropPartitions(t *testing.T, db *postgres.DB, schema string, parents ...string) {
	t.Helper()
	for _, parent := range parents {
		for _, name := range partitionNames(t, db, schema, parent) {
			if _, err := db.Pool().Exec(context.Background(), fmt.Sprintf(`DROP TABLE %s.%s`, schema, name)); err != nil {
				t.Fatalf("drop %s: %v", name, err)
			}
		}
	}
}

func TestMaintain(t *testing.T) {
	ctx := context.Background()

	t.Run("stores.pg-maintain-idempotent", func(t *testing.T) {
		db := newDB(t, "maint_idem")
		dropPartitions(t, db, "maint_idem", "journal_record", "audit_record", "event_record")
		expired := time.Now().UTC().AddDate(0, 0, -3)
		expiredDay := expired.Format("20060102")
		expiredName := fmt.Sprintf("journal_record_p%s", expiredDay)
		_, err := db.Pool().Exec(context.Background(), fmt.Sprintf(`
CREATE TABLE maint_idem.%s PARTITION OF maint_idem.journal_record
FOR VALUES FROM ('%s') TO ('%s')`,
			expiredName, expiredDay, expired.AddDate(0, 0, 1).Format("20060102")))
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Pool().Exec(context.Background(), `
INSERT INTO maint_idem.journal_record
    (session_id, call_id, state, fingerprint, tool, at, expires_at, payload)
VALUES ('mi', 'stale', 0, 'fp', 'tool', $1, $1, ''::bytea)`, expired)
		if err != nil {
			t.Fatal(err)
		}
		type outcome struct {
			report postgres.MaintainReport
			err    error
		}
		results := make(chan outcome, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := postgres.Maintain(ctx, db.Pool(), postgres.MaintainOptions{
					Schema:    "maint_idem",
					Retention: 24 * time.Hour,
				})
				results <- outcome{r, err}
			}()
		}
		wg.Wait()
		close(results)
		for o := range results {
			if o.err != nil {
				t.Fatalf("Maintain: %v", o.err)
			}
		}
		for _, parent := range []string{"journal_record", "audit_record", "event_record"} {
			want := windowNames(parent, time.Now(), postgres.DefaultPartitionWindowDays)
			got := partitionNames(t, db, "maint_idem", parent)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("%s partitions = %v, want %v", parent, got, want)
			}
		}
		if toRegclass(t, db, "maint_idem."+expiredName) {
			t.Fatalf("expired partition %s survived concurrent Maintain", expiredName)
		}
		if n := count(t, db, `SELECT count(*) FROM maint_idem.journal_record WHERE session_id = 'mi'`); n != 0 {
			t.Fatalf("expired journal rows = %d, want 0", n)
		}
		second, err := postgres.Maintain(ctx, db.Pool(), postgres.MaintainOptions{
			Schema:    "maint_idem",
			Retention: 24 * time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(second.Created) != 0 || len(second.Dropped) != 0 || second.Reaped != 0 {
			t.Fatalf("second run created %v dropped %v reaped %d, want none", second.Created, second.Dropped, second.Reaped)
		}
	})

	t.Run("stores.pg-partitions-ahead", func(t *testing.T) {
		db := newDB(t, "maint_ahead")
		dropPartitions(t, db, "maint_ahead", "journal_record", "audit_record", "event_record")
		for _, parent := range []string{"journal_record", "audit_record", "event_record"} {
			if got := partitionNames(t, db, "maint_ahead", parent); len(got) != 0 {
				t.Fatalf("%s partitions after drop = %v, want none", parent, got)
			}
		}
		report, err := postgres.Maintain(ctx, db.Pool(), postgres.MaintainOptions{Schema: "maint_ahead"})
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Created) == 0 {
			t.Fatalf("Maintain created nothing after partition drop: %+v", report)
		}
		for _, parent := range []string{"journal_record", "audit_record", "event_record"} {
			want := windowNames(parent, time.Now(), postgres.DefaultPartitionWindowDays)
			got := partitionNames(t, db, "maint_ahead", parent)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("%s partitions = %v, want %v", parent, got, want)
			}
		}
		j := postgres.NewJournal(db)
		if _, _, err := j.Reserve(ctx, types.CallKey{SessionID: "ma", CallID: "in-window"}, "tool", "fp", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		ahead := newDB(t, "maint_ahead", postgres.WithClock(func() time.Time { return time.Now().Add(30 * 24 * time.Hour) }))
		ja := postgres.NewJournal(ahead)
		_, _, err = ja.Reserve(ctx, types.CallKey{SessionID: "ma", CallID: "far-ahead"}, "tool", "fp", json.RawMessage(`{}`))
		if !errors.Is(err, types.ErrPartitionMissing) {
			t.Fatalf("Reserve 30 days ahead = %v, want ErrPartitionMissing", err)
		}
	})

	t.Run("stores.retention-by-partition", func(t *testing.T) {
		twoDaysAgo := time.Now().Add(-48 * time.Hour)
		db := newDB(t, "maint_ret", postgres.WithClock(func() time.Time { return twoDaysAgo }))
		old, err := postgres.Maintain(ctx, db.Pool(), postgres.MaintainOptions{
			Schema: "maint_ret",
			Now:    func() time.Time { return twoDaysAgo },
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(old.Created) == 0 {
			t.Fatalf("backdated Maintain created nothing: %+v", old)
		}
		j := postgres.NewJournal(db)
		if _, _, err := j.Reserve(ctx, types.CallKey{SessionID: "mr", CallID: "old"}, "tool", "fp", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		before := tupDel(t, db, "maint_ret", "journal_record")
		report, err := postgres.Maintain(ctx, db.Pool(), postgres.MaintainOptions{Schema: "maint_ret", Retention: 24 * time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Dropped) == 0 {
			t.Fatalf("retention dropped nothing: %+v", report)
		}
		gone := fmt.Sprintf("journal_record_p%s", twoDaysAgo.UTC().Format("20060102"))
		for _, name := range partitionNames(t, db, "maint_ret", "journal_record") {
			if name == gone {
				t.Fatalf("partition %s survived retention", gone)
			}
		}
		if n := count(t, db, `SELECT count(*) FROM maint_ret.journal_record WHERE session_id = 'mr'`); n != 0 {
			t.Fatalf("retained-day rows = %d, want 0", n)
		}
		if after := tupDel(t, db, "maint_ret", "journal_record"); after != before {
			t.Fatalf("n_tup_del changed %d -> %d during Maintain, want unchanged", before, after)
		}
	})

	t.Run("upgrade backfills existing rows", func(t *testing.T) {
		pool, err := pgxpoolFor(t, "maint_upg")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: "maint_upg", TargetVersion: postgres.HeadVersion() - 1}); err != nil {
			t.Fatal(err)
		}
		past := time.Now().UTC().Add(-72 * time.Hour)
		old, err := postgres.New(ctx, dsn(t), postgres.WithSchema("maint_upg"), postgres.WithClock(func() time.Time { return past }))
		if err != nil {
			t.Fatal(err)
		}
		jOld := postgres.NewJournal(old, postgres.WithJournalTTL(30*24*time.Hour))
		if _, _, err := jOld.Reserve(ctx, types.CallKey{SessionID: "u", CallID: "cold"}, "tool", "old-fp", json.RawMessage(`{"x":1}`)); err != nil {
			t.Fatal(err)
		}
		old.Close()
		nowDB, err := postgres.New(ctx, dsn(t), postgres.WithSchema("maint_upg"))
		if err != nil {
			t.Fatal(err)
		}
		jNow := postgres.NewJournal(nowDB, postgres.WithJournalTTL(30*24*time.Hour))
		if _, _, err := jNow.Reserve(ctx, types.CallKey{SessionID: "u", CallID: "ctoday"}, "tool", "old-fp", json.RawMessage(`{"y":2}`)); err != nil {
			t.Fatal(err)
		}
		nowDB.Close()
		if _, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: "maint_upg"}); err != nil {
			t.Fatal(err)
		}
		db, err := postgres.New(ctx, dsn(t), postgres.WithSchema("maint_upg"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		j := postgres.NewJournal(db, postgres.WithJournalTTL(30*24*time.Hour))
		entries, err := j.ByFingerprint(ctx, "u", "old-fp")
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("ByFingerprint after upgrade = %d entries, want 2", len(entries))
		}
		if toRegclass(t, db, "maint_upg.journal_record_v7") {
			t.Fatal("journal_record_v7 staging survived migration")
		}
		oldDay := fmt.Sprintf("journal_record_p%s", past.Format("20060102"))
		if !toRegclass(t, db, "maint_upg."+oldDay) {
			t.Fatalf("partition %s missing after backfill", oldDay)
		}
	})
}
