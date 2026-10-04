package gohan

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// takeoverRT is a scripted runtime whose single Step blocks on a gate
// channel; every wait yields on a channel so synctest keeps working.
type takeoverRT struct {
	gate    chan struct{}
	entered chan struct{}
	once    sync.Once
	mu      sync.Mutex
	calls   int
}

func (r *takeoverRT) Name() string                         { return "takeover.test" }
func (r *takeoverRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *takeoverRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *takeoverRT) Step(_ context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if r.entered != nil {
		r.once.Do(func() { close(r.entered) })
	}
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return st, nil, runtime.DoneStatus, nil
}

func (r *takeoverRT) modelCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// takeoverLog serves history from the memory log and records control
// reads and writes.
type takeoverLog struct {
	*stores.MemorySessionLog
	mu      sync.Mutex
	control stores.SessionControl
	sets    []stores.SessionControl
}

func (l *takeoverLog) Control(context.Context, string) (stores.SessionControl, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.control, nil
}

func (l *takeoverLog) SetControl(_ context.Context, _ string, c stores.SessionControl) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.control = c
	l.sets = append(l.sets, c)
	return nil
}

func (l *takeoverLog) controlSets() []stores.SessionControl {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]stores.SessionControl(nil), l.sets...)
}

// takeoverExpirer records the tokens takeover resolved.
type takeoverExpirer struct {
	mu     sync.Mutex
	tokens []types.ResumeToken
}

func (e *takeoverExpirer) Expire(t types.ResumeToken) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tokens = append(e.tokens, t)
	return true
}

func (e *takeoverExpirer) expired() []types.ResumeToken {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]types.ResumeToken(nil), e.tokens...)
}

type takeoverFixture struct {
	conv    *conversation
	rt      *takeoverRT
	log     *takeoverLog
	runs    *stores.MemoryRuns
	expirer *takeoverExpirer
	audit   *stores.MemoryAuditLog
}

