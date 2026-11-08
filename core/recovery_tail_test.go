package gohan

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// errFinishInject is the deterministic terminal-write failure the one-shot
// decorator parks on the run store's Finish.
var errFinishInject = errors.New("terminal write failed")

// oneShotFinishFailRuns fails the first terminal transition and delegates
// every later call, after the oneShotFenceRuns shape.
type oneShotFinishFailRuns struct {
	*stores.MemoryRuns
	used  atomic.Bool
	runID string
}

func (r *oneShotFinishFailRuns) Start(ctx context.Context, run stores.Run, ttl time.Duration) (stores.Lease, error) {
	l, err := r.MemoryRuns.Start(ctx, run, ttl)
	if err == nil {
		r.runID = l.RunID
	}
	return l, err
}

func (r *oneShotFinishFailRuns) FinishWithReason(ctx context.Context, l stores.Lease, st stores.RunState, uncertain []types.CallKey, ref string, reason types.StopReason) error {
	if !r.used.Swap(true) {
		return errFinishInject
	}
	return r.MemoryRuns.FinishWithReason(ctx, l, st, uncertain, ref, reason)
}

// limitStopRT scripts a step that carries a limit stop as a Done returned
// ahead of the terminal handling.
type limitStopRT struct {
	mu    sync.Mutex
	calls int
}

func (r *limitStopRT) Name() string                         { return "limit.stop" }
func (r *limitStopRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *limitStopRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *limitStopRT) Step(_ context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls == 1 {
		return st, nil, runtime.Continue, nil
	}
	return st, []types.Event{types.Done{Reason: types.StopLimit}}, runtime.DoneStatus, nil
}

func TestRuntimeFinishError(t *testing.T) {
	t.Run("runtime.finish-error-no-done", func(t *testing.T) {
		stack, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		stack.stores = stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}
		runs := &oneShotFinishFailRuns{MemoryRuns: stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now))}
		evlog := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
		conv, err := NewConversation(stack, "chat", &limitStopRT{},
			WithConversationRuns(runs),
			WithConversationEventLog(evlog),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}

		var sawDone bool
		var lastErr error
		for ev, err := range conv.Send(principalCtx(context.Background()), "s1", userMsg("hi")) {
			if _, ok := ev.(types.Done); ok {
				sawDone = true
			}
			if err != nil {
				lastErr = err
			}
		}
		if sawDone {
			t.Fatal("Done delivered past a failed terminal write")
		}
		if !errors.Is(lastErr, errFinishInject) {
			t.Fatalf("stream err = %v, want the failed terminal write", lastErr)
		}

		var records int
		var last types.Event
		for e, err := range evlog.Read(context.Background(), runs.runID, 0) {
			if err != nil {
				t.Fatalf("read event log: %v", err)
			}
			records++
			last = e.Payload
		}
		if records != 1 {
			t.Fatalf("event log records = %d, want the single terminal failure", records)
		}
		if _, ok := last.(types.TerminalError); !ok {
			t.Fatalf("last record %T, want TerminalError", last)
		}
	})
}

// tailRT re-drives a recovered run like the real step: pending calls go
// through the journal, one ordinary event streams, one model call finishes
// the run. block parks the drive mid-step for the cancellation case.
type tailRT struct {
	journal stores.Journal
	log     stores.SessionLog
	session string
	block   chan struct{}
	entered chan struct{}
}

func (r *tailRT) Name() string                         { return "tail.test" }
func (r *tailRT) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

func (r *tailRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *tailRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if r.block != nil {
		if r.entered != nil {
			select {
			case <-r.entered:
			default:
				close(r.entered)
			}
		}
		<-r.block
		return st, nil, runtime.Continue, nil
	}
	if len(st.Pending) > 0 {
		msg := types.Message{Role: types.RoleAssistant}
		var first types.ToolResult
		for i, call := range st.Pending {
			key := types.CallKey{SessionID: r.session, CallID: call.ID}
			entry, _, err := r.journal.Reserve(ctx, key, call.Name, stores.Fingerprint("fp-"+call.ID), nil)
			if err != nil {
				return st, nil, 0, err
			}
			if entry.State != stores.Completed {
				entry.Result = types.ToolResult{ID: call.ID, Outcome: types.Succeeded}
				if err := r.journal.Complete(ctx, key, entry.Result); err != nil {
					return st, nil, 0, err
				}
			}
			if i == 0 {
				first = entry.Result
			}
			msg.Blocks = append(msg.Blocks, entry.Result)
		}
		ver, err := r.log.Append(ctx, r.session, st.HistoryVersion, msg)
		if err != nil {
			return st, nil, 0, err
		}
		return runtime.State{Turn: st.Turn + 1, HistoryVersion: ver}, []types.Event{types.ToolFinished{Turn: st.Turn, Result: first}}, runtime.Continue, nil
	}
	return st, nil, runtime.DoneStatus, nil
}

