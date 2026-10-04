package gohan

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// recoverRuntime is the fake backend Recover re-drives through. It models
// the real step: a batch of pending calls goes through the journal, its
// results append to the session log, then one model call finishes the run.
type recoverRuntime struct {
	journal  stores.Journal
	log      stores.SessionLog
	session  string
	mu       sync.Mutex
	models   int
	executed []string
	replayed []string
	seenInfo []types.RunInfo
}

func (r *recoverRuntime) Name() string                         { return "recover.test" }
func (r *recoverRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

func (r *recoverRuntime) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *recoverRuntime) modelCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.models
}

func (r *recoverRuntime) calls() (exec, replay []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.executed...), append([]string(nil), r.replayed...)
}

func (r *recoverRuntime) infos() []types.RunInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]types.RunInfo(nil), r.seenInfo...)
}

func (r *recoverRuntime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if len(st.Pending) > 0 {
		msg := types.Message{Role: types.RoleAssistant}
		for _, call := range st.Pending {
			key := types.CallKey{SessionID: r.session, CallID: call.ID}
			entry, replayed, err := r.journal.Reserve(ctx, key, stores.Fingerprint("fp-"+call.ID))
			if err != nil {
				return st, nil, 0, err
			}
			// A Completed entry replays its recorded result; a Reserved
			// one re-executes with its pinned key.
			if !replayed && entry.State == stores.Completed {
				r.mu.Lock()
				r.replayed = append(r.replayed, call.ID)
				r.mu.Unlock()
			} else {
				r.mu.Lock()
				r.executed = append(r.executed, call.ID)
				r.mu.Unlock()
				entry.Result = types.ToolResult{ID: call.ID, Outcome: types.Succeeded}
				if err := r.journal.Complete(ctx, key, entry.Result); err != nil {
					return st, nil, 0, err
				}
			}
			msg.Blocks = append(msg.Blocks, entry.Result)
		}
		ver, err := r.log.Append(ctx, r.session, st.HistoryVersion, msg)
		if err != nil {
			return st, nil, 0, err
		}
		return runtime.State{Turn: st.Turn + 1, HistoryVersion: ver}, nil, runtime.Continue, nil
	}
	r.mu.Lock()
	r.models++
	if info, ok := types.RunInfoFrom(ctx); ok {
		r.seenInfo = append(r.seenInfo, info)
	}
	r.mu.Unlock()
	return st, nil, runtime.DoneStatus, nil
}

type recoverFixture struct {
	stack *Stack
	runs  *stores.MemoryRuns
	cps   *stores.MemoryCheckpoints
	log   stores.SessionLog
	jnl   stores.Journal
	rt    *recoverRuntime
	now   time.Time
}

// reaperCtx is the harness principal a reaper drive carries; the session
// log scopes appends with it.
func (f *recoverFixture) reaperCtx() context.Context {
	return WithPrincipal(context.Background(), types.Principal{Subject: "reaper", Tenant: "t1"})
}

func newRecoverFixture(t *testing.T, flows ...string) *recoverFixture {
	t.Helper()
	f := &recoverFixture{now: time.Unix(0, 0)}
	tick := func() time.Time { return f.now }
	f.runs = stores.NewMemoryRuns(stores.WithMemoryRunClock(tick))
	f.cps = stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(tick), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))
	f.log = stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	f.jnl = stores.NewMemoryJournal(stores.WithMemoryJournalClock(tick))
	f.rt = &recoverRuntime{journal: f.jnl, log: f.log, session: "s1"}
	opts := []Option{
		WithStores(stores.Stores{SessionLog: f.log, Runs: f.runs, Checkpoints: f.cps, Journal: f.jnl}),
	}
	for _, flow := range flows {
		opts = append(opts, WithRecoveryRuntime(flow, f.rt))
	}
	st, err := Build(opts...)
	if err != nil {
		t.Fatal(err)
	}
	f.stack = st
	return f
}

// seedRun starts a run with a live lease and returns it.
func (f *recoverFixture) seedRun(t *testing.T, run stores.Run) stores.Run {
	t.Helper()
	f.seedRunLease(t, run)
	return run
}

