package gohan

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// steerRuns is a Runs fake that hands the mailbox to the test in stages,
// so a steer can land between the safe-point drain and Finish: the first
// Drain is empty and the Finish it precedes is refused with
// ErrSignalsPending; the next Drain returns the steer.
type steerRuns struct {
	steer  stores.Signal
	finish int
	stage  int
}

func (s *steerRuns) Start(context.Context, stores.Run, time.Duration) (stores.Lease, error) {
	return stores.Lease{RunID: "run-s"}, nil
}

func (s *steerRuns) Heartbeat(_ context.Context, l stores.Lease) (stores.Lease, error) {
	return l, nil
}

func (s *steerRuns) Suspend(context.Context, stores.Lease, types.ResumeToken) error { return nil }

func (s *steerRuns) Resuming(_ context.Context, _ string, _ time.Duration) (stores.Lease, error) {
	return stores.Lease{RunID: "run-s"}, nil
}

func (s *steerRuns) Finish(_ context.Context, _ stores.Lease, _ stores.RunState, _ []types.CallKey, _ string) error {
	s.finish++
	if s.stage == 1 {
		return fmt.Errorf("gohan: run run-s: %w", types.ErrSignalsPending)
	}
	return nil
}

func (s *steerRuns) ByOperation(context.Context, string, string) (stores.Run, error) {
	return stores.Run{}, stores.ErrRunNotFound
}

func (s *steerRuns) Stale(context.Context, time.Duration, int) ([]stores.Run, error) {
	return nil, nil
}

func (s *steerRuns) Reclaim(_ context.Context, _ stores.Run, _ time.Duration) (stores.Lease, error) {
	return stores.Lease{RunID: "run-s"}, nil
}

func (s *steerRuns) Signal(context.Context, string, stores.Signal) error { return nil }

func (s *steerRuns) Drain(context.Context, stores.Lease) ([]stores.Signal, error) {
	if s.stage == 1 {
		s.stage = 2
		return []stores.Signal{s.steer}, nil
	}
	s.stage = 1
	return nil, nil
}

func (s *steerRuns) Notices(context.Context, int) ([]types.RunNotice, error) { return nil, nil }

func (s *steerRuns) AckNotice(context.Context, string) error { return nil }

// steerRT plays one tool-batch turn and one final turn. The batch step
// blocks on gate until the test releases it, so the test can steer while
// the batch executes.
type steerRT struct {
	mu          sync.Mutex
	log         stores.SessionLog
	sess        string
	gate        chan struct{}
	entered     chan struct{}
	enteredDone bool
	calls       int
}

func (r *steerRT) Name() string                         { return "steer.test" }
func (r *steerRT) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

func (r *steerRT) Start(_ context.Context, _ runtime.AgentRun) (runtime.State, error) {
	r.mu.Lock()
	log, sess := r.log, r.sess
	r.mu.Unlock()
	ver := int64(0)
	if log != nil && sess != "" {
		if h, err := log.Load(context.Background(), sess); err == nil {
			ver = h.Version
		}
	}
	return runtime.State{HistoryVersion: ver}, nil
}

func (r *steerRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	log, sess, gate, entered := r.log, r.sess, r.gate, r.entered
	r.mu.Unlock()
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	if gate != nil {
		r.mu.Lock()
		first := entered != nil
		enteredOnce := r.enteredDone
		r.enteredDone = true
		r.mu.Unlock()
		if first && !enteredOnce && entered != nil {
			close(entered)
		}
		<-gate
	}
	if log != nil && sess != "" {
		r.mu.Lock()
		turn := r.calls
		r.mu.Unlock()
		if h, err := log.Load(ctx, sess); err == nil {
			asst := batchAssistant()
			asst.ID = fmt.Sprintf("assistant-%d", turn)
			results := batchResults()
			results.ID = fmt.Sprintf("tool-results-%d", turn)
			v, aerr := log.Append(ctx, sess, h.Version, asst, results)
			if aerr == nil {
				st.HistoryVersion = v
			}
		}
	}
	st.Turn++
	r.mu.Lock()
	n := r.calls
	r.mu.Unlock()
	if gate != nil && n == 1 {
		return st, nil, runtime.Continue, nil
	}
	return st, nil, runtime.DoneStatus, nil
}