func TestRecoveryEventTail(t *testing.T) {
	t.Run("recovery.eventlog-tail-durable", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		ctx := f.reaperCtx()
		evlog := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
		f.stack.stores.EventLog = evlog
		rt := &tailRT{journal: f.jnl, log: f.log, session: "s1"}
		f.stack.recovery["agent"] = rt

		f.completeInJournal(t, "c1")
		f.seedHistory(t, 1)
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "agent", State: stores.Running, Turn: 2, Seq: 1, Pending: pendingCalls("c1")})
		f.expire()

		if err := f.stack.Recover(ctx, 10); err != nil {
			t.Fatalf("recover: %v", err)
		}
		if st := runState(t, f.runs, "r1"); st != stores.Finished {
			t.Fatalf("run state = %v, want Finished", st)
		}

		var payloads []types.Event
		var dones int
		for e, err := range evlog.Read(ctx, "r1", 0) {
			if err != nil {
				t.Fatalf("read event log: %v", err)
			}
			payloads = append(payloads, e.Payload)
			if _, ok := e.Payload.(types.Done); ok {
				dones++
			}
		}
		if len(payloads) != 2 {
			t.Fatalf("recovered tail = %d records, want the ordinary event and the boundary", len(payloads))
		}
		if _, ok := payloads[0].(types.ToolFinished); !ok {
			t.Fatalf("first record %T, want the ordinary ToolFinished", payloads[0])
		}
		if dones != 1 {
			t.Fatalf("Done records = %d, want exactly the recovered boundary", dones)
		}

		conv, err := NewConversation(f.stack, "agent", f.rt,
			WithConversationRuns(f.runs),
			WithConversationCheckpoints(f.cps),
			WithConversationEventLog(evlog),
		)
		if err != nil {
			t.Fatalf("second conversation: %v", err)
		}
		attach := func(afterSeq int64) []types.Event {
			t.Helper()
			var evs []types.Event
			actx, cancel := context.WithCancel(context.Background())
			defer cancel()
			for ev, err := range conv.Attach(actx, "r1", afterSeq) {
				if err != nil {
					t.Fatalf("attach from %d: %v", afterSeq, err)
				}
				evs = append(evs, ev)
			}
			return evs
		}
		full := attach(0)
		if len(full) != 2 {
			t.Fatalf("attached tail = %d events, want two", len(full))
		}
		if _, ok := full[len(full)-1].(types.Done); !ok {
			t.Fatalf("last attached event %T, want the boundary", full[len(full)-1])
		}
		cursor := attach(1)
		if len(cursor) != 1 {
			t.Fatalf("attach past the prior cursor = %d events, want the boundary only", len(cursor))
		}
		if _, ok := cursor[0].(types.Done); !ok {
			t.Fatalf("event %T, want Done", cursor[0])
		}

		// A cancelled reaper still leaves the tail's terminal boundary:
		// the completion writes run bounded past the cancellation.
		rt2 := &tailRT{journal: f.jnl, log: f.log, session: "s1", block: make(chan struct{}), entered: make(chan struct{})}
		f.stack.recovery["agent"] = rt2
		f.completeInJournal(t, "c2")
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "r2", Flow: "agent", State: stores.Running, Turn: 2, Seq: 1, Pending: pendingCalls("c2")})
		f.expire()
		rctx, rcancel := context.WithCancel(ctx)
		defer rcancel()
		recDone := make(chan error, 1)
		go func() { recDone <- f.stack.Recover(rctx, 10) }()
		<-rt2.entered
		rcancel()
		close(rt2.block)
		if err := <-recDone; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled recovery: %v", err)
		}
		var r2Records int
		for e, err := range evlog.Read(ctx, "r2", 0) {
			if err != nil {
				t.Fatalf("read event log: %v", err)
			}
			r2Records++
			if _, ok := e.Payload.(types.TerminalError); !ok {
				t.Fatalf("record %T, want the terminal failure", e.Payload)
			}
		}
		if r2Records != 1 {
			t.Fatalf("r2 records = %d, want the terminal failure only", r2Records)
		}
	})
}

