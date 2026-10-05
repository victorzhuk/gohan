package gohan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// staleClock gives a runs store a clock the test advances past the lease
// TTL, so a seeded run goes stale on demand.
func staleClock() (func() time.Time, func()) {
	now := time.Unix(0, 0)
	tick := func() time.Time { return now }
	return tick, func() { now = now.Add(2 * stores.LeaseTTL) }
}

func seedNativeHistory(t *testing.T, log stores.SessionLog, sessionID string, msgs ...types.Message) int64 {
	t.Helper()
	ver := int64(0)
	for _, m := range msgs {
		v, err := log.Append(nrrCtx(), sessionID, ver, m)
		if err != nil {
			t.Fatalf("seed history: %v", err)
		}
		ver = v
	}
	return ver
}

func assistantCalls(calls ...types.ToolUse) types.Message {
	msg := types.Message{Role: types.RoleAssistant}
	for _, c := range calls {
		msg.Blocks = append(msg.Blocks, c)
	}
	return msg
}

func resultMsg(results ...types.ToolResult) types.Message {
	msg := types.Message{Role: types.RoleUser}
	for _, r := range results {
		msg.Blocks = append(msg.Blocks, r)
	}
	return msg
}

func countBlocks(t *testing.T, log stores.SessionLog, sessionID string, match func(types.Block) bool) int {
	t.Helper()
	h, err := log.Load(nrrCtx(), sessionID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	n := 0
	for _, m := range h.Messages {
		for _, b := range m.Blocks {
			if match(b) {
				n++
			}
		}
	}
	return n
}

func noteResult(id string) types.ToolResult {
	return types.ToolResult{ID: id, Content: []types.Block{types.Text{Text: "noted " + id}}, Outcome: types.Succeeded}
}

func runState(t *testing.T, runs stores.Runs, runID string) stores.RunState {
	t.Helper()
	rf, ok := runs.(runFinder)
	if !ok {
		t.Fatal("runs store has no by-id reader")
	}
	r, err := rf.ByID(context.Background(), runID)
	if err != nil {
		t.Fatalf("run by id: %v", err)
	}
	return r.State
}

// A stale Running native run re-enters from the durable history: a call
// whose result is already recorded never re-executes, an unresolved call
// runs exactly once, and the re-drive finishes without a phase decode
// failure.
func TestNativeRecoverRunningRun(t *testing.T) {
	model := &nrrModel{}
	book := &bookTool{}
	note := &nrrNoteTool{}
	tick, expire := staleClock()
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(tick))
	stack, _ := nrrStack(t, model, book, note, nil, runs, WithRecoveryRuntime("chat", runtime.NewNative()))
	log := stack.stores.SessionLog
	ver := seedNativeHistory(t, log, "s1",
		nlUser("go"),
		assistantCalls(nrrNote("n0")),
		resultMsg(noteResult("n0")),
		assistantCalls(nrrNote("n1")),
	)
	model.reset()
	if _, err := runs.Start(nrrCtx(), stores.Run{SessionID: "s1", RunID: "r1", Flow: "chat", State: stores.Running, Turn: 1, Seq: ver}, stores.LeaseTTL); err != nil {
		t.Fatal(err)
	}
	expire()

	if err := stack.Recover(nrrCtx(), 10); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if ran := note.ran(); len(ran) != 1 || ran[0] != "n1" {
		t.Fatalf("executed = %v, want only the unresolved n1", ran)
	}
	if n := countBlocks(t, log, "s1", func(b types.Block) bool {
		tr, ok := b.(types.ToolResult)
		return ok && tr.ID == "n0"
	}); n != 1 {
		t.Fatalf("n0 results = %d, want the recorded one only", n)
	}
	if n := countBlocks(t, log, "s1", func(b types.Block) bool {
		tr, ok := b.(types.ToolResult)
		return ok && tr.ID == "n1"
	}); n != 1 {
		t.Fatalf("n1 results = %d, want exactly one", n)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}
	if st := runState(t, runs, "r1"); st != stores.Finished {
		t.Fatalf("run state = %v, want Finished", st)
	}
	if ran := book.ran(); len(ran) != 0 {
		t.Fatalf("side effect executed: %v", ran)
	}
}