func batchAssistant() types.Message {
	return types.Message{
		ID:   "assistant-1",
		Role: types.RoleAssistant,
		Blocks: []types.Block{
			types.ToolUse{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}}, ID: "c1", Name: "echo"},
			types.ToolUse{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}}, ID: "c2", Name: "echo"},
		},
	}
}

func batchResults() types.Message {
	results := types.Message{ID: "tool-results-1", Role: types.RoleUser}
	for _, id := range []string{"c1", "c2"} {
		results.Blocks = append(results.Blocks, types.ToolResult{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool}},
			ID:        id,
		})
	}
	return results
}

func steerMsg(text, id string) types.Message {
	m := userMsg(text)
	m.ID = id
	return m
}

func steerLog(t *testing.T) stores.SessionLog {
	t.Helper()
	return stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
}

func steerAppend(log stores.SessionLog, sess string) HistoryAppender {
	return AppendFunc(func(ctx context.Context, expected int64, msgs ...types.Message) (int64, error) {
		return log.Append(ctx, sess, expected, msgs...)
	})
}

func steerNewRuns() *runsFixture {
	return &runsFixture{
		MemoryRuns: stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		),
		onSess: map[string]string{},
	}
}

// historySig describes each message as role plus the first text block, so
// the assertions do not depend on the ids the memory log assigns.
func historySig(h stores.History) []string {
	out := make([]string, 0, len(h.Messages))
	for _, m := range h.Messages {
		text := ""
		for _, b := range m.Blocks {
			if tb, ok := b.(types.Text); ok {
				text = tb.Text
				break
			}
		}
		out = append(out, string(m.Role)+":"+text)
	}
	return out
}