func TestRuntimeResumeToolBudget(t *testing.T) {
	t.Run("runtime.resume-tool-budget-cumulative", func(t *testing.T) {
		// The approval Resume path: two fresh batches before the ask, the
		// prepaid ask on resume, the next fresh batch refused against the
		// cumulative admissions.
		model := &batchSeqModel{steps: [][]types.ToolUse{
			{nrrNote("n1"), nrrNote("n2")},
			{nrrBook("b1")},
			{nrrNote("n4")},
		}}
		book := &bookTool{}
		note := &nrrNoteTool{}
		stack, conv := nrrStack(t, model, book, note, nil, nil,
			WithLimits("chat", nativeConvLimits(3)))

		token := suspendToken(t, conv)
		if ran := note.ran(); len(ran) != 2 {
			t.Fatalf("note executions after Send = %v, want [n1 n2]", ran)
		}

		_, resumeErr := driveApproval(t, stack, token)
		if !errors.Is(resumeErr, types.ErrBatchOverrun) {
			t.Fatalf("resume err = %v, want the MaxToolCalls refusal", resumeErr)
		}
		if ran := book.ran(); len(ran) != 1 || ran[0] != "b1" {
			t.Fatalf("book executions = %v, want the prepaid b1 only", ran)
		}
		if ran := note.ran(); len(ran) != 2 {
			t.Fatalf("note executions = %v, want no admission past the limit", ran)
		}

		// The crash recovery path: the approval is consumed, the client
		// dies before its resume lease, recovery completes the decision
		// and refuses the next fresh batch against the same cumulative
		// total.
		model2 := &batchSeqModel{steps: [][]types.ToolUse{
			{nrrNote("n5"), nrrNote("n6")},
			{nrrBook("b2")},
			{nrrNote("n7")},
		}}
		book2 := &bookTool{}
		note2 := &nrrNoteTool{}
		memRuns := stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		)
		runs := memRuns
		stack2, err := Build(
			WithStores(stores.Stores{
				SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
				Runs:       runs,
				Checkpoints: stores.NewMemoryCheckpoints(
					stores.WithMemoryCheckpointClock(time.Now),
					stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom),
					stores.WithMemoryCheckpointRuns(runs),
				),
			}),
			WithModels(model2),
			WithNativeAgent(NativeSpec{
				Request: FlowRequest{Name: "chat"},
				Profile: "native",
				Tools:   []types.Tool{book2, note2},
				Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
				},
			}),
			WithLimits("chat", nativeConvLimits(3)),
			WithRecoveryRuntime("chat", runtime.NewNative()),
		)
		if err != nil {
			t.Fatalf("build recovery stack: %v", err)
		}
		conv2, err := NewNativeConversation(stack2, "chat",
			WithConversationRuns(runs),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
			WithConversationApprovalPolicy(nrrAllowPolicy{}),
		)
		if err != nil {
			t.Fatalf("recovery conversation: %v", err)
		}
		token2 := suspendToken(t, conv2)
		if ran := note2.ran(); len(ran) != 2 {
			t.Fatalf("note executions after Send = %v, want [n5 n6]", ran)
		}
		decided := stores.ResumeInput{Verdict: stores.VerdictApprove, Approver: &nrrOwner}
		consumed, err := stack2.stores.Checkpoints.Consume(nrrCtx(), token2, decided)
		if err != nil {
			t.Fatalf("consume approval: %v", err)
		}
		recoverErr := stack2.Recover(nrrCtx(), 10)
		if recoverErr != nil {
			var recOver *types.LimitExceededError
			if !errors.As(recoverErr, &recOver) && !errors.Is(recoverErr, types.ErrBatchOverrun) {
				t.Fatalf("recover: %v", recoverErr)
			}
		}
		if st := runState(t, runs, consumed.RunID); st != stores.Failed {
			t.Fatalf("recovered run state = %v, want Failed on the limit refusal", st)
		}
		if ran := book2.ran(); len(ran) != 1 || ran[0] != "b2" {
			t.Fatalf("book executions = %v, want the prepaid b2 only", ran)
		}
		if ran := note2.ran(); len(ran) != 2 {
			t.Fatalf("note executions = %v, want no admission past the limit", ran)
		}
	})
}