func (f *recoverFixture) seedRunLease(t *testing.T, run stores.Run) (stores.Run, stores.Lease) {
	t.Helper()
	lease, err := f.runs.Start(t.Context(), run, stores.LeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	return run, lease
}

// seedHistory advances the session log to the given version, so a replay
// appending at run.Seq matches.
func (f *recoverFixture) seedHistory(t *testing.T, to int64) {
	t.Helper()
	for i := int64(0); i < to; i++ {
		if _, err := f.log.Append(f.reaperCtx(), "s1", i, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "m"}}}); err != nil {
			t.Fatal(err)
		}
	}
}

// expire ages the store clock past the lease TTL, so the seeded runs stale.
func (f *recoverFixture) expire() { f.now = f.now.Add(2 * stores.LeaseTTL) }

func pendingCalls(ids ...string) []types.ToolUse {
	calls := make([]types.ToolUse, 0, len(ids))
	for _, id := range ids {
		calls = append(calls, types.ToolUse{ID: id, Name: "create_booking", Args: pendingArgs(id)})
	}
	return calls
}

func pendingArgs(s string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"id": s})
	return b
}

// completeInJournal records a completed journal entry for call id.
func (f *recoverFixture) completeInJournal(t *testing.T, id string) {
	t.Helper()
	k := types.CallKey{SessionID: "s1", CallID: id}
	if _, _, err := f.jnl.Reserve(f.reaperCtx(), k, stores.Fingerprint("fp-"+id)); err != nil {
		t.Fatal(err)
	}
	if err := f.jnl.Complete(f.reaperCtx(), k, types.ToolResult{ID: id, Outcome: types.Succeeded}); err != nil {
		t.Fatal(err)
	}
}

