package gohan

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// errFencedInject is the deterministic lease failure the one-shot
// decorator parks between the conditional consume and the token-fenced
// lease acquisition.
var errFencedInject = errors.New("pod died before the resume lease")

// oneShotFenceRuns fails the first token-fenced lease acquisition and
// delegates every later call: the deterministic shape of a pod dying
// between ConsumeIf and ResumingCheckpoint.
type oneShotFenceRuns struct {
	*stores.MemoryRuns
	used atomic.Bool
}

func (r *oneShotFenceRuns) ResumingCheckpoint(ctx context.Context, runID string, t types.ResumeToken, ttl time.Duration) (stores.Lease, error) {
	if !r.used.Swap(true) {
		return stores.Lease{}, errFencedInject
	}
	return r.MemoryRuns.ResumingCheckpoint(ctx, runID, t, ttl)
}

// frozenPortRuns wraps Runs without the optional PreemptedLister and
// CheckpointRuns surfaces, standing in for a store built before the
// token-fenced resume existed.
type frozenPortRuns struct{ stores.Runs }

// frozenPortCheckpoints keeps the resume surface (Peek, ConsumeIf,
// UpdatePending) and drops the optional discovery lister, standing in for
// a checkpoint store without recovery discovery.
type frozenPortCheckpoints struct {
	stores.Checkpoints
	stores.CheckpointResumer
}

