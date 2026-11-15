package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func seedDeadRuns(t *testing.T, db *postgres.DB, schema string, n int) {
	t.Helper()
	runs := postgres.NewRuns(db)
	for i := range n {
		id := fmt.Sprintf("dead-%03d", i)
		if _, err := runs.Start(context.Background(),
			stores.Run{RunID: id, SessionID: "dead-s-" + id, Flow: "chat"}, stores.LeaseTTL); err != nil {
			t.Fatalf("start dead run %s: %v", id, err)
		}
	}
	if _, err := db.Pool().Exec(context.Background(), fmt.Sprintf(
		`UPDATE %s.run_record SET state = 3, heartbeat_at = now() - interval '72 hours'
		 WHERE run_id LIKE 'dead-%%'`, schema)); err != nil {
		t.Fatalf("mark dead runs terminal: %v", err)
	}
}

func installUpdateTally(t *testing.T, db *postgres.DB, schema string) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		fmt.Sprintf(`CREATE TABLE %s.run_update_tally (n bigint NOT NULL)`, schema),
		fmt.Sprintf(`INSERT INTO %s.run_update_tally (n) VALUES (0)`, schema),
		fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s.tally_run_update() RETURNS trigger
			LANGUAGE plpgsql AS $$
			BEGIN
				UPDATE %s.run_update_tally SET n = n + 1;
				RETURN NEW;
			END $$`, schema, schema),
		fmt.Sprintf(`CREATE TRIGGER tally_run_update BEFORE UPDATE ON %s.run_record
			FOR EACH ROW EXECUTE FUNCTION %s.tally_run_update()`, schema, schema),
	} {
		if _, err := db.Pool().Exec(ctx, stmt); err != nil {
			t.Fatalf("install tally: %v", err)
		}
	}
}

func readTally(t *testing.T, db *postgres.DB, schema string) int64 {
	t.Helper()
	var n int64
	if err := db.Pool().QueryRow(context.Background(),
		fmt.Sprintf(`SELECT n FROM %s.run_update_tally`, schema)).Scan(&n); err != nil {
		t.Fatalf("read tally: %v", err)
	}
	return n
}

func TestReap(t *testing.T) {
	t.Run("stores.pg-hot-runs-reaping", func(t *testing.T) {
		schema := "reap_hot"
		db := newDB(t, schema)
		seedDeadRuns(t, db, schema, 40)
		installUpdateTally(t, db, schema)

		runs := postgres.NewRuns(db)
		journal := postgres.NewJournal(db)
		const workers = 10
		leases := make([]stores.Lease, workers)
		for w := range workers {
			id := fmt.Sprintf("live-%02d", w)
			l, err := runs.Start(context.Background(),
				stores.Run{RunID: id, SessionID: "live-s-" + id, Flow: "chat"}, stores.LeaseTTL)
			if err != nil {
				t.Fatalf("start live run %s: %v", id, err)
			}
			leases[w] = l
		}

		var heartbeats atomic.Int64
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		errs := make(chan error, workers)
		var wg sync.WaitGroup
		stop := make(chan struct{})
		var beatWG sync.WaitGroup
		beatWG.Add(workers)
		var maintainStart atomic.Int64
		var liveDuringReap atomic.Bool
		for w := range workers {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				var once sync.Once
				signalBeat := func() { once.Do(beatWG.Done) }
				defer signalBeat()
				for {
					select {
					case <-stop:
						return
					default:
					}
					l, err := runs.Heartbeat(ctx, leases[w])
					if err != nil {
						errs <- fmt.Errorf("worker %d heartbeat: %w", w, err)
						return
					}
					leases[w] = l
					heartbeats.Add(1)
					signalBeat()
					if start := maintainStart.Load(); start != 0 &&
						time.Now().UnixNano() > start {
						liveDuringReap.Store(true)
					}
					k := types.CallKey{SessionID: "live-s-live-" + fmt.Sprintf("%02d", w),
						CallID: fmt.Sprintf("call-%d-%d", w, heartbeats.Load())}
					if _, _, err := journal.Reserve(ctx, k, "book", "fp", json.RawMessage(`{}`)); err != nil {
						errs <- fmt.Errorf("worker %d reserve: %w", w, err)
						return
					}
					if err := journal.Complete(ctx, k, types.ToolResult{ID: k.CallID}); err != nil {
						errs <- fmt.Errorf("worker %d complete: %w", w, err)
						return
					}
				}
			}(w)
		}

		type maintainResult struct {
			report postgres.MaintainReport
			err    error
		}
		maintainDone := make(chan maintainResult, 1)
		go func() {
			beatWG.Wait()
			maintainStart.Store(time.Now().UnixNano())
			mctx, mcancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer mcancel()
			report, err := postgres.Maintain(mctx, db.Pool(), postgres.MaintainOptions{
				Schema:         schema,
				Retention:      time.Hour,
				ReapBatchSize:  25,
				MaxReapBatches: 4,
			})
			maintainDone <- maintainResult{report, err}
		}()

		res := <-maintainDone
		close(stop)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("live worker failed: %v", err)
		}
		if res.err != nil {
			t.Fatalf("maintain: %v", res.err)
		}
		report := res.report
		if !liveDuringReap.Load() {
			t.Fatal("no worker heartbeat succeeded after Maintain started")
		}
		if report.Reaped != 40 {
			t.Fatalf("reaped = %d, want 40", report.Reaped)
		}
		if n := count(t, db, fmt.Sprintf(
			`SELECT count(*) FROM %s.run_record WHERE state IN (3, 4)`, schema)); n != 0 {
			t.Fatalf("terminal rows left = %d, want 0", n)
		}
		if n := count(t, db, fmt.Sprintf(
			`SELECT count(*) FROM %s.run_record WHERE state = 0`, schema)); n != workers {
			t.Fatalf("live rows = %d, want %d", n, workers)
		}
		if n := count(t, db, fmt.Sprintf(`SELECT count(*) FROM %s.run_record`, schema)); n > workers {
			t.Fatalf("run_record rows = %d, want <= %d", n, workers)
		}
		if got, want := readTally(t, db, schema), heartbeats.Load(); got != want {
			t.Fatalf("run_record updates = %d, want exactly the %d successful heartbeats", got, want)
		}
	})

	t.Run("reaping honors the per-call batch bound", func(t *testing.T) {
		schema := "reap_bounded"
		db := newDB(t, schema)
		seedDeadRuns(t, db, schema, 40)

		report, err := postgres.Maintain(context.Background(), db.Pool(), postgres.MaintainOptions{
			Schema:         schema,
			Retention:      time.Hour,
			ReapBatchSize:  25,
			MaxReapBatches: 1,
		})
		if err != nil {
			t.Fatalf("maintain: %v", err)
		}
		if report.Reaped != 25 {
			t.Fatalf("reaped = %d, want 25 (one batch cap)", report.Reaped)
		}
		if n := count(t, db, fmt.Sprintf(
			`SELECT count(*) FROM %s.run_record WHERE state IN (3, 4)`, schema)); n != 15 {
			t.Fatalf("terminal rows left = %d, want 15", n)
		}
	})
}