func TestRecover(t *testing.T) {
	t.Run("recovery.pod-dies-mid-turn", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		f.completeInJournal(t, "c1")
		f.seedHistory(t, 3)
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "agent", State: stores.Running, Turn: 2, Seq: 3, Pending: pendingCalls("c1", "c2")})
		f.expire()

		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		exec, replay := f.rt.calls()
		if len(replay) != 1 || replay[0] != "c1" {
			t.Fatalf("replayed = %v, want [c1]", replay)
		}
		if len(exec) != 1 || exec[0] != "c2" {
			t.Fatalf("executed = %v, want [c2]", exec)
		}
		if f.rt.modelCalls() != 1 {
			t.Fatalf("model calls = %d, want 1", f.rt.modelCalls())
		}
		if h := f.runs.SessionLeaseActive(t.Context(), "s1"); h {
			t.Fatal("run still holds a lease after recovery")
		}
	})

	t.Run("recovery.pod-dies-inside-a-side-effect", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		k := types.CallKey{SessionID: "s1", CallID: "c1"}
		if _, _, err := f.jnl.Reserve(t.Context(), k, stores.Fingerprint("fp-c1")); err != nil {
			t.Fatal(err)
		}
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "agent", State: stores.Running, Turn: 1, Seq: 2, Pending: pendingCalls("c1")})
		f.expire()

		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		exec, replay := f.rt.calls()
		if len(exec) != 1 || exec[0] != "c1" {
			t.Fatalf("executed = %v, want [c1] re-executed", exec)
		}
		if len(replay) != 0 {
			t.Fatalf("a reserved entry replayed: %v", replay)
		}
		entries, err := f.jnl.ByFingerprint(t.Context(), "s1", "fp-c1")
		if err != nil || len(entries) != 1 || entries[0].Key != "c1" {
			t.Fatalf("pinned key lost: entries=%d err=%v key=%q", len(entries), err, entries[0].Key)
		}
	})

	t.Run("recovery.headless-recovery-impossible", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		uncertain := []types.CallKey{{SessionID: "s1", CallID: "c9"}}
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "r9", Flow: "unregistered", State: stores.Running, Uncertain: uncertain})
		f.expire()

		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		// gohan.run.abandoned records the abandonment here; the metric
		// itself lands with telemetry (row 28).
		if f.rt.modelCalls() != 0 {
			t.Fatalf("unregistered flow drove a model call")
		}
		// The session is consistent for the next Send: a new run starts.
		if _, err := f.runs.Start(t.Context(), stores.Run{SessionID: "s1", RunID: "r10", Flow: "agent"}, stores.LeaseTTL); err != nil {
			t.Fatalf("next Send would fail: %v", err)
		}
	})

	t.Run("recovery.no-double-run", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "agent"})
		events := stores.NewMemoryEventLog()
		conv, err := NewConversation(f.stack, "agent", f.rt,
			WithConversationRuns(f.runs), WithConversationEventLog(events))
		if err != nil {
			t.Fatal(err)
		}
		ctx := WithPrincipal(t.Context(), types.Principal{Subject: "u1", Tenant: "t1"})
		for _, err := range conv.Send(ctx, "s1", Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}) {
			if !isErrRunActive(err) {
				t.Fatalf("Send error = %v, want ErrRunActive", err)
			}
		}
		if f.rt.modelCalls() != 0 {
			t.Fatalf("refused Send made %d model calls", f.rt.modelCalls())
		}
	})

	t.Run("identity.nested-spans-and-recovery", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "root", Flow: "agent", State: stores.Running, Turn: 1, Seq: 1})
		f.seedRun(t, stores.Run{SessionID: "s1/child", RunID: "child", RootRunID: "root", ParentRunID: "root", Depth: 1, Flow: "agent", State: stores.Running, Turn: 1, Seq: 1})
		f.expire()

		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		infos := f.rt.infos()
		if len(infos) != 2 {
			t.Fatalf("replayed %d runs, want 2", len(infos))
		}
		if infos[0].RunID != "root" || infos[0].Depth != 0 {
			t.Fatalf("first replayed run = %+v, want the root", infos[0])
		}
		if infos[1].RunID != "child" || infos[1].RootRunID != "root" || infos[1].ParentRunID != "root" || infos[1].Depth != 1 {
			t.Fatalf("child replayed outside the tree: %+v", infos[1])
		}
		// The span-nesting half of this scenario is telemetry's (row 28).
	})

	t.Run("only one racing reaper wins", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "agent", State: stores.Running, Turn: 1, Seq: 1})
		f.expire()

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				errs[i] = f.stack.Recover(t.Context(), 10)
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("reaper %d: %v", i, err)
			}
		}
		if n := f.rt.modelCalls(); n != 1 {
			t.Fatalf("model calls = %d, want exactly one replay", n)
		}
	})

	t.Run("second recover after a completed one is a no-op", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "agent", State: stores.Running, Turn: 1, Seq: 1})
		f.expire()
		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if n := f.rt.modelCalls(); n != 1 {
			t.Fatalf("model calls = %d, want 1", n)
		}
	})

	t.Run("resuming run re-drives from the persisted input", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		_, lease := f.seedRunLease(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "agent", State: stores.Running})
		tokCtx := types.WithRunInfo(t.Context(), types.RunInfo{RunID: "r1"})
		token, err := f.cps.Put(WithPrincipal(tokCtx, types.Principal{Subject: "u1", Tenant: "t1"}), stores.Checkpoint{
			SessionID:  "s1",
			Originator: types.Principal{Subject: "u1", Tenant: "t1"},
			Data:       mustJSON(t, runtime.State{Turn: 3, Pending: pendingCalls("c1")}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.runs.Suspend(t.Context(), lease, token); err != nil {
			t.Fatal(err)
		}
		if _, err := f.runs.Resuming(t.Context(), "r1", stores.LeaseTTL); err != nil {
			t.Fatal(err)
		}
		// The client consumed the token on another pod before that pod died.
		if _, err := f.cps.Consume(t.Context(), token, stores.ResumeInput{Verdict: stores.VerdictApprove}); err != nil {
			t.Fatal(err)
		}
		f.expire()

		if err := f.stack.Recover(f.reaperCtx(), 10); err != nil {
			t.Fatal(err)
		}
		exec, _ := f.rt.calls()
		if len(exec) != 1 || exec[0] != "c1" {
			t.Fatalf("executed = %v, want [c1]", exec)
		}
		if f.rt.modelCalls() != 1 {
			t.Fatalf("model calls = %d, want 1", f.rt.modelCalls())
		}
	})
}

func isErrRunActive(err error) bool {
	return err != nil && err.Error() == types.ErrRunActive.Error()
}