func takeoverSetup(t *testing.T, rt *takeoverRT, log *takeoverLog) *takeoverFixture {
	t.Helper()
	stack, err := Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	stack.stores = stores.Stores{SessionLog: log}
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	expirer := &takeoverExpirer{}
	audit := stores.NewMemoryAuditLog()
	conv, err := NewConversation(stack, "chat", rt,
		WithConversationRuns(runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	c, ok := conv.(*conversation)
	if !ok {
		t.Fatalf("NewConversation returned %T, want *conversation", conv)
	}
	return &takeoverFixture{conv: c, rt: rt, log: log, runs: runs, expirer: expirer, audit: audit}
}

var takeoverOperator = types.Principal{
	Tenant:  "t-a",
	Subject: "op-1",
	Scopes:  []string{types.ScopeSessionControl},
}

// seedSession creates the session row the takeover scope check requires
// before any store write.
func seedSession(t *testing.T, f *takeoverFixture, sid string) {
	t.Helper()
	ctx := principalCtx(context.Background())
	if _, err := f.log.Append(ctx, sid, 0, userMsg("seed")); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func takeoverCtx(ctx context.Context, f *takeoverFixture) context.Context {
	ctx = WithDecisionAudit(ctx, f.audit)
	return WithApprovalExpiry(ctx, f.expirer)
}

func auditKinds(t *testing.T, f *takeoverFixture) []stores.AuditKind {
	t.Helper()
	var kinds []stores.AuditKind
	for r, err := range f.audit.Read(context.Background(), "sess-t") {
		if err != nil {
			t.Fatalf("audit read: %v", err)
		}
		kinds = append(kinds, r.Kind)
	}
	return kinds
}

func TestSessionTakeover(t *testing.T) {
	t.Run("flow.takeover-pauses-agent", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			sid := "sess-t"
			rt := &takeoverRT{gate: make(chan struct{}), entered: make(chan struct{})}
			f := takeoverSetup(t, rt, &takeoverLog{MemorySessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))})
			ctx := takeoverCtx(principalCtx(context.Background()), f)

			streamDone := make(chan streamResult, 1)
			go func() {
				streamDone <- collectStream(f.conv.Send(ctx, sid, userMsg("run long")))
			}()
			<-rt.entered

			taken := make(chan error, 1)
			go func() {
				taken <- f.conv.TakeOver(ctx, sid, "tok-1", takeoverOperator)
			}()
			synctest.Wait()
			close(rt.gate)

			if err := <-taken; err != nil {
				t.Fatalf("TakeOver: %v", err)
			}
			res := <-streamDone
			if res.err != nil {
				t.Fatalf("stream: %v", res.err)
			}
			last := res.evs[len(res.evs)-1]
			done, ok := last.(types.Done)
			if !ok {
				t.Fatalf("last event %T, want Done", last)
			}
			if done.Reason != types.StopCancelled {
				t.Fatalf("reason = %q, want cancelled", done.Reason)
			}
			if ctrl, _ := f.log.Control(context.Background(), sid); ctrl != stores.ControlHuman {
				t.Fatalf("control = %v, want ControlHuman", ctrl)
			}
			if got := f.expirer.expired(); len(got) != 1 || got[0] != "tok-1" {
				t.Fatalf("expired = %v, want [tok-1]", got)
			}
			if f.runs.SessionLeaseActive(context.Background(), sid) {
				t.Fatal("run lease still live after takeover")
			}
		})
	})

	t.Run("flow.operator-send-origin", func(t *testing.T) {
		sid := "sess-t"
		rt := &takeoverRT{}
		f := takeoverSetup(t, rt, &takeoverLog{MemorySessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))})
		ctx := takeoverCtx(principalCtx(context.Background()), f)

		ev, err := f.conv.OperatorSend(ctx, sid, takeoverOperator, userMsg("try the other region"))
		if err != nil {
			t.Fatalf("OperatorSend: %v", err)
		}
		am, ok := ev.(types.AssistantMessage)
		if !ok {
			t.Fatalf("event %T, want AssistantMessage", ev)
		}
		if !am.Operator || am.Message.Role != types.RoleAssistant {
			t.Fatalf("operator=%v role=%q, want true assistant", am.Operator, am.Message.Role)
		}
		txt, ok := am.Message.Blocks[0].(types.Text)
		if !ok || txt.Origin != (types.Origin{Kind: types.OriginOperator, Name: "op-1"}) {
			t.Fatalf("block origin = %v, want OriginOperator{op-1}", am.Message.Blocks[0])
		}
		if n := rt.modelCalls(); n != 0 {
			t.Fatalf("model calls = %d, want 0", n)
		}
		h, err := f.log.Load(ctx, sid)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if last := h.Messages[len(h.Messages)-1]; last.Role != types.RoleAssistant {
			t.Fatalf("recorded role = %q, want assistant", last.Role)
		}
	})

	t.Run("pending token expires on takeover", func(t *testing.T) {
		sid := "sess-t"
		rt := &takeoverRT{}
		f := takeoverSetup(t, rt, &takeoverLog{MemorySessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))})
		seedSession(t, f, sid)
		ctx := takeoverCtx(principalCtx(context.Background()), f)

		if err := f.conv.TakeOver(ctx, sid, "tok-9", takeoverOperator); err != nil {
			t.Fatalf("TakeOver: %v", err)
		}
		if got := f.expirer.expired(); len(got) != 1 || got[0] != "tok-9" {
			t.Fatalf("expired = %v, want [tok-9]", got)
		}
		if got := f.log.controlSets(); len(got) != 1 || got[0] != stores.ControlHuman {
			t.Fatalf("control sets = %v, want [ControlHuman]", got)
		}
	})

	t.Run("transition is audited", func(t *testing.T) {
		sid := "sess-t"
		rt := &takeoverRT{}
		f := takeoverSetup(t, rt, &takeoverLog{MemorySessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))})
		seedSession(t, f, sid)
		ctx := takeoverCtx(principalCtx(context.Background()), f)

		if err := f.conv.TakeOver(ctx, sid, "", takeoverOperator); err != nil {
			t.Fatalf("TakeOver: %v", err)
		}
		if err := f.conv.HandBack(ctx, sid, takeoverOperator); err != nil {
			t.Fatalf("HandBack: %v", err)
		}
		kinds := auditKinds(t, f)
		if len(kinds) != 2 || kinds[0] != auditHandoffAccepted || kinds[1] != auditHandback {
			t.Fatalf("audit kinds = %v, want [handoff_accepted handback]", kinds)
		}
		if got := f.log.controlSets(); len(got) != 2 || got[0] != stores.ControlHuman || got[1] != stores.ControlAgent {
			t.Fatalf("control sets = %v, want [ControlHuman ControlAgent]", got)
		}
		var approved []string
		for r, err := range f.audit.Read(context.Background(), sid) {
			if err != nil {
				t.Fatalf("audit read: %v", err)
			}
			approved = append(approved, r.Approver)
		}
		if len(approved) != 2 || approved[0] != "op-1" || approved[1] != "op-1" {
			t.Fatalf("approvers = %v, want [op-1 op-1]", approved)
		}
	})

	t.Run("an operator send issues no model call", func(t *testing.T) {
		sid := "sess-t"
		rt := &takeoverRT{}
		f := takeoverSetup(t, rt, &takeoverLog{MemorySessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))})
		ctx := takeoverCtx(principalCtx(context.Background()), f)

		for i := range 3 {
			if _, err := f.conv.OperatorSend(ctx, sid, takeoverOperator, userMsg("note")); err != nil {
				t.Fatalf("OperatorSend %d: %v", i, err)
			}
		}
		if n := rt.modelCalls(); n != 0 {
			t.Fatalf("model calls = %d, want 0", n)
		}
	})
}