// A durable turn whose calls all carry recorded results re-enters the
// model phase: no second assistant message, no duplicated results, one
// model call finishes the run.
func TestNativeRecoverSettledBatchStartsModelPhase(t *testing.T) {
	model := &nrrModel{}
	note := &nrrNoteTool{}
	tick, expire := staleClock()
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(tick))
	stack, _ := nrrStack(t, model, &bookTool{}, note, nil, runs, WithRecoveryRuntime("chat", runtime.NewNative()))
	log := stack.stores.SessionLog
	ver := seedNativeHistory(t, log, "s1",
		nlUser("go"),
		assistantCalls(nrrNote("n0"), nrrNote("n1")),
		resultMsg(noteResult("n0"), noteResult("n1")),
	)
	model.reset()
	if _, err := runs.Start(nrrCtx(), stores.Run{SessionID: "s1", RunID: "r1", Flow: "chat", State: stores.Running, Turn: 1, Seq: ver}, stores.LeaseTTL); err != nil {
		t.Fatal(err)
	}
	expire()

	if err := stack.Recover(nrrCtx(), 10); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}
	if ran := note.ran(); len(ran) != 0 {
		t.Fatalf("settled calls re-executed: %v", ran)
	}
	if n := countBlocks(t, log, "s1", func(b types.Block) bool {
		_, ok := b.(types.ToolUse)
		return ok
	}); n != 2 {
		t.Fatalf("tool-use blocks = %d, want the original two", n)
	}
	for _, id := range []string{"n0", "n1"} {
		if n := countBlocks(t, log, "s1", func(b types.Block) bool {
			tr, ok := b.(types.ToolResult)
			return ok && tr.ID == id
		}); n != 1 {
			t.Fatalf("%s results = %d, want 1", id, n)
		}
	}
	if st := runState(t, runs, "r1"); st != stores.Finished {
		t.Fatalf("run state = %v, want Finished", st)
	}
}

// An uncommitted read-only turn may repeat its effects, but the persisted
// turn appears once: one assistant message, one result per call.
func TestNativeRecoverReadOnlyUncommittedTurn(t *testing.T) {
	model := &nrrModel{}
	note := &nrrNoteTool{}
	tick, expire := staleClock()
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(tick))
	stack, _ := nrrStack(t, model, &bookTool{}, note, nil, runs, WithRecoveryRuntime("chat", runtime.NewNative()))
	log := stack.stores.SessionLog
	ver := seedNativeHistory(t, log, "s1", nlUser("go"))
	model.reset(nrrNote("n1"))
	if _, err := runs.Start(nrrCtx(), stores.Run{SessionID: "s1", RunID: "r1", Flow: "chat", State: stores.Running, Turn: 0, Seq: ver}, stores.LeaseTTL); err != nil {
		t.Fatal(err)
	}
	expire()

	if err := stack.Recover(nrrCtx(), 10); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if ran := note.ran(); len(ran) != 1 || ran[0] != "n1" {
		t.Fatalf("executed = %v, want [n1]", ran)
	}
	if n := countBlocks(t, log, "s1", func(b types.Block) bool {
		tr, ok := b.(types.ToolResult)
		return ok && tr.ID == "n1"
	}); n != 1 {
		t.Fatalf("n1 results = %d, want exactly one", n)
	}
	if st := runState(t, runs, "r1"); st != stores.Finished {
		t.Fatalf("run state = %v, want Finished", st)
	}
}

// A recovered run resumes with the turn count its record carries: a drive
// entered at MaxTurns ends at the boundary instead of restarting the
// model budget.
func TestNativeMaxTurnsSurvivesRecovery(t *testing.T) {
	model := &nrrModel{}
	note := &nrrNoteTool{}
	tick, expire := staleClock()
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(tick))
	limits := nativeConvLimits(50)
	limits.MaxTurns = 1
	stack, _ := nrrStack(t, model, &bookTool{}, note, nil, runs,
		WithRecoveryRuntime("chat", runtime.NewNative()), WithLimits("chat", limits))
	log := stack.stores.SessionLog
	ver := seedNativeHistory(t, log, "s1", nlUser("go"))
	if _, err := runs.Start(nrrCtx(), stores.Run{SessionID: "s1", RunID: "r1", Flow: "chat", State: stores.Running, Turn: 1, Seq: ver}, stores.LeaseTTL); err != nil {
		t.Fatal(err)
	}
	expire()

	if err := stack.Recover(nrrCtx(), 10); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if model.calls != 0 {
		t.Fatalf("model calls = %d, want the restored turn to stop the budget", model.calls)
	}
	if st := runState(t, runs, "r1"); st != stores.Finished {
		t.Fatalf("run state = %v, want Finished", st)
	}
}