func TestConversationSteer(t *testing.T) {
	ctx := principalCtx(context.Background())
	sid := "sess-steer"

	t.Run("flow.steer-applied-at-boundary", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			log := steerLog(t)
			if _, err := log.Append(ctx, sid, 0, steerMsg("hi", "m0")); err != nil {
				t.Fatalf("seed history: %v", err)
			}
			runs := steerNewRuns()
			events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
			stack, err := Build()
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			stack.stores = stores.Stores{SessionLog: log}
			conv, err := NewConversation(stack, "chat", &convRT{}, WithConversationRuns(runs), WithConversationEventLog(events))
			if err != nil {
				t.Fatalf("new conversation: %v", err)
			}
			lease, err := runs.Start(ctx, stores.Run{SessionID: sid, RunID: "run-1"}, stores.LeaseTTL)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			rt := &steerRT{log: log, sess: sid, gate: make(chan struct{}), entered: make(chan struct{})}
			lc := NewLifecycle(
				WithLifecycleRuns(runs, lease),
				WithLifecycleSession(sid),
				WithLifecycleAppender(steerAppend(log, sid)),
				WithLifecycleMaxTurns(6),
			)

			driven := make(chan error, 1)
			go func() {
				st, err := rt.Start(ctx, runtime.AgentRun{})
				if err != nil {
					driven <- err
					return
				}
				// Turn 1: the batch executes. The drain at the safe point
				// after the batch applies the steer, and the next model
				// call sees it.
				st, _, _, err = rt.Step(ctx, st)
				if err != nil {
					driven <- err
					return
				}
				terminal, again, evs, err := lc.drainSafePoint(ctx, st)
				if err != nil {
					driven <- err
					return
				}
				if terminal || !again {
					driven <- fmt.Errorf("safe point: terminal=%v again=%v evs=%v", terminal, again, evs)
					return
				}
				// Turn 2: the steer is in history before this call.
				st, _, status, err := rt.Step(ctx, st)
				if err != nil {
					driven <- err
					return
				}
				if status != runtime.DoneStatus {
					driven <- fmt.Errorf("status after steer turn = %v, want done", status)
					return
				}
				_, evs2, _, err := lc.finishTurn(ctx, st)
				if err != nil {
					driven <- err
					return
				}
				for _, e := range evs2 {
					if d, ok := e.(types.Done); ok && d.Reason != types.StopCompleted {
						driven <- fmt.Errorf("done reason = %v, want completed", d.Reason)
						return
					}
				}
				driven <- nil
			}()

			<-rt.entered
			if err := conv.Steer(ctx, sid, steerMsg("use the cheaper carrier", "steer-1")); err != nil {
				t.Fatalf("steer: %v", err)
			}
			close(rt.gate)
			if err := <-driven; err != nil {
				t.Fatalf("drive: %v", err)
			}
			h, err := log.Load(ctx, sid)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			got := historySig(h)
			want := []string{
				"user:hi", "assistant:", "user:", "user:use the cheaper carrier",
				"assistant:", "user:",
			}
			if len(got) != len(want) {
				t.Fatalf("history ids = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("history ids = %v, want %v", got, want)
				}
			}
		})
	})

	t.Run("flow.steer-preserves-adjacency", func(t *testing.T) {
		log := steerLog(t)
		if _, err := log.Append(ctx, sid, 0, steerMsg("hi", "m0")); err != nil {
			t.Fatalf("seed history: %v", err)
		}
		runs := steerNewRuns()
		lease, err := runs.Start(ctx, stores.Run{SessionID: sid, RunID: "run-2"}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if err := runs.Signal(ctx, "run-2", stores.Signal{Kind: stores.SignalSteer, Message: steerMsg("slow down", "steer-2")}); err != nil {
			t.Fatalf("signal steer: %v", err)
		}
		lc := NewLifecycle(
			WithLifecycleRuns(runs, lease),
			WithLifecycleSession(sid),
			WithLifecycleAppender(steerAppend(log, sid)),
			WithLifecycleMaxTurns(6),
		)
		rt := &steerRT{log: log, sess: sid}
		st, err := rt.Start(ctx, runtime.AgentRun{})
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		st, _, status, err := rt.Step(ctx, st)
		if err != nil {
			t.Fatalf("batch step: %v", err)
		}
		if status != runtime.DoneStatus {
			t.Fatalf("status = %v, want done", status)
		}
		terminal, again, evs, err := lc.drainSafePoint(ctx, st)
		if err != nil {
			t.Fatalf("drain: %v", err)
		}
		if terminal || !again {
			t.Fatalf("terminal=%v again=%v, want one more turn", terminal, again)
		}
		applied := false
		for _, e := range evs {
			if sa, ok := e.(types.SteerApplied); ok {
				applied = true
				if sa.MessageID != "steer-2" {
					t.Fatalf("SteerApplied.MessageID = %q, want steer-2", sa.MessageID)
				}
			}
		}
		if !applied {
			t.Fatalf("no SteerApplied in %v", evs)
		}
		h, err := log.Load(ctx, sid)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		msgs := h.Messages
		if len(msgs) != 4 {
			t.Fatalf("history = %v, want seed, batch, steer", historySig(h))
		}
		if msgs[1].Role != types.RoleAssistant {
			t.Fatalf("message 2 role = %v, want assistant", msgs[1].Role)
		}
		uses := 0
		for _, b := range msgs[1].Blocks {
			if _, ok := b.(types.ToolUse); ok {
				uses++
			}
		}
		if uses != 2 {
			t.Fatalf("assistant message carries %d ToolUse blocks, want 2", uses)
		}
		if msgs[3].Role != types.RoleUser {
			t.Fatalf("message 4 role = %v, want user steer after both results", msgs[3].Role)
		}
		if sig := historySig(h); sig[3] != "user:slow down" {
			t.Fatalf("message 4 = %q, want the steer after both results", sig[3])
		}
	})

	t.Run("flow.steer-after-final-reply-runs-turn", func(t *testing.T) {
		log := steerLog(t)
		if _, err := log.Append(ctx, sid, 0, steerMsg("hi", "m0")); err != nil {
			t.Fatalf("seed history: %v", err)
		}
		// The steer lands between the safe-point drain and Finish: the
		// first drain is empty, Finish refuses with ErrSignalsPending and
		// the refusal drain applies the steer and reports one more turn.
		runs := &steerRuns{steer: stores.Signal{Kind: stores.SignalSteer, Message: steerMsg("one more", "steer-3")}}
		lease, err := runs.Start(ctx, stores.Run{SessionID: sid, RunID: "run-s"}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		lc := NewLifecycle(
			WithLifecycleRuns(runs, lease),
			WithLifecycleSession(sid),
			WithLifecycleAppender(steerAppend(log, sid)),
			WithLifecycleMaxTurns(3),
		)
		rt := &lcRuntime{script: []lcStep{
			{status: runtime.DoneStatus},
			{status: runtime.DoneStatus},
		}}
		st, err := rt.Start(ctx, runtime.AgentRun{})
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		st, _, status, err := rt.Step(ctx, st)
		if err != nil {
			t.Fatalf("final step: %v", err)
		}
		if status != runtime.DoneStatus {
			t.Fatalf("status = %v, want done", status)
		}
		if _, _, again, err := lc.finishTurn(ctx, st); err != nil || !again {
			t.Fatalf("finishTurn again=%v err=%v, want one more turn", again, err)
		}
		if runs.finish != 1 {
			t.Fatalf("Finish ran %d times, want 1 refusal", runs.finish)
		}
		if st.Turn != 1 {
			t.Fatalf("turn = %d, want 1", st.Turn)
		}
		h, err := log.Load(ctx, sid)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got := historySig(h); len(got) != 2 || got[1] != "user:one more" {
			t.Fatalf("history = %v, want seed then steer-3", got)
		}

		// The extra turn counts against MaxTurns: with the bound already
		// reached, a drained steer ends the run instead of another turn.
		limited := NewLifecycle(
			WithLifecycleRuns(runs, lease),
			WithLifecycleSession(sid),
			WithLifecycleAppender(steerAppend(log, sid)),
			WithLifecycleMaxTurns(1),
		)
		st2, _, _, err := rt.Step(ctx, st)
		if err != nil {
			t.Fatalf("step: %v", err)
		}
		runs.stage = 1 // the next drain carries the steer again
		terminal, again, _, err := limited.drainSafePoint(ctx, st2)
		if err != nil {
			t.Fatalf("drain: %v", err)
		}
		if !terminal || again {
			t.Fatalf("terminal=%v again=%v, want the run to end at MaxTurns", terminal, again)
		}
	})

	t.Run("flow.steer-no-active-run", func(t *testing.T) {
		log := steerLog(t)
		rt := &convRT{}
		f := convSetup(t, rt, log)
		if err := f.conv.Steer(ctx, sid, steerMsg("late", "steer-4")); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("steer without run err = %v, want ErrRunNotActive", err)
		}
		// A suspended run is not Running.
		if _, err := log.Append(ctx, sid, 0, steerMsg("hi", "m0")); err != nil {
			t.Fatalf("seed history: %v", err)
		}
		lease, err := f.runs.Start(ctx, stores.Run{SessionID: sid, RunID: "run-9"}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if err := f.runs.Suspend(ctx, lease, types.ResumeToken("run-9")); err != nil {
			t.Fatalf("suspend: %v", err)
		}
		if err := f.conv.Steer(ctx, sid, steerMsg("late", "steer-4")); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("steer at suspended run err = %v, want ErrRunNotActive", err)
		}
		h, err := log.Load(ctx, sid)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(h.Messages) != 1 {
			t.Fatalf("history grew to %d messages, want 1", len(h.Messages))
		}
	})

	t.Run("mailbox full at MaxPendingSignals", func(t *testing.T) {
		log := steerLog(t)
		rt := &convRT{}
		f := convSetup(t, rt, log)
		if _, err := f.runs.Start(ctx, stores.Run{SessionID: sid, RunID: "run-cap"}, stores.LeaseTTL); err != nil {
			t.Fatalf("start: %v", err)
		}
		f.runs.mu.Lock()
		f.runs.onSess[sid] = "run-cap"
		f.runs.mu.Unlock()
		for i := range stores.MaxPendingSignals {
			if err := f.conv.Steer(ctx, sid, steerMsg(fmt.Sprintf("s-%d", i), fmt.Sprintf("steer-%d", i))); err != nil {
				t.Fatalf("steer %d err = %v", i, err)
			}
		}
		err := f.conv.Steer(ctx, sid, steerMsg("one too many", "steer-x"))
		if !errors.Is(err, types.ErrMailboxFull) {
			t.Fatalf("eleventh steer err = %v, want ErrMailboxFull", err)
		}
	})
}
