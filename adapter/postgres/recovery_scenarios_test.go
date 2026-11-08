//go:build full

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/adapter/postgres"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func newPgRuns(t *testing.T, db *postgres.DB) *postgres.Runs {
	t.Helper()
	return postgres.NewRuns(db)
}

func newPgCheckpoints(t *testing.T, db *postgres.DB) *postgres.Checkpoints {
	t.Helper()
	return postgres.NewCheckpoints(db)
}

func seedRun(t *testing.T, ctx context.Context, runs *postgres.Runs, session, runID string, ttl time.Duration) stores.Lease {
	t.Helper()
	l, err := runs.Start(ctx, stores.Run{SessionID: session, RunID: runID}, ttl)
	if err != nil {
		t.Fatalf("start %s: %v", runID, err)
	}
	return l
}

func suspendRun(t *testing.T, ctx context.Context, runs *postgres.Runs, cps *postgres.Checkpoints, l stores.Lease, session string, reason types.SuspendReason) types.ResumeToken {
	t.Helper()
	tok, err := cps.Put(ctx, stores.Checkpoint{RunID: l.RunID, SessionID: session})
	if err != nil {
		t.Fatalf("put checkpoint for %s: %v", l.RunID, err)
	}
	if err := runs.SuspendWithReason(ctx, l, tok, reason); err != nil {
		t.Fatalf("suspend %s: %v", l.RunID, err)
	}
	return tok
}

func runByID(t *testing.T, ctx context.Context, runs *postgres.Runs, runID string) stores.Run {
	t.Helper()
	run, err := runs.ByID(ctx, runID)
	if err != nil {
		t.Fatalf("load %s: %v", runID, err)
	}
	return run
}

func checkpointExpiry(t *testing.T, db *postgres.DB, tok types.ResumeToken) *time.Time {
	t.Helper()
	var exp *time.Time
	if err := db.Pool().QueryRow(t.Context(), `SELECT expires_at FROM checkpoint_record WHERE token = $1`, string(tok)).Scan(&exp); err != nil {
		t.Fatalf("read expiry of %s: %v", tok, err)
	}
	return exp
}

