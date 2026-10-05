package gohan

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// convRT is a scripted runtime: startErr fails at Start, gate blocks each
// Step until the test releases it, entered closes once the run reaches its
// first Step, and steps Continue reports precede DoneStatus. Every block
// waits on a channel so synctest keeps working.
type convRT struct {
	mu       sync.Mutex
	once     sync.Once
	startErr error
	gate     chan struct{}
	entered  chan struct{}
	steps    int
	calls    int
}

func (r *convRT) Name() string                         { return "conv.test" }
func (r *convRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *convRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return runtime.State{}, r.startErr
}

func (r *convRT) Step(_ context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if r.entered != nil {
		r.once.Do(func() { close(r.entered) })
	}
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls <= r.steps {
		return st, nil, runtime.Continue, nil
	}
	return st, nil, runtime.DoneStatus, nil
}

// runsFixture embeds the memory runs store, counts Starts and answers the
// SessionRunFinder port Cancel uses for a run another pod started.
type runsFixture struct {
	*stores.MemoryRuns
	mu     sync.Mutex
	onSess map[string]string
	starts int
}

func (f *runsFixture) Start(ctx context.Context, r stores.Run, ttl time.Duration) (stores.Lease, error) {
	l, err := f.MemoryRuns.Start(ctx, r, ttl)
	if err == nil {
		f.mu.Lock()
		f.starts++
		f.onSess[r.SessionID] = r.RunID
		f.mu.Unlock()
	}
	return l, err
}

func (f *runsFixture) RunForSession(ctx context.Context, sessionID string) (stores.Run, error) {
	f.mu.Lock()
	runID, ok := f.onSess[sessionID]
	f.mu.Unlock()
	if !ok || !f.SessionLeaseActive(ctx, sessionID) {
		return stores.Run{}, stores.ErrRunNotFound
	}
	return stores.Run{SessionID: sessionID, RunID: runID, State: stores.Running}, nil
}

// controlledLog serves every session with a fixed control state.
type controlledLog struct {
	stores.SessionLog
	control stores.SessionControl
}

func (l controlledLog) Control(context.Context, string) (stores.SessionControl, error) {
	return l.control, nil
}

type convFixture struct {
	conv   Conversation
	rt     *convRT
	runs   *runsFixture
	events *stores.MemoryEventLog
	log    stores.SessionLog
}

func convSetup(t *testing.T, rt *convRT, log stores.SessionLog) *convFixture {
	t.Helper()
	stack, err := Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	stack.stores = stores.Stores{SessionLog: log}
	runs := &runsFixture{
		MemoryRuns: stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		),
		onSess: map[string]string{},
	}
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	conv, err := NewConversation(stack, "chat", rt,
		WithConversationRuns(runs),
		WithConversationEventLog(events),
	)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	return &convFixture{conv: conv, rt: rt, runs: runs, events: events, log: log}
}

func principalCtx(ctx context.Context) context.Context {
	return WithPrincipal(ctx, types.Principal{Tenant: "t-a", Subject: "u1"})
}

func userMsg(text string) types.Message {
	return types.Message{
		Role: types.RoleUser,
		Blocks: []types.Block{types.Text{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}},
			Text:      text,
		}},
	}
}

type streamResult struct {
	evs []types.Event
	err error
}

func collectStream(seq iter.Seq2[types.Event, error]) streamResult {
	var res streamResult
	for e, err := range seq {
		if err != nil {
			res.err = err
			return res
		}
		res.evs = append(res.evs, e)
	}
	return res
}

// failingEventLog fails the failAt-th Append.
type failingEventLog struct {
	stores.EventLog
	failAt int
	n      int
}

var errRecordFailed = errors.New("event log unavailable")

func (l *failingEventLog) Append(ctx context.Context, runID string, e stores.Event) error {
	l.n++
	if l.n == l.failAt {
		return errRecordFailed
	}
	return l.EventLog.Append(ctx, runID, e)
}

// finishCountRuns counts the Finish calls the conversation makes.
type finishCountRuns struct {
	*runsFixture
	finishes int
}

func (f *finishCountRuns) Finish(ctx context.Context, l stores.Lease, st stores.RunState, uncertain []types.CallKey, ref string) error {
	f.finishes++
	return f.MemoryRuns.Finish(ctx, l, st, uncertain, ref)
}

func convWithLog(t *testing.T, rt *convRT, runs stores.Runs, log stores.EventLog) Conversation {
	t.Helper()
	stack, err := Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	stack.stores = stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}
	conv, err := NewConversation(stack, "chat", rt,
		WithConversationRuns(runs),
		WithConversationEventLog(log),
	)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	return conv
}

