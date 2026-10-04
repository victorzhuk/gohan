package gohan

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// continueRT records every AgentRun Start sees and appends one assistant
// message per turn, so the tests can observe what a driven run carries and
// what lands in the session log. Nothing waits on a timer.
type continueRT struct {
	mu    sync.Mutex
	log   stores.SessionLog
	sess  string
	runs  []runtime.AgentRun
	calls int
}

func (r *continueRT) Name() string                         { return "continue.test" }
func (r *continueRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *continueRT) Start(_ context.Context, ag runtime.AgentRun) (runtime.State, error) {
	r.mu.Lock()
	r.runs = append(r.runs, ag)
	r.mu.Unlock()
	return runtime.State{}, nil
}

func (r *continueRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	r.calls++
	log, sess := r.log, r.sess
	r.mu.Unlock()
	if log != nil && sess != "" {
		if h, err := log.Load(ctx, sess); err == nil {
			_, _ = log.Append(ctx, sess, h.Version, assistantMsg("continued"))
		}
	}
	return st, nil, runtime.DoneStatus, nil
}

func (r *continueRT) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func assistantMsg(text string) types.Message {
	return types.Message{
		Role: types.RoleAssistant,
		Blocks: []types.Block{types.Text{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}},
			Text:      text,
		}},
	}
}

func contSetup(t *testing.T, rt runtime.Runtime, log stores.SessionLog) *convFixture {
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
	return &convFixture{conv: conv, runs: runs, events: events, log: log}
}