func TestPostgres(t *testing.T) {
	t.Run("stores.resume-session-exclusive", func(t *testing.T) {
		ctx := t.Context()
		db := newDB(t, "resume_excl")
		runs := newPgRuns(t, db)
		cps := newPgCheckpoints(t, db)
		const sess = "s-resume-excl"
		l := seedRun(t, ctx, runs, sess, "run-a", time.Hour)
		suspendRun(t, ctx, runs, cps, l, sess, "")

		var mu sync.Mutex
		var startErr, resumeErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := runs.Start(ctx, stores.Run{SessionID: sess, RunID: "run-b"}, time.Hour)
			mu.Lock()
			startErr = err
			mu.Unlock()
		}()
		go func() {
			defer wg.Done()
			_, err := runs.Resuming(ctx, "run-a", time.Hour)
			mu.Lock()
			resumeErr = err
			mu.Unlock()
		}()
		wg.Wait()

		mu.Lock()
		defer mu.Unlock()
		winners := 0
		if startErr == nil {
			winners++
		}
		if resumeErr == nil {
			winners++
		}
		if winners != 1 {
			t.Fatalf("winners = %d (start %v, resume %v), want exactly one", winners, startErr, resumeErr)
		}
		if startErr != nil && !errors.Is(startErr, types.ErrRunActive) {
			t.Fatalf("start err = %v, want ErrRunActive", startErr)
		}
		if resumeErr != nil && !errors.Is(resumeErr, types.ErrRunActive) {
			t.Fatalf("resume err = %v, want ErrRunActive", resumeErr)
		}
		if startErr == nil {
			if run := runByID(t, ctx, runs, "run-b"); run.State != stores.Running {
				t.Fatalf("winner run-b state = %v, want Running", run.State)
			}
			if run := runByID(t, ctx, runs, "run-a"); run.State != stores.Suspended {
				t.Fatalf("loser run-a state = %v, want unchanged Suspended", run.State)
			}
			return
		}
		if run := runByID(t, ctx, runs, "run-a"); run.State != stores.Resuming {
			t.Fatalf("winner run-a state = %v, want Resuming", run.State)
		}
	})

	t.Run("stores.reclaim-session-exclusive", func(t *testing.T) {
		ctx := t.Context()
		db := newDB(t, "reclaim_excl")
		runs := newPgRuns(t, db)
		const sess = "s-reclaim-excl"
		seedRun(t, ctx, runs, sess, "run-a", 80*time.Millisecond)
		time.Sleep(250 * time.Millisecond)
		lb := seedRun(t, ctx, runs, sess, "run-b", time.Hour)

		const reclaimers = 4
		var mu sync.Mutex
		var reclaimErrs, heartbeatErrs int
		var wg sync.WaitGroup
		wg.Add(reclaimers + 2)
		for range reclaimers {
			go func() {
				defer wg.Done()
				_, err := runs.Reclaim(ctx, stores.Run{RunID: "run-a", SessionID: sess}, time.Hour)
				mu.Lock()
				if err == nil || !errors.Is(err, types.ErrRunActive) {
					reclaimErrs++
				}
				mu.Unlock()
			}()
		}
		for range 2 {
			go func() {
				defer wg.Done()
				_, err := runs.Heartbeat(ctx, lb)
				mu.Lock()
				if err != nil {
					heartbeatErrs++
				}
				mu.Unlock()
			}()
		}
		wg.Wait()

		mu.Lock()
		defer mu.Unlock()
		if reclaimErrs != 0 {
			t.Fatalf("%d reclaims escaped ErrRunActive while a newer run owns the session", reclaimErrs)
		}
		if heartbeatErrs != 0 {
			t.Fatalf("%d heartbeats failed on the newer owner's lease", heartbeatErrs)
		}
		loser := runByID(t, ctx, runs, "run-a")
		if loser.State != stores.Running {
			t.Fatalf("refused run-a state = %v, want unchanged Running", loser.State)
		}
		winner := runByID(t, ctx, runs, "run-b")
		if winner.State != stores.Running {
			t.Fatalf("owner run-b state = %v, want Running", winner.State)
		}
		if _, err := runs.Heartbeat(ctx, lb); err != nil {
			t.Fatalf("owner run-b lease unusable after refused reclaims: %v", err)
		}
		if !runs.SessionLeaseActive(ctx, sess) {
			t.Fatal("newer owner's lease must stay active after refused reclaims")
		}
	})

	t.Run("stores.reclaim-state-refused", func(t *testing.T) {
		ctx := t.Context()
		db := newDB(t, "reclaim_state")
		runs := newPgRuns(t, db)
		cps := newPgCheckpoints(t, db)

		const sessA = "s-reclaim-susp"
		la := seedRun(t, ctx, runs, sessA, "run-a", time.Hour)
		suspendRun(t, ctx, runs, cps, la, sessA, "")
		if _, err := runs.Reclaim(ctx, stores.Run{RunID: "run-a", SessionID: sessA}, time.Hour); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("reclaim suspended err = %v, want ErrRunNotActive", err)
		}
		if run := runByID(t, ctx, runs, "run-a"); run.State != stores.Suspended {
			t.Fatalf("refused run-a state = %v, want unchanged Suspended", run.State)
		}

		const sessB = "s-reclaim-fin"
		lb := seedRun(t, ctx, runs, sessB, "run-b", time.Hour)
		if err := runs.Finish(ctx, lb, stores.Finished, nil, ""); err != nil {
			t.Fatalf("finish run-b: %v", err)
		}
		if _, err := runs.Reclaim(ctx, stores.Run{RunID: "run-b", SessionID: sessB}, time.Hour); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("reclaim finished err = %v, want ErrRunNotActive", err)
		}
		if run := runByID(t, ctx, runs, "run-b"); run.State != stores.Finished {
			t.Fatalf("refused run-b state = %v, want unchanged Finished", run.State)
		}
	})

	t.Run("stores.optional-checkpoint-resume-port", func(t *testing.T) {
		ctx := t.Context()
		db := newDB(t, "resume_port")
		runs := newPgRuns(t, db)
		cps := newPgCheckpoints(t, db)

		var port stores.CheckpointRuns = runs
		var frozen stores.Runs = runs
		_ = frozen

		const sess = "s-resume-port"
		l := seedRun(t, ctx, runs, sess, "run-a", time.Hour)
		tok := suspendRun(t, ctx, runs, cps, l, sess, "")
		got, err := port.SuspensionToken(ctx, "run-a")
		if err != nil {
			t.Fatalf("SuspensionToken: %v", err)
		}
		if got != tok {
			t.Fatalf("SuspensionToken = %s, want %s", got, tok)
		}
	})

	t.Run("stores.resume-current-token-fenced", func(t *testing.T) {
		ctx := t.Context()
		db := newDB(t, "token_fenced")
		runs := newPgRuns(t, db)
		cps := newPgCheckpoints(t, db)
		const sess = "s-token-fenced"
		const runID = "run-a"

		l := seedRun(t, ctx, runs, sess, runID, time.Hour)
		tok1 := suspendRun(t, ctx, runs, cps, l, sess, "")
		l2, err := runs.Resuming(ctx, runID, time.Hour)
		if err != nil {
			t.Fatalf("resume for newer suspension: %v", err)
		}
		tok2 := suspendRun(t, ctx, runs, cps, l2, sess, "")

		if _, err := runs.ResumingCheckpoint(ctx, runID, tok1, time.Hour); !errors.Is(err, types.ErrTokenMismatch) {
			t.Fatalf("old token err = %v, want ErrTokenMismatch", err)
		}
		got, err := runs.SuspensionToken(ctx, runID)
		if err != nil || got != tok2 {
			t.Fatalf("SuspensionToken = (%s, %v), want (%s, nil)", got, err, tok2)
		}
		if _, err := cps.Peek(ctx, tok2); err != nil {
			t.Fatalf("newer suspension must stay usable: %v", err)
		}
		l3, err := runs.ResumingCheckpoint(ctx, runID, tok2, time.Hour)
		if err != nil {
			t.Fatalf("ResumingCheckpoint current token: %v", err)
		}
		if l3.RunID != runID || l3.Generation == 0 {
			t.Fatalf("lease = %+v, want run %s with a fresh generation", l3, runID)
		}
		if run := runByID(t, ctx, runs, runID); run.State != stores.Resuming {
			t.Fatalf("state = %v, want Resuming", run.State)
		}
	})

	t.Run("stores.postgres-ownership-database-time", func(t *testing.T) {
		ctx := t.Context()
		dba := newDB(t, "dbtime", postgres.WithCheckpointTTL(1500*time.Millisecond))
		dbb := newDB(t, "dbtime")
		runsA, runsB := newPgRuns(t, dba), newPgRuns(t, dbb)
		cpsA, cpsB := newPgCheckpoints(t, dba), newPgCheckpoints(t, dbb)
		const sess = "s-dbtime"

		seedRun(t, ctx, runsA, sess, "run-a", 1500*time.Millisecond)
		tok, err := cpsA.Put(ctx, stores.Checkpoint{RunID: "run-a", SessionID: sess})
		if err != nil {
			t.Fatalf("put checkpoint: %v", err)
		}
		var serverNow time.Time
		if err := dbb.Pool().QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&serverNow); err != nil {
			t.Fatalf("read server time: %v", err)
		}
		exp := checkpointExpiry(t, dba, tok)
		if exp == nil || exp.Sub(serverNow.Add(1500*time.Millisecond)).Abs() > 2*time.Second {
			t.Fatalf("checkpoint expiry %v, want server time plus 1500ms (server now %v)", exp, serverNow)
		}
		var heartbeat time.Time
		if err := dbb.Pool().QueryRow(ctx, `SELECT heartbeat_at FROM run_record WHERE run_id = 'run-a'`).Scan(&heartbeat); err != nil {
			t.Fatalf("read heartbeat: %v", err)
		}
		if heartbeat.Sub(serverNow).Abs() > 2*time.Second {
			t.Fatalf("heartbeat stamp %v, want server time near %v", heartbeat, serverNow)
		}

		if _, err := runsB.Start(ctx, stores.Run{SessionID: sess, RunID: "run-b"}, time.Hour); !errors.Is(err, types.ErrRunActive) {
			t.Fatalf("second instance start err = %v, want ErrRunActive by database time", err)
		}
		if _, err := cpsB.Peek(ctx, tok); err != nil {
			t.Fatalf("unexpired checkpoint must stay resumable: %v", err)
		}

		time.Sleep(2 * time.Second)

		if _, err := runsB.Start(ctx, stores.Run{SessionID: sess, RunID: "run-b"}, time.Hour); err != nil {
			t.Fatalf("start after the lease lapsed by database time: %v", err)
		}
		if _, err := cpsB.Peek(ctx, tok); !errors.Is(err, types.ErrTokenExpired) {
			t.Fatalf("expired checkpoint peek err = %v, want ErrTokenExpired", err)
		}
	})

	t.Run("stores.checkpoint-lifetime-policy", func(t *testing.T) {
		ctx := t.Context()
		dbZero := newDB(t, "cp_life_zero")
		cpsZero := newPgCheckpoints(t, dbZero)

		want := time.Now().Add(10 * time.Second)
		tokExplicit, err := cpsZero.Put(ctx, stores.Checkpoint{RunID: "run-e", SessionID: "s-cp-explicit", ExpiresAt: want})
		if err != nil {
			t.Fatalf("put explicit: %v", err)
		}
		if exp := checkpointExpiry(t, dbZero, tokExplicit); exp == nil || exp.Sub(want).Abs() > 2*time.Second {
			t.Fatalf("explicit expiry = %v, want preserved %v", exp, want)
		}
		tokNone, err := cpsZero.Put(ctx, stores.Checkpoint{RunID: "run-n", SessionID: "s-cp-none"})
		if err != nil {
			t.Fatalf("put without expiry: %v", err)
		}
		if exp := checkpointExpiry(t, dbZero, tokNone); exp != nil {
			t.Fatalf("absent expiry = %v, want unset", exp)
		}

		dbTTL := newDB(t, "cp_life_ttl", postgres.WithCheckpointTTL(time.Second))
		cpsTTL := newPgCheckpoints(t, dbTTL)
		before := time.Now()
		tokTTL, err := cpsTTL.Put(ctx, stores.Checkpoint{RunID: "run-t", SessionID: "s-cp-ttl"})
		if err != nil {
			t.Fatalf("put under ttl: %v", err)
		}
		exp := checkpointExpiry(t, dbTTL, tokTTL)
		if exp == nil || exp.Before(before.Add(500*time.Millisecond)) || exp.After(before.Add(4*time.Second)) {
			t.Fatalf("ttl expiry = %v, want store time plus 1s near %v", exp, before.Add(time.Second))
		}

		dbNeg := newDB(t, "cp_life_neg", postgres.WithCheckpointTTL(-time.Second))
		cpsNeg := newPgCheckpoints(t, dbNeg)
		if _, err := cpsNeg.Put(ctx, stores.Checkpoint{RunID: "run-x", SessionID: "s-cp-neg"}); err == nil {
			t.Fatal("negative checkpoint ttl must refuse the write")
		}
		if n := count(t, dbNeg, `SELECT count(*) FROM cp_life_neg.checkpoint_record WHERE session_id = 's-cp-neg'`); n != 0 {
			t.Fatalf("refused write left %d rows, want 0", n)
		}
	})

	t.Run("stores.resume-ready-filter-before-limit", func(t *testing.T) {
		ctx := t.Context()
		db := newDB(t, "rr_filter")
		runs := newPgRuns(t, db)
		cps := newPgCheckpoints(t, db)

		lx := seedRun(t, ctx, runs, "s-rr-x", "run-x", time.Hour)
		tokX, err := cps.Put(ctx, stores.Checkpoint{RunID: "run-x", SessionID: "s-rr-x"})
		if err != nil {
			t.Fatalf("put run-x checkpoint: %v", err)
		}
		if _, err := cps.Consume(ctx, tokX, stores.ResumeInput{}); err != nil {
			t.Fatalf("consume run-x checkpoint: %v", err)
		}
		if err := runs.Finish(ctx, lx, stores.Finished, nil, ""); err != nil {
			t.Fatalf("finish run-x: %v", err)
		}

		ly := seedRun(t, ctx, runs, "s-rr-y", "run-y", time.Hour)
		tokY, err := cps.Put(ctx, stores.Checkpoint{RunID: "run-y", SessionID: "s-rr-y"})
		if err != nil {
			t.Fatalf("put run-y checkpoint: %v", err)
		}
		if _, err := cps.Consume(ctx, tokY, stores.ResumeInput{}); err != nil {
			t.Fatalf("consume run-y checkpoint: %v", err)
		}
		if err := runs.Suspend(ctx, ly, tokY); err != nil {
			t.Fatalf("suspend run-y: %v", err)
		}

		ready, err := cps.ResumeReady(ctx, 1)
		if err != nil {
			t.Fatalf("ResumeReady: %v", err)
		}
		if len(ready) != 1 || ready[0].RunID != "run-y" {
			t.Fatalf("ResumeReady(1) = %d rows (first %v), want only the eligible newer run-y", len(ready), ready)
		}
	})

	t.Run("stores.preempted-filter-before-limit", func(t *testing.T) {
		ctx := t.Context()
		db := newDB(t, "preempted_filter")
		runs := newPgRuns(t, db)
		cps := newPgCheckpoints(t, db)

		lh := seedRun(t, ctx, runs, "s-pre-h", "run-h", time.Hour)
		suspendRun(t, ctx, runs, cps, lh, "s-pre-h", types.HumanApproval)
		lp := seedRun(t, ctx, runs, "s-pre-p", "run-p", time.Hour)
		suspendRun(t, ctx, runs, cps, lp, "s-pre-p", types.Preempted)

		out, err := runs.Preempted(ctx, 1)
		if err != nil {
			t.Fatalf("Preempted: %v", err)
		}
		if len(out) != 1 || out[0].RunID != "run-p" {
			t.Fatalf("Preempted(1) = %d rows, want only the newer preempted run-p", len(out))
		}
		out, err = runs.Preempted(ctx, 10)
		if err != nil {
			t.Fatalf("Preempted: %v", err)
		}
		if len(out) != 1 || out[0].RunID != "run-p" {
			t.Fatalf("Preempted(10) = %d rows, want the human approval filtered out", len(out))
		}
	})
}