func TestConversationStreamEventOrdering(t *testing.T) {
	ctx := principalCtx(context.Background())
	sid := "sess-1"

	t.Run("streams.error-tuple-terminal", func(t *testing.T) {
		log := &failingEventLog{EventLog: stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now)), failAt: 1}
		runs := &runsFixture{MemoryRuns: stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now)), onSess: map[string]string{}}
		conv := convWithLog(t, &convRT{steps: 0}, runs, log)
		var tuples int
		for ev, err := range conv.Send(ctx, sid, userMsg("hi")) {
			tuples++
			if tuples > 1 {
				t.Fatalf("tuple %d after the terminal error tuple: ev=%v err=%v", tuples, ev, err)
			}
			if err == nil {
				t.Fatalf("delivered %+v after the append failure; want only the error tuple", ev)
			}
			if !errors.Is(err, errRecordFailed) {
				t.Fatalf("err = %v, want the record failure", err)
			}
		}
		if tuples != 1 {
			t.Fatalf("tuples = %d, want exactly the one error tuple", tuples)
		}
	})

	t.Run("guard blocked append failure ends the stream", func(t *testing.T) {
		log := &failingEventLog{EventLog: stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now)), failAt: 1}
		runs := &runsFixture{MemoryRuns: stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now)), onSess: map[string]string{}}
		rt := &convRT{startErr: &types.GuardBlockedError{Stage: types.StageOutput, Reason: "blocked"}}
		conv := convWithLog(t, rt, runs, log)
		var tuples int
		for ev, err := range conv.Send(ctx, sid, userMsg("hi")) {
			tuples++
			if tuples > 1 {
				t.Fatalf("tuple %d after the terminal error tuple: ev=%v err=%v", tuples, ev, err)
			}
			if ev != nil {
				t.Fatalf("delivered %+v after the append failure; want only the error tuple", ev)
			}
			if !errors.Is(err, errRecordFailed) {
				t.Fatalf("err = %v, want the record failure", err)
			}
		}
		if tuples != 1 {
			t.Fatalf("tuples = %d, want exactly the one error tuple", tuples)
		}
	})

	t.Run("lifecycle owns the terminal transition", func(t *testing.T) {
		inner := &runsFixture{MemoryRuns: stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now)), onSess: map[string]string{}}
		runs := &finishCountRuns{runsFixture: inner}
		log := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
		rt := &convRT{startErr: errors.New("model down")}
		conv := convWithLog(t, rt, runs, log)
		var got error
		for _, err := range conv.Send(ctx, sid, userMsg("hi")) {
			if err != nil {
				got = err
			}
		}
		if got == nil || !strings.Contains(got.Error(), "model down") {
			t.Fatalf("err = %v, want the run failure", got)
		}
		if runs.finishes != 1 {
			t.Fatalf("Finish called %d times, want exactly the lifecycle's one", runs.finishes)
		}
	})
}