func fixtureConversation(t *testing.T, f *recoverFixture, runs stores.Runs, cps stores.Checkpoints) Conversation {
	t.Helper()
	conv, err := NewConversation(f.stack, "agent", f.rt,
		WithConversationRuns(runs),
		WithConversationCheckpoints(cps),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	return conv
}

func TestRecoveryContract(t *testing.T) {
	t.Run("recovery.empty-continue-consumed", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		f.seedHistory(t, 1)
		conv := fixtureConversation(t, f, &oneShotFenceRuns{MemoryRuns: f.runs}, f.cps)
		tok := f.seedPreempted(t, "r1")

		// The public resume crosses the crash window: the empty
		// continuation is consumed, the lease fails, the run stays
		// Suspended.
		var resumeErr error
		for _, err := range ResumePreempted(ctx, conv, tok) {
			resumeErr = err
		}
		if !errors.Is(resumeErr, errFencedInject) {
			t.Fatalf("resume error = %v, want the parked lease failure", resumeErr)
		}
		if _, err := f.cps.Peek(ctx, tok); !errors.Is(err, types.ErrTokenConsumed) {
			t.Fatalf("peek after failed resume = %v, want ErrTokenConsumed", err)
		}
		if st := runState(t, f.runs, "r1"); st != stores.Suspended {
			t.Fatalf("run state = %v, want Suspended", st)
		}

		// One recovery pass completes the recorded decision exactly once.
		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if n := f.rt.modelCalls(); n != 1 {
			t.Fatalf("model calls = %d, want 1", n)
		}
		if st := runState(t, f.runs, "r1"); st != stores.Finished {
			t.Fatalf("run state = %v, want Finished", st)
		}
		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if n := f.rt.modelCalls(); n != 1 {
			t.Fatalf("model calls after second recover = %d, want 1", n)
		}
	})

	t.Run("recovery.preemption-token-consumed", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		f.seedHistory(t, 1)
		conv := fixtureConversation(t, f, f.runs, f.cps)
		tokA := f.seedPreempted(t, "r1")

		// The client recorded its decision on A; the lease never landed.
		if _, err := f.cps.Consume(ctx, tokA, Continue()); err != nil {
			t.Fatal(err)
		}
		// The run re-suspends on a newer token.
		lease2, err := f.runs.Resuming(ctx, "r1", stores.LeaseTTL)
		if err != nil {
			t.Fatal(err)
		}
		owner := types.Principal{Subject: "u1", Tenant: "t1"}
		tokCtx := types.WithRunInfo(ctx, types.RunInfo{SessionID: "s1", RunID: "r1", Flow: "agent"})
		tokB, err := f.cps.Put(WithPrincipal(tokCtx, owner), stores.Checkpoint{
			RunID:      "r1",
			SessionID:  "s1",
			Flow:       "agent",
			Reason:     types.Preempted,
			Originator: owner,
			Data:       mustJSON(t, runtime.State{Turn: 2, HistoryVersion: 1}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.runs.SuspendWithReason(t.Context(), lease2, tokB, types.Preempted); err != nil {
			t.Fatal(err)
		}

		// A cannot resume, and B stays the current suspension.
		var resumeErr error
		for _, err := range conv.Resume(ctx, tokA, Continue()) {
			resumeErr = err
		}
		if !errors.Is(resumeErr, types.ErrTokenConsumed) {
			t.Fatalf("resume of consumed token = %v, want ErrTokenConsumed", resumeErr)
		}
		got, err := f.runs.SuspensionToken(ctx, "r1")
		if err != nil || got != tokB {
			t.Fatalf("suspension token = %q err %v, want the newer %q", got, err, tokB)
		}

		// Recovery completes the current suspension exactly once.
		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if n := f.rt.modelCalls(); n != 1 {
			t.Fatalf("model calls = %d, want 1", n)
		}
		if st := runState(t, f.runs, "r1"); st != stores.Finished {
			t.Fatalf("run state = %v, want Finished", st)
		}
	})

	t.Run("recovery.human-approval-not-preempted", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		f.seedHistory(t, 1)
		_, lease := f.seedRunLease(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "agent", State: stores.Running, Turn: 1, Seq: 1})
		owner := types.Principal{Subject: "u1", Tenant: "t1"}
		tokCtx := types.WithRunInfo(ctx, types.RunInfo{SessionID: "s1", RunID: "r1", Flow: "agent"})
		tok, err := f.cps.Put(WithPrincipal(tokCtx, owner), stores.Checkpoint{
			RunID:      "r1",
			SessionID:  "s1",
			Flow:       "agent",
			Reason:     types.HumanApproval,
			Originator: owner,
			Data:       mustJSON(t, runtime.State{Turn: 1, HistoryVersion: 1, Pending: pendingCalls("c1")}),
		})
		if err != nil {
			t.Fatal(err)
		}
		// The pod parked the run Preempted at a safe point, but the
		// checkpoint itself still waits on a human approval.
		if err := f.runs.SuspendWithReason(t.Context(), lease, tok, types.Preempted); err != nil {
			t.Fatal(err)
		}

		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if n := f.rt.modelCalls(); n != 0 {
			t.Fatalf("model calls = %d, want 0: recovery decided the approval", n)
		}
		if st := runState(t, f.runs, "r1"); st != stores.Suspended {
			t.Fatalf("run state = %v, want Suspended", st)
		}
		if _, err := f.cps.Peek(ctx, tok); err != nil {
			t.Fatalf("peek after recovery = %v, want the token pending", err)
		}
	})

	t.Run("recovery.optional-token-surface-absent", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		f.seedHistory(t, 1)
		frozenRuns := &frozenPortRuns{Runs: f.runs}
		frozenCps := &frozenPortCheckpoints{Checkpoints: f.cps, CheckpointResumer: f.cps}
		st, err := Build(
			WithStores(stores.Stores{SessionLog: f.log, Runs: frozenRuns, Checkpoints: frozenCps, Journal: f.jnl}),
			WithRecoveryRuntime("agent", f.rt),
		)
		if err != nil {
			t.Fatal(err)
		}
		conv, err := NewConversation(st, "agent", f.rt,
			WithConversationRuns(frozenRuns),
			WithConversationCheckpoints(frozenCps),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		tok := f.seedPreempted(t, "r1")

		// External resume refuses before consuming.
		var resumeErr error
		for _, err := range conv.Resume(ctx, tok, Continue()) {
			resumeErr = err
		}
		if !errors.Is(resumeErr, types.ErrCheckpointIncompatible) {
			t.Fatalf("resume without the fenced surface = %v, want ErrCheckpointIncompatible", resumeErr)
		}
		if _, err := f.cps.Peek(ctx, tok); err != nil {
			t.Fatalf("peek after refused resume = %v, want the token untouched", err)
		}

		// Automatic preempted and consumed recovery skip the unsupported
		// path without error.
		if err := st.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if n := f.rt.modelCalls(); n != 0 {
			t.Fatalf("model calls = %d, want 0", n)
		}
		if st := runState(t, f.runs, "r1"); st != stores.Suspended {
			t.Fatalf("run state = %v, want Suspended", st)
		}

		// Ordinary stale-run recovery stays available.
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "stale1", Flow: "agent", State: stores.Running, Turn: 1, Seq: 1})
		f.expire()
		if err := st.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if n := f.rt.modelCalls(); n != 1 {
			t.Fatalf("model calls = %d, want 1", n)
		}
		if st := runState(t, f.runs, "stale1"); st != stores.Finished {
			t.Fatalf("run state = %v, want Finished", st)
		}

		// New sends stay available on the frozen store.
		stack, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		stack.stores = stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}
		mem := stores.NewMemoryCheckpoints()
		sendConv, err := NewConversation(stack, "chat", &preemptConvRT{log: &appendSpy{}},
			WithConversationRuns(&frozenPortRuns{Runs: stores.NewMemoryRuns()}),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
			WithConversationCheckpoints(&frozenPortCheckpoints{Checkpoints: mem, CheckpointResumer: mem}),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		done := false
		for ev, err := range sendConv.Send(principalCtx(context.Background()), "s-send", userMsg("hi")) {
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			if _, ok := ev.(types.Done); ok {
				done = true
			}
		}
		if !done {
			t.Fatal("send without suspension did not finish")
		}
	})
}