// With no checkpoint and no stored session owner, recovery fails closed:
// the reaper's principal and credentials never drive, nothing executes,
// and the run closes Failed with an explicit error.
func TestRecoveryAuthorityMissingOwnerFailsClosed(t *testing.T) {
	model := &nrrModel{}
	book := &bookTool{}
	note := &nrrNoteTool{}
	creds := &recordingCreds{}
	tick, expire := staleClock()
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(tick))
	stack, _ := nrrStack(t, model, book, note, nil, runs,
		WithRecoveryRuntime("chat", runtime.NewNative()), WithCredentialSource(creds))
	// No session row: s1 has no owner and no checkpoint survived.
	if _, err := runs.Start(nrrCtx(), stores.Run{SessionID: "s1", RunID: "r1", Flow: "chat", State: stores.Running, Turn: 1, Seq: 0}, stores.LeaseTTL); err != nil {
		t.Fatal(err)
	}
	expire()

	err := stack.Recover(nrrCtx(), 10)
	if err == nil || !errors.Is(err, types.ErrSessionForbidden) {
		t.Fatalf("recover error = %v, want the stored-authority failure", err)
	}
	if got := creds.principals(); len(got) != 0 {
		t.Fatalf("credential source saw %v, want nothing", got)
	}
	if model.calls != 0 || len(note.ran()) != 0 || len(book.ran()) != 0 {
		t.Fatal("recovery executed work without stored authority")
	}
	if st := runState(t, runs, "r1"); st != stores.Failed {
		t.Fatalf("run state = %v, want Failed", st)
	}
}

// A consumed checkpoint re-drives as the checkpoint's originator, not as
// the reaper's principal.
func TestNativeRecoveryPreservesCheckpointOriginator(t *testing.T) {
	f := newRecoverFixture(t, "agent")
	creds := &recordingCreds{}
	st, err := Build(
		WithStores(stores.Stores{SessionLog: f.log, Runs: f.runs, Checkpoints: f.cps, Journal: f.jnl}),
		WithRecoveryRuntime("agent", f.rt),
		WithCredentialSource(creds),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.seedHistory(t, 1)
	_, lease := f.seedRunLease(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "agent", State: stores.Running, Turn: 1, Seq: 1})
	tokCtx := types.WithRunInfo(f.reaperCtx(), types.RunInfo{RunID: "r1"})
	token, err := f.cps.Put(WithPrincipal(tokCtx, types.Principal{Subject: "u1", Tenant: "t1"}), stores.Checkpoint{
		RunID:      "r1",
		SessionID:  "s1",
		Originator: types.Principal{Subject: "u1", Tenant: "t1"},
		Data:       mustJSON(t, runtime.State{Turn: 1, HistoryVersion: 1}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.runs.Suspend(f.reaperCtx(), lease, token); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cps.Consume(f.reaperCtx(), token, stores.ResumeInput{}); err != nil {
		t.Fatal(err)
	}
	f.expire()

	if err := st.Recover(f.reaperCtx(), 10); err != nil {
		t.Fatalf("recover: %v", err)
	}
	want := types.Principal{Tenant: "t1", Subject: "u1"}
	got := creds.principals()
	if len(got) != 1 || got[0].Tenant != want.Tenant || got[0].Subject != want.Subject {
		t.Fatalf("credential source saw %v, want the originator %v", got, want)
	}
	infos := f.rt.infos()
	if len(infos) != 1 || infos[0].Principal.Tenant != want.Tenant || infos[0].Principal.Subject != want.Subject {
		t.Fatalf("run principal = %+v, want the originator %v", infos, want)
	}
}
