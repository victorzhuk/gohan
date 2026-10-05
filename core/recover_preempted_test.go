package gohan

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// bareRuns hides MemoryRuns' PreemptedLister so Recover falls back to the
// stale-only path.
type bareRuns struct {
	stores.Runs
}

func TestRecoverPreemptedFirst(t *testing.T) {
	t.Run("recovery.preempted-before-stale", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		f.seedHistory(t, 1)
		f.seedRun(t, stores.Run{SessionID: "s2", RunID: "stale1", Flow: "agent", State: stores.Running, Turn: 1, Seq: 1})
		f.seedPreempted(t, "pre1")
		// Two seconds in: the preempted run is nowhere near stale.
		f.now = f.now.Add(2 * time.Second)

		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		infos := f.rt.infos()
		if len(infos) != 1 || infos[0].RunID != "pre1" {
			t.Fatalf("model calls = %+v, want only the preempted run first", infos)
		}
		// The stale run waits out its lease; the preempted one did not.
		if !f.runs.SessionLeaseActive(t.Context(), "s2") {
			t.Fatal("stale run reclaimed before its lease expired")
		}
	})

	t.Run("preempted resumed without waiting out LeaseTTL", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		f.seedHistory(t, 1)
		f.seedPreempted(t, "pre1")

		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if f.rt.modelCalls() != 1 {
			t.Fatalf("model calls = %d, want 1 with the clock unmoved", f.rt.modelCalls())
		}
		if f.runs.SessionLeaseActive(t.Context(), "s1") {
			t.Fatal("run still holds a lease after resumption")
		}
	})

	t.Run("consumed token skipped silently", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		f.seedHistory(t, 1)
		tok := f.seedPreempted(t, "pre1")
		if _, err := f.cps.Consume(ctx, tok, stores.ResumeInput{Reason: "client"}); err != nil {
			t.Fatal(err)
		}

		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatalf("consumed token must be skipped without error: %v", err)
		}
		if f.rt.modelCalls() != 0 {
			t.Fatalf("model calls = %d, want 0", f.rt.modelCalls())
		}
	})

	t.Run("second Recover does not resume twice", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		f.seedHistory(t, 1)
		f.seedPreempted(t, "pre1")

		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if f.rt.modelCalls() != 1 {
			t.Fatalf("model calls = %d, want 1", f.rt.modelCalls())
		}
	})

	t.Run("store without PreemptedLister still recovers stale runs", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		f.completeInJournal(t, "c1")
		f.seedHistory(t, 3)
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "agent", State: stores.Running, Turn: 2, Seq: 3, Pending: pendingCalls("c1", "c2")})
		f.expire()

		st, err := Build(
			WithStores(stores.Stores{SessionLog: f.log, Runs: bareRuns{f.runs}, Checkpoints: f.cps, Journal: f.jnl}),
			WithRecoveryRuntime("agent", f.rt),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		exec, replay := f.rt.calls()
		if len(replay) != 1 || replay[0] != "c1" || len(exec) != 1 || exec[0] != "c2" {
			t.Fatalf("exec=%v replay=%v, want c2 executed and c1 replayed", exec, replay)
		}
	})
}

// seedPreempted parks a run Preempted at a safe point: a live Suspended
// row with a fresh heartbeat and a checkpoint carrying the state to
// re-drive. It returns the single-use resume token a client would hold.
func (f *recoverFixture) seedPreempted(t *testing.T, runID string) types.ResumeToken {
	t.Helper()
	lease, err := f.runs.Start(t.Context(), stores.Run{
		SessionID: "s1", RunID: runID, Flow: "agent", State: stores.Running, Turn: 1, Seq: 1,
	}, stores.LeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(runtime.State{Turn: 1, HistoryVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := types.WithRunInfo(f.reaperCtx(), types.RunInfo{SessionID: "s1", RunID: runID, Flow: "agent"})
	tok, err := f.cps.Put(ctx, stores.Checkpoint{
		RunID:      runID,
		SessionID:  "s1",
		Flow:       "agent",
		Reason:     types.Preempted,
		Originator: types.Principal{Subject: "u1", Tenant: "t1"},
		Data:       data,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.runs.Suspend(t.Context(), lease, tok); err != nil {
		t.Fatal(err)
	}
	return tok
}
