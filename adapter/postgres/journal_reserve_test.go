//go:build full

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestJournalReserveAtomic(t *testing.T) {
	ctx := context.Background()
	const n = 10

	t.Run("stores.pg-journal-concurrent-reserve", func(t *testing.T) {
		t.Run("pool", func(t *testing.T) {
			db := newDB(t, bindSchema("journalrace"))
			j := postgres.NewJournal(db)
			k := types.CallKey{SessionID: "jr-s1", CallID: "jr-c1"}
			type outcome struct {
				entry   stores.Entry
				created bool
				err     error
			}
			start := make(chan struct{})
			results := make(chan outcome, n)
			for range n {
				go func() {
					<-start
					e, created, err := j.Reserve(ctx, k, "tool", "jr-fp", json.RawMessage(`{"x":1}`))
					results <- outcome{e, created, err}
				}()
			}
			close(start)
			created := 0
			for i := 0; i < n; i++ {
				r := <-results
				if r.err != nil {
					t.Fatalf("Reserve: %v", r.err)
				}
				if string(r.entry.Fingerprint) != "jr-fp" {
					t.Fatalf("Reserve fingerprint = %q, want jr-fp", r.entry.Fingerprint)
				}
				if r.created {
					created++
				}
			}
			if created != 1 {
				t.Fatalf("created=true count = %d, want exactly 1", created)
			}
			if got := count(t, db, `SELECT count(*) FROM journal_record WHERE session_id = $1 AND call_id = $2`, k.SessionID, k.CallID); got != 1 {
				t.Fatalf("journal rows = %d, want 1", got)
			}
		})

		t.Run("tx", func(t *testing.T) {
			db := newDB(t, bindSchema("journalracetx"))
			j := postgres.NewJournal(db)
			k := types.CallKey{SessionID: "jr-s2", CallID: "jr-c2"}
			type outcome struct {
				created bool
				err     error
			}
			start := make(chan struct{})
			results := make(chan outcome, n)
			for range n {
				go func() {
					<-start
					tx, err := db.Begin(ctx)
					if err != nil {
						results <- outcome{err: err}
						return
					}
					defer tx.Rollback(ctx)
					_, created, err := j.ReserveTx(ctx, tx, k, "tool", "jr-fp", json.RawMessage(`{"x":1}`))
					if err != nil {
						results <- outcome{err: err}
						return
					}
					if err := tx.Commit(ctx); err != nil {
						results <- outcome{err: err}
						return
					}
					results <- outcome{created: created}
				}()
			}
			close(start)
			created := 0
			for i := 0; i < n; i++ {
				r := <-results
				if r.err != nil {
					t.Fatalf("ReserveTx: %v", r.err)
				}
				if r.created {
					created++
				}
			}
			if created != 1 {
				t.Fatalf("created=true count = %d, want exactly 1", created)
			}
			if got := count(t, db, `SELECT count(*) FROM journal_record WHERE session_id = $1 AND call_id = $2`, k.SessionID, k.CallID); got != 1 {
				t.Fatalf("journal rows = %d, want 1", got)
			}
		})
	})
}