func TestConversationSend(t *testing.T) {
	ctx := principalCtx(context.Background())
	sid := "sess-1"

	t.Run("flow.cancel-other-request", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rt := &convRT{gate: make(chan struct{}), entered: make(chan struct{})}
			f := convSetup(t, rt, stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
			streamDone := make(chan streamResult, 1)
			go func() {
				res := collectStream(f.conv.Send(ctx, sid, userMsg("hi")))
				streamDone <- res
			}()
			<-rt.entered
			cancelDone := make(chan error, 1)
			go func() { cancelDone <- f.conv.Cancel(ctx, sid) }()
			synctest.Wait()
			close(rt.gate)
			if err := <-cancelDone; err != nil {
				t.Fatalf("cancel: %v", err)
			}
			res := <-streamDone
			if res.err != nil {
				t.Fatalf("send: %v", res.err)
			}
			done, ok := res.evs[len(res.evs)-1].(types.Done)
			if !ok || done.Reason != types.StopCancelled {
				t.Fatalf("last event %T %v, want Done{cancelled}", res.evs[len(res.evs)-1], res.evs[len(res.evs)-1])
			}
			if f.runs.SessionLeaseActive(ctx, sid) {
				t.Fatal("run still holds the lease after cancel")
			}
		})
	})

	t.Run("flow.send-during-active-run", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rt := &convRT{gate: make(chan struct{}), entered: make(chan struct{})}
			f := convSetup(t, rt, stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
			streamDone := make(chan streamResult, 1)
			go func() { streamDone <- collectStream(f.conv.Send(ctx, sid, userMsg("first"))) }()
			<-rt.entered
			res := collectStream(f.conv.Send(ctx, sid, userMsg("second")))
			if !errors.Is(res.err, types.ErrRunActive) {
				t.Fatalf("got %v, want ErrRunActive", res.err)
			}
			hist, lerr := f.log.Load(ctx, sid)
			if lerr != nil {
				t.Fatalf("load: %v", lerr)
			}
			if len(hist.Messages) != 1 {
				t.Fatalf("got %d messages, want 1 (refusal before append)", len(hist.Messages))
			}
			close(rt.gate)
			if res := <-streamDone; res.err != nil {
				t.Fatalf("first send: %v", res.err)
			}
		})
	})

	t.Run("refused-second-send-while-run-live", func(t *testing.T) {
		rt := &convRT{gate: make(chan struct{}), entered: make(chan struct{})}
		f := convSetup(t, rt, stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
		streamDone := make(chan streamResult, 1)
		go func() { streamDone <- collectStream(f.conv.Send(ctx, sid, userMsg("first"))) }()
		<-rt.entered
		res := collectStream(f.conv.Send(ctx, sid, userMsg("second")))
		if !errors.Is(res.err, types.ErrRunActive) {
			t.Fatalf("got %v, want ErrRunActive", res.err)
		}
		close(rt.gate)
		first := <-streamDone
		if first.err != nil {
			t.Fatalf("first send: %v", first.err)
		}
		hist, err := f.log.Load(ctx, sid)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(hist.Messages) != 1 {
			t.Fatalf("got %d messages, want 1", len(hist.Messages))
		}
	})

	t.Run("flow.idempotent-send", func(t *testing.T) {
		rt := &convRT{}
		f := convSetup(t, rt, stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
		keyed := WithIdempotencyKey(ctx, "op-1")
		first := collectStream(f.conv.Send(keyed, sid, userMsg("hi")))
		if first.err != nil {
			t.Fatalf("first send: %v", first.err)
		}
		second := collectStream(f.conv.Send(keyed, sid, userMsg("hi")))
		if second.err != nil {
			t.Fatalf("reattached send: %v", second.err)
		}
		if len(second.evs) != len(first.evs) {
			t.Fatalf("got %d events on reattach, want %d", len(second.evs), len(first.evs))
		}
		hist, err := f.log.Load(ctx, sid)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(hist.Messages) != 1 {
			t.Fatalf("got %d messages, want 1 (no second append)", len(hist.Messages))
		}
		f.runs.mu.Lock()
		starts := f.runs.starts
		f.runs.mu.Unlock()
		if starts != 1 {
			t.Fatalf("got %d runs started, want 1", starts)
		}
	})

	t.Run("flow.send-during-human-control-no-run", func(t *testing.T) {
		rt := &convRT{}
		log := controlledLog{
			SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			control:    stores.ControlHuman,
		}
		f := convSetup(t, rt, log)
		res := collectStream(f.conv.Send(ctx, sid, userMsg("hi")))
		if res.err != nil {
			t.Fatalf("send: %v", res.err)
		}
		if len(res.evs) != 1 {
			t.Fatalf("got %d events, want 1", len(res.evs))
		}
		done, ok := res.evs[0].(types.Done)
		if !ok || done.Reason != types.StopHandedOff {
			t.Fatalf("event %T %v, want Done{handed_off}", res.evs[0], res.evs[0])
		}
		hist, err := f.log.Load(ctx, sid)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(hist.Messages) != 1 {
			t.Fatalf("got %d messages, want 1 (message appended)", len(hist.Messages))
		}
		f.runs.mu.Lock()
		starts := f.runs.starts
		f.runs.mu.Unlock()
		if starts != 0 {
			t.Fatalf("got %d runs started, want 0 under human control", starts)
		}
		if _, ferr := f.runs.RunForSession(ctx, sid); !errors.Is(ferr, stores.ErrRunNotFound) {
			t.Fatalf("run lookup got %v, want ErrRunNotFound", ferr)
		}
	})

	t.Run("guard-blocked-emission", func(t *testing.T) {
		rt := &convRT{startErr: &types.GuardBlockedError{Stage: types.StageInput, Reason: "disallowed topic"}}
		f := convSetup(t, rt, stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
		res := collectStream(f.conv.Send(ctx, sid, userMsg("hi")))
		if res.err != nil {
			t.Fatalf("send: %v", res.err)
		}
		if len(res.evs) != 2 {
			t.Fatalf("got %d events, want 2", len(res.evs))
		}
		gb, ok := res.evs[0].(types.GuardBlocked)
		if !ok || gb.Stage != types.StageInput || gb.Reason != "disallowed topic" {
			t.Fatalf("event %T %v, want GuardBlocked{input}", res.evs[0], res.evs[0])
		}
		done, ok := res.evs[1].(types.Done)
		if !ok || done.Reason != types.StopGuardBlocked {
			t.Fatalf("event %T %v, want Done{guard_blocked}", res.evs[1], res.evs[1])
		}
		if f.runs.SessionLeaseActive(ctx, sid) {
			t.Fatal("run still holds the lease after guard block")
		}
	})
}
