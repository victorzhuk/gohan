package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestJournalTx(t *testing.T) {
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
