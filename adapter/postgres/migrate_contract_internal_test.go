package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestMigrateContract(t *testing.T) {
	t.Run("stores.pg-expand-contract-skew", func(t *testing.T) {
		ctx := context.Background()
		mkFS := func(marker bool) fstest.MapFS {
			contract := "-- +goose Up\n"
			if marker {
				contract += "-- +gohan contract after-reader 2\n"
			}
			contract += "ALTER TABLE t1 DROP COLUMN secret;\n-- +goose Down\nALTER TABLE t1 ADD COLUMN secret text;"
			return fstest.MapFS{
				"migrations/00001_expand.sql":   &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE t1 (id int PRIMARY KEY, secret text);\n-- +goose Down\nDROP TABLE t1;")},
				"migrations/00002_contract.sql": &fstest.MapFile{Data: []byte(contract)},
			}
		}

		t.Run("blocked_below_declared_floor", func(t *testing.T) {
			schema := fmt.Sprintf("contract_block_%d", time.Now().UnixNano())
			pool := poolFor(t, schema)
			_, err := migrateFS(ctx, pool, mkFS(true), MigrateOptions{Schema: schema, MinReaderVersion: 1})
			if !errors.Is(err, ErrContractMigrationBlocked) {
				t.Fatalf("migrateFS with MinReaderVersion 1 = %v, want ErrContractMigrationBlocked", err)
			}
			var maxv int64
			qerr := pool.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM `+quoteIdent(schema)+`.schema_migrations`).Scan(&maxv)
			if qerr != nil && !pgxConnErr(qerr) {
				t.Fatalf("read applied versions: %v", qerr)
			}
			if qerr != nil {
				maxv = 0
			}
			if maxv >= 2 {
				t.Fatalf("version 2 recorded in schema_migrations (max %d), want nothing applied", maxv)
			}
		})

		t.Run("applies_at_floor", func(t *testing.T) {
			schema := fmt.Sprintf("contract_ok_%d", time.Now().UnixNano())
			pool := poolFor(t, schema)
			applied, err := migrateFS(ctx, pool, mkFS(true), MigrateOptions{Schema: schema, MinReaderVersion: 2})
			if err != nil {
				t.Fatalf("migrateFS with MinReaderVersion 2: %v", err)
			}
			if len(applied) != 2 || applied[0] != 1 || applied[1] != 2 {
				t.Fatalf("applied %v, want [1 2]", applied)
			}
		})

		t.Run("unmarked_contract_needs_no_floor", func(t *testing.T) {
			schema := fmt.Sprintf("contract_free_%d", time.Now().UnixNano())
			pool := poolFor(t, schema)
			applied, err := migrateFS(ctx, pool, mkFS(false), MigrateOptions{Schema: schema, MinReaderVersion: 0})
			if err != nil {
				t.Fatalf("migrateFS without marker: %v", err)
			}
			if len(applied) != 2 || applied[0] != 1 || applied[1] != 2 {
				t.Fatalf("applied %v, want [1 2]", applied)
			}
		})

		t.Run("malformed marker is refused", func(t *testing.T) {
			schema := fmt.Sprintf("contract_bad_%d", time.Now().UnixNano())
			pool := poolFor(t, schema)
			bad := fstest.MapFS{
				"migrations/00001_expand.sql":   &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE t1 (id int PRIMARY KEY, secret text);\n-- +goose Down\nDROP TABLE t1;")},
				"migrations/00002_contract.sql": &fstest.MapFile{Data: []byte("-- +goose Up\n-- +gohan contract after-reader nine\nALTER TABLE t1 DROP COLUMN secret;\n-- +goose Down\nALTER TABLE t1 ADD COLUMN secret text;")},
			}
			_, err := migrateFS(ctx, pool, bad, MigrateOptions{Schema: schema, MinReaderVersion: 9})
			if !errors.Is(err, ErrContractMigrationBlocked) {
				t.Fatalf("migrateFS with malformed marker = %v, want ErrContractMigrationBlocked", err)
			}
			if !strings.Contains(err.Error(), "malformed contract marker") {
				t.Fatalf("error %q, want malformed contract marker detail", err)
			}
			var maxv int64
			qerr := pool.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM `+quoteIdent(schema)+`.schema_migrations`).Scan(&maxv)
			if qerr != nil && !pgxConnErr(qerr) {
				t.Fatalf("read applied versions: %v", qerr)
			}
			if qerr != nil {
				maxv = 0
			}
			if maxv >= 2 {
				t.Fatalf("version 2 recorded in schema_migrations (max %d), want nothing applied", maxv)
			}
		})

		t.Run("empty_schema_is_too_old", func(t *testing.T) {
			_, err := New(ctx, dsn(t), WithSchema(fmt.Sprintf("contract_too_old_%d", time.Now().UnixNano())))
			if !errors.Is(err, types.ErrSchemaTooOld) {
				t.Fatalf("New below MinSchema = %v, want ErrSchemaTooOld", err)
			}
		})

		t.Run("reader_lags_writer_across_contract", func(t *testing.T) {
			entries, err := os.ReadDir("migrations")
			if err != nil {
				t.Fatalf("read migrations dir: %v", err)
			}
			realFS := fstest.MapFS{}
			for _, e := range entries {
				data, rerr := os.ReadFile(filepath.Join("migrations", e.Name()))
				if rerr != nil {
					t.Fatalf("read %s: %v", e.Name(), rerr)
				}
				realFS["migrations/"+e.Name()] = &fstest.MapFile{Data: data}
			}
			realFS["migrations/00009_contract.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\n-- +gohan contract after-reader 9\nALTER TABLE journal_record DROP COLUMN IF EXISTS endpoint;\n-- +goose Down\nALTER TABLE journal_record ADD COLUMN IF NOT EXISTS endpoint text NOT NULL DEFAULT '';")}

			schema := fmt.Sprintf("contract_roll_%d", time.Now().UnixNano())
			pool := poolFor(t, schema)

			// (a) A reader with no floor applies the expand history only.
			applied, err := migrateFS(ctx, pool, realFS, MigrateOptions{Schema: schema, MinReaderVersion: 0, TargetVersion: 8})
			if err != nil {
				t.Fatalf("migrateFS with MinReaderVersion 0: %v", err)
			}
			want := make([]int64, 0, 8)
			for v := int64(1); v <= 8; v++ {
				want = append(want, v)
			}
			if !slices.Equal(applied, want) {
				t.Fatalf("applied %v, want %v", applied, want)
			}

			var use int
			useStack := func(t *testing.T) {
				t.Helper()
				use++
				db, nerr := New(ctx, dsn(t), WithSchema(schema))
				if nerr != nil {
					t.Fatalf("New on schema %s: %v", schema, nerr)
				}
				defer db.Close()
				journal := NewJournal(db)
				k := types.CallKey{SessionID: "skew", CallID: fmt.Sprintf("c%d", use)}
				entry, created, rerr := journal.Reserve(ctx, k, "tool", "fp", json.RawMessage(`{"x":1}`))
				if rerr != nil || !created {
					t.Fatalf("Reserve = created %v err %v, want fresh reservation", created, rerr)
				}
				if entry.Fingerprint != "fp" {
					t.Fatalf("Reserve fingerprint = %q, want fp", entry.Fingerprint)
				}
				if cerr := journal.Complete(ctx, k, types.ToolResult{ID: k.CallID}); cerr != nil {
					t.Fatalf("Complete: %v", cerr)
				}
				replay, created, rerr := journal.Reserve(ctx, k, "tool", "fp", json.RawMessage(`{"x":1}`))
				if rerr != nil || created || replay.State != stores.Completed {
					t.Fatalf("re-Reserve = (created %v, state %v, err %v), want completed replay", created, replay.State, rerr)
				}
				events := NewEventLog(db)
				runID := fmt.Sprintf("skew-run-%d", use)
				if aerr := events.Append(ctx, runID, stores.Event{Payload: types.TextDelta{Delta: "hi"}}); aerr != nil {
					t.Fatalf("EventLog Append: %v", aerr)
				}
				var got int
				for _, rerr := range events.Read(ctx, runID, 0) {
					if rerr != nil {
						t.Fatalf("EventLog Read: %v", rerr)
					}
					got++
				}
				if got != 1 {
					t.Fatalf("read %d events for %s, want 1", got, runID)
				}
			}

			// (b) The version 8 stack serves the old reader/writer path.
			useStack(t)

			// (c) The contract migration holds while the reader sits below 9.
			_, err = migrateFS(ctx, pool, realFS, MigrateOptions{Schema: schema, MinReaderVersion: 8, TargetVersion: 9})
			if !errors.Is(err, ErrContractMigrationBlocked) {
				t.Fatalf("migrateFS with MinReaderVersion 8 = %v, want ErrContractMigrationBlocked", err)
			}
			var maxv int64
			if qerr := pool.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM `+quoteIdent(schema)+`.schema_migrations`).Scan(&maxv); qerr != nil {
				t.Fatalf("read applied versions: %v", qerr)
			}
			if maxv >= 9 {
				t.Fatalf("version 9 recorded in schema_migrations (max %d), want blocked", maxv)
			}
			var cols int
			if qerr := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'journal_record' AND column_name = 'endpoint'`, schema).Scan(&cols); qerr != nil {
				t.Fatalf("check endpoint column: %v", qerr)
			}
			if cols != 1 {
				t.Fatalf("endpoint column count = %d, want 1: contract migration ran despite block", cols)
			}

			// (d) The old shape still serves reads and writes.
			useStack(t)

			// (e) Once the reader declares 9 the contract migration applies.
			applied, err = migrateFS(ctx, pool, realFS, MigrateOptions{Schema: schema, MinReaderVersion: 9, TargetVersion: 9})
			if err != nil {
				t.Fatalf("migrateFS with MinReaderVersion 9: %v", err)
			}
			if len(applied) != 1 || applied[0] != 9 {
				t.Fatalf("applied %v, want [9]", applied)
			}
			if qerr := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'journal_record' AND column_name = 'endpoint'`, schema).Scan(&cols); qerr != nil {
				t.Fatalf("check endpoint column: %v", qerr)
			}
			if cols != 0 {
				t.Fatalf("endpoint column count = %d, want 0 after contract migration", cols)
			}
		})
	})
}