func TestConversationContinue(t *testing.T) {
	ctx := principalCtx(context.Background())
	sid := "sess-1"

	t.Run("flow.continue-without-input", func(t *testing.T) {
		rt := &continueRT{}
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		f := contSetup(t, rt, log)
		rt.mu.Lock()
		rt.log, rt.sess = log, sid
		rt.mu.Unlock()
		res := collectStream(f.conv.Send(ctx, sid, userMsg("hi")))
		if res.err != nil {
			t.Fatalf("send: %v", res.err)
		}
		before, err := log.Load(ctx, sid)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		callsBefore := rt.callCount()
		cre := collectStream(f.conv.Continue(ctx, sid))
		if cre.err != nil {
			t.Fatalf("continue: %v", cre.err)
		}
		done, ok := cre.evs[len(cre.evs)-1].(types.Done)
		if !ok || done.Reason != types.StopCompleted {
			t.Fatalf("last event %T %v, want Done{completed}", cre.evs[len(cre.evs)-1], cre.evs[len(cre.evs)-1])
		}
		if got := rt.callCount() - callsBefore; got != 1 {
			t.Fatalf("continue drove %d turns, want 1", got)
		}
		if len(rt.runs) != 2 || len(rt.runs[1].Input) != 0 {
			t.Fatalf("continue run input = %d messages, want none", len(rt.runs[len(rt.runs)-1].Input))
		}
		after, err := f.log.Load(ctx, sid)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(after.Messages) != len(before.Messages)+1 {
			t.Fatalf("got %d messages after continue, want %d", len(after.Messages), len(before.Messages)+1)
		}
		users := 0
		for _, m := range after.Messages {
			if m.Role == types.RoleUser {
				users++
			}
		}
		if users != 1 {
			t.Fatalf("got %d user messages, want 1 (no input appended)", users)
		}
	})

	t.Run("continue-refused-without-assistant-turn", func(t *testing.T) {
		rt := &continueRT{}
		f := contSetup(t, rt, stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
		res := collectStream(f.conv.Continue(ctx, sid))
		if !errors.Is(res.err, types.ErrEmptyHistory) {
			t.Fatalf("got %v, want ErrEmptyHistory", res.err)
		}
		if rt.callCount() != 0 {
			t.Fatal("run started for an empty session")
		}
	})

	t.Run("continue-refused-while-run-live", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rt := &convRT{gate: make(chan struct{}), entered: make(chan struct{})}
			f := convSetup(t, rt, stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
			streamDone := make(chan streamResult, 1)
			go func() { streamDone <- collectStream(f.conv.Send(ctx, sid, userMsg("hi"))) }()
			<-rt.entered
			res := collectStream(f.conv.Continue(ctx, sid))
			if !errors.Is(res.err, types.ErrRunActive) {
				t.Fatalf("got %v, want ErrRunActive", res.err)
			}
			close(rt.gate)
			if res := <-streamDone; res.err != nil {
				t.Fatalf("send: %v", res.err)
			}
		})
	})

	t.Run("continue-refused-when-handed-off", func(t *testing.T) {
		rt := &continueRT{}
		log := controlledLog{
			SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			control:    stores.ControlHuman,
		}
		f := contSetup(t, rt, log)
		if _, err := log.Append(ctx, sid, 0, userMsg("hi")); err != nil {
			t.Fatalf("append: %v", err)
		}
		res := collectStream(f.conv.Continue(ctx, sid))
		if !errors.Is(res.err, types.ErrSessionHandedOff) {
			t.Fatalf("got %v, want ErrSessionHandedOff", res.err)
		}
		f.runs.mu.Lock()
		starts := f.runs.starts
		f.runs.mu.Unlock()
		if starts != 0 {
			t.Fatalf("got %d runs started, want 0 for a handed-off session", starts)
		}
	})

	t.Run("flow.regenerate-is-fork-and-continue", func(t *testing.T) {
		rt := &continueRT{}
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		f := contSetup(t, rt, log)
		rt.mu.Lock()
		rt.log, rt.sess = log, sid
		rt.mu.Unlock()
		if res := collectStream(f.conv.Send(ctx, sid, userMsg("hi"))); res.err != nil {
			t.Fatalf("send: %v", res.err)
		}
		parentBefore, err := log.Load(ctx, sid)
		if err != nil {
			t.Fatalf("load parent: %v", err)
		}
		if len(parentBefore.Messages) < 2 {
			t.Fatalf("parent has %d messages, want a user turn and an assistant reply", len(parentBefore.Messages))
		}
		lastUser := parentBefore.Messages[0]
		if lastUser.Role != types.RoleUser {
			t.Fatalf("first message role = %v, want user", lastUser.Role)
		}
		var forker stores.SessionForker
		if f2, ok := any(log).(stores.SessionForker); ok {
			forker = f2
		} else {
			t.Fatal("session log does not satisfy SessionForker")
		}
		forkID, err := forker.Fork(ctx, sid, lastUser.ID)
		if err != nil {
			t.Fatalf("fork: %v", err)
		}
		forkBefore, err := log.Load(ctx, forkID)
		if err != nil {
			t.Fatalf("load fork: %v", err)
		}
		rt.mu.Lock()
		rt.log, rt.sess = log, forkID
		rt.mu.Unlock()
		res := collectStream(f.conv.Continue(ctx, forkID))
		if res.err != nil {
			t.Fatalf("continue on fork: %v", res.err)
		}
		done, ok := res.evs[len(res.evs)-1].(types.Done)
		if !ok || done.Reason != types.StopCompleted {
			t.Fatalf("last event %T %v, want Done{completed}", res.evs[len(res.evs)-1], res.evs[len(res.evs)-1])
		}
		// The fork's first model call carries the shared prefix: the run the
		// continue starts sees the fork's history untouched. Reporting
		// CachedInputTokens for that prefix is the assembly projection of
		// row 19 and is recorded as its half, not faked here.
		forkAfter, err := log.Load(ctx, forkID)
		if err != nil {
			t.Fatalf("load fork: %v", err)
		}
		if len(forkAfter.Messages) != len(forkBefore.Messages)+1 {
			t.Fatalf("fork has %d messages after continue, want %d", len(forkAfter.Messages), len(forkBefore.Messages)+1)
		}
		last := forkAfter.Messages[len(forkAfter.Messages)-1]
		if last.Role != types.RoleAssistant {
			t.Fatalf("fork's new message role = %v, want assistant", last.Role)
		}
		for i, m := range forkBefore.Messages {
			if forkAfter.Messages[i].ID != m.ID {
				t.Fatalf("fork message %d id = %q, want preserved %q", i, forkAfter.Messages[i].ID, m.ID)
			}
		}
		parentAfter, err := log.Load(ctx, sid)
		if err != nil {
			t.Fatalf("load parent: %v", err)
		}
		if len(parentAfter.Messages) != len(parentBefore.Messages) {
			t.Fatalf("parent has %d messages after the fork's continue, want %d (untouched)",
				len(parentAfter.Messages), len(parentBefore.Messages))
		}
		if len(rt.runs) != 2 || len(rt.runs[1].Input) != 0 {
			t.Fatalf("fork continue run input = %d messages, want none", len(rt.runs[len(rt.runs)-1].Input))
		}
	})
}
