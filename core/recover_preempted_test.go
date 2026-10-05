package gohan

import (
	"context"
	"encoding/json"
	"sync/atomic"
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
		// The client is alive: its resume already took the run lease, so
		// the reaper must not touch the run.
		if _, err := f.runs.Resuming(ctx, "pre1", stores.LeaseTTL); err != nil {
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

	t.Run("recovery.consumed-preempted-is-recoverable", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		ownerCtx := WithPrincipal(context.Background(), types.Principal{Subject: "u1", Tenant: "t1"})
		if _, err := f.log.Append(ownerCtx, "s1", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "m"}}}); err != nil {
			t.Fatal(err)
		}
		lease, err := f.runs.Start(ctx, stores.Run{
			SessionID: "s1", RunID: "pre1", Flow: "agent", State: stores.Running, Turn: 1, Seq: 1,
		}, stores.LeaseTTL)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(runtime.State{Turn: 1, HistoryVersion: 1, Pending: pendingCalls("c1")})
		if err != nil {
			t.Fatal(err)
		}
		tokCtx := types.WithRunInfo(ctx, types.RunInfo{SessionID: "s1", RunID: "pre1", Flow: "agent"})
		tok, err := f.cps.Put(tokCtx, stores.Checkpoint{
			RunID:      "pre1",
			SessionID:  "s1",
			Flow:       "agent",
			Reason:     types.Preempted,
			Originator: types.Principal{Subject: "u1", Tenant: "t1"},
			Data:       data,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.runs.Suspend(ctx, lease, tok); err != nil {
			t.Fatal(err)
		}
		// The client consumed the token, then died before Runs.Resuming.
		if _, err := f.cps.Consume(ctx, tok, stores.ResumeInput{Verdict: stores.VerdictApprove, Reason: "client"}); err != nil {
			t.Fatal(err)
		}

		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		exec, _ := f.rt.calls()
		if len(exec) != 1 || exec[0] != "c1" {
			t.Fatalf("executed = %v, want the recorded input's [c1]", exec)
		}
		if f.rt.modelCalls() != 1 {
			t.Fatalf("model calls = %d, want 1", f.rt.modelCalls())
		}
		final, err := f.runs.ByID(ctx, "pre1")
		if err != nil {
			t.Fatal(err)
		}
		if final.State != stores.Finished {
			t.Fatalf("run state = %v, want Finished", final.State)
		}

		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if f.rt.modelCalls() != 1 {
			t.Fatalf("model calls after second recover = %d, want 1", f.rt.modelCalls())
		}

		// A live client lease still wins: its resume flipped the run to
		// Resuming, and the reaper leaves it alone.
		f2 := newRecoverFixture(t, "agent")
		f2.seedHistory(t, 1)
		tok2 := f2.seedPreempted(t, "pre1")
		if _, err := f2.cps.Consume(ctx, tok2, stores.ResumeInput{Reason: "client"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f2.runs.Resuming(ctx, "pre1", stores.LeaseTTL); err != nil {
			t.Fatal(err)
		}
		if err := f2.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if f2.rt.modelCalls() != 0 {
			t.Fatalf("live lease reaped: model calls = %d, want 0", f2.rt.modelCalls())
		}
	})

	t.Run("recovered run suspends again with full wiring", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		ownerCtx := WithPrincipal(context.Background(), types.Principal{Subject: "u1", Tenant: "t1"})
		if _, err := f.log.Append(ownerCtx, "s1", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "m"}}}); err != nil {
			t.Fatal(err)
		}
		sus := &resuspendRuntime{recoverRuntime: f.rt}
		st, err := Build(
			WithStores(stores.Stores{SessionLog: f.log, Runs: f.runs, Checkpoints: f.cps, Journal: f.jnl}),
			WithRecoveryRuntime("agent", sus),
		)
		if err != nil {
			t.Fatal(err)
		}
		tok := f.seedPreempted(t, "pre1")
		if _, err := f.cps.Consume(ctx, tok, stores.ResumeInput{Reason: "client"}); err != nil {
			t.Fatal(err)
		}
		sus.suspend.Store(true)

		if err := st.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		final, err := f.runs.ByID(ctx, "pre1")
		if err != nil {
			t.Fatal(err)
		}
		if final.State != stores.Suspended {
			t.Fatalf("run state = %v, want Suspended", final.State)
		}
		cp, _, err := f.cps.PendingInput(ctx, "pre1")
		if err != nil {
			t.Fatal(err)
		}
		if cp.Originator.Subject != "u1" || cp.Originator.Tenant != "t1" {
			t.Fatalf("second checkpoint originator = %+v, want u1/t1", cp.Originator)
		}
	})
}

// resuspendRuntime drives the first recovered step into a fresh
// suspension, so the re-drive proves it can park the run a second time.
type resuspendRuntime struct {
	*recoverRuntime
	suspend atomic.Bool
}

func (r *resuspendRuntime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if r.suspend.Load() {
		return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion}, nil, runtime.SuspendedStatus, nil
	}
	return r.recoverRuntime.Step(ctx, st)
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
