package gohan

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// lifecycleStack builds one stack carrying both a native and a foreign
// flow, so both constructors bind and admit against the same stack.
func lifecycleStack(t *testing.T) *Stack {
	t.Helper()
	stack, err := Build(
		WithStores(stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}),
		WithModels(&funcModel{fn: func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			return func(yield func(types.ModelChunk, error) bool) {
				yield(types.ModelChunk{Kind: types.DeltaText, Delta: "hi"}, nil)
				yield(types.ModelChunk{Finish: types.FinishStop}, nil)
			}
		}}, foreignFlowModel{}),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   []types.Tool{noopTool{}},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
		WithForeignAgent(ForeignSpec{
			Request:    FlowRequest{Name: "assist"},
			Profile:    "foreign-test",
			NewRuntime: func() runtime.Runtime { return &constructionRT{} },
			Tools:      []types.Tool{&governedCountTool{}},
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return stack
}

// lifecycleRuns mints a runs store whose clock the test advances, plus the
// conversation bound to it. advance moves the store clock forward.
func lifecycleConversation(t *testing.T, rt runtime.Runtime, opts ...ConversationOption) (*conversation, *stores.MemoryRuns, func(time.Duration)) {
	t.Helper()
	stack, err := Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var mu sync.Mutex
	now := time.Now()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(func() time.Time { mu.Lock(); defer mu.Unlock(); return now }),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	stack.stores = stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}
	conv, err := NewConversation(stack, "chat", rt, append([]ConversationOption{
		WithConversationRuns(runs),
		WithConversationEventLog(events),
	}, opts...)...)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	return conv.(*conversation), runs, advance
}

// lifecycleDoneRT finishes on its first step.
type lifecycleDoneRT struct{}

func (lifecycleDoneRT) Name() string                         { return "lifecycle.done" }
func (lifecycleDoneRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }
func (lifecycleDoneRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}
func (lifecycleDoneRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1}, nil, runtime.DoneStatus, nil
}

func TestConstructorBindings(t *testing.T) {
	t.Run("streams.handle-constructor-bindings", func(t *testing.T) {
		stack := lifecycleStack(t)
		native, err := NewNativeConversation(stack, "chat",
			WithConversationRuns(stores.NewMemoryRuns()),
			WithConversationEventLog(stores.NewMemoryEventLog()))
		if err != nil {
			t.Fatalf("native: %v", err)
		}
		foreign, err := NewForeignConversation(stack, "assist",
			WithConversationRuns(stores.NewMemoryRuns()),
			WithConversationEventLog(stores.NewMemoryEventLog()))
		if err != nil {
			t.Fatalf("foreign: %v", err)
		}
		for name, conv := range map[string]Conversation{"native": native, "foreign": foreign} {
			c := conv.(*conversation)
			if c.stack != stack {
				t.Fatalf("%s: stack not bound", name)
			}
			if c.tenant != stack.Tenant() {
				t.Fatalf("%s: tenant %v, want %v", name, c.tenant, stack.Tenant())
			}
			if c.titleProfile != stack.titleProfile {
				t.Fatalf("%s: title profile %q, want %q", name, c.titleProfile, stack.titleProfile)
			}
			if (c.flags == nil) != (stack.flags == nil) {
				t.Fatalf("%s: flags snapshot not bound", name)
			}
			if (c.frozen == nil) != (stack.frozen == nil) {
				t.Fatalf("%s: frozen snapshot not bound", name)
			}
		}
	})

	t.Run("smoke.send-through-each-constructor", func(t *testing.T) {
		stack := lifecycleStack(t)
		runs := stores.NewMemoryRuns()
		native, err := NewNativeConversation(stack, "chat",
			WithConversationRuns(runs),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))))
		if err != nil {
			t.Fatalf("native: %v", err)
		}
		res := collectStream(native.Send(principalCtx(context.Background()), "sn", userMsg("hi")))
		if res.err != nil {
			t.Fatalf("native send: %v", res.err)
		}
		foreign, err := NewForeignConversation(stack, "assist",
			WithConversationRuns(runs),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))))
		if err != nil {
			t.Fatalf("foreign: %v", err)
		}
		res = collectStream(foreign.Send(principalCtx(context.Background()), "sf", userMsg("go")))
		if res.err != nil {
			t.Fatalf("foreign send: %v", res.err)
		}
	})

	t.Run("streams.handle-native-foreign-share-stack", func(t *testing.T) {
		stack := lifecycleStack(t)
		mk := func(kind, name string) Conversation {
			t.Helper()
			var conv Conversation
			var err error
			if kind == "native" {
				conv, err = NewNativeConversation(stack, name,
					WithConversationRuns(stores.NewMemoryRuns()),
					WithConversationEventLog(stores.NewMemoryEventLog()))
			} else {
				conv, err = NewForeignConversation(stack, name,
					WithConversationRuns(stores.NewMemoryRuns()),
					WithConversationEventLog(stores.NewMemoryEventLog()))
			}
			if err != nil {
				t.Fatalf("%s %s: %v", kind, name, err)
			}
			return conv
		}
		native := mk("native", "chat")
		foreign := mk("foreign", "assist")
		if err := stack.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		for name, conv := range map[string]Conversation{"native": native, "foreign": foreign} {
			res := collectStream(conv.Send(principalCtx(context.Background()), "s1", userMsg("hi")))
			if !errors.Is(res.err, types.ErrShuttingDown) {
				t.Fatalf("%s send after shutdown = %v, want ErrShuttingDown", name, res.err)
			}
			res = collectStream(conv.Continue(principalCtx(context.Background()), "s1"))
			if !errors.Is(res.err, types.ErrShuttingDown) {
				t.Fatalf("%s continue after shutdown = %v, want ErrShuttingDown", name, res.err)
			}
		}
	})
}

func TestShutdownAdmission(t *testing.T) {
	t.Run("runtime.shutdown-admission-atomic", func(t *testing.T) {
		stack, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		// A run admitted before Shutdown stays registered: Shutdown arms
		// its preemption and the wait names it as incomplete when the
		// run holds its safe point past the deadline.
		const runID = "run-held"
		pre := NewPreemptor()
		if err := stack.beginRun(runID, pre); err != nil {
			t.Fatalf("beginRun: %v", err)
		}
		// Concurrent admissions interleave with Shutdown: each one is
		// either refused or registered, and none lands after Shutdown
		// returned with the flag armed.
		var armed atomic.Bool
		var late atomic.Int32
		var raced atomic.Int64
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 50 {
					p := NewPreemptor()
					id := fmt.Sprintf("run-race-%d", raced.Add(1))
					if err := stack.beginRun(id, p); err != nil {
						if !errors.Is(err, types.ErrShuttingDown) && armed.Load() {
							t.Errorf("refusal after arm: %v", err)
							return
						}
						continue
					}
					if armed.Load() {
						late.Add(1)
					}
					stack.endRun(id)
				}
			}()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		var inc *ShutdownIncomplete
		if err := stack.Shutdown(ctx); !errors.As(err, &inc) {
			t.Fatalf("shutdown = %v, want ShutdownIncomplete", err)
		}
		if !slices.Contains(inc.RunIDs, runID) {
			t.Fatalf("shutdown incomplete %v, want held run %s named", inc.RunIDs, runID)
		}
		for _, id := range inc.RunIDs {
			if id != runID && !strings.HasPrefix(id, "run-race-") {
				t.Fatalf("shutdown incomplete names unexpected run %q", id)
			}
		}
		armed.Store(true)
		wg.Wait()
		if late.Load() != 0 {
			t.Fatalf("%d admissions landed after Shutdown returned", late.Load())
		}
		if err := stack.beginRun("run-late", NewPreemptor()); !errors.Is(err, types.ErrShuttingDown) {
			t.Fatalf("late beginRun = %v, want ErrShuttingDown", err)
		}
		stack.endRun(runID)
		if err := stack.Shutdown(context.Background()); err != nil {
			t.Fatalf("second shutdown: %v", err)
		}
	})

	t.Run("runtime.shutdown-admission-every-constructor", func(t *testing.T) {
		stack := lifecycleStack(t)
		native, err := NewNativeConversation(stack, "chat",
			WithConversationRuns(stores.NewMemoryRuns()),
			WithConversationEventLog(stores.NewMemoryEventLog()))
		if err != nil {
			t.Fatalf("native: %v", err)
		}
		foreign, err := NewForeignConversation(stack, "assist",
			WithConversationRuns(stores.NewMemoryRuns()),
			WithConversationEventLog(stores.NewMemoryEventLog()))
		if err != nil {
			t.Fatalf("foreign: %v", err)
		}
		if err := stack.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		for name, conv := range map[string]Conversation{"native": native, "foreign": foreign} {
			res := collectStream(conv.Send(principalCtx(context.Background()), "s1", userMsg("hi")))
			if !errors.Is(res.err, types.ErrShuttingDown) {
				t.Fatalf("%s send = %v, want ErrShuttingDown", name, res.err)
			}
			res = collectStream(conv.Continue(principalCtx(context.Background()), "s1"))
			if !errors.Is(res.err, types.ErrShuttingDown) {
				t.Fatalf("%s continue = %v, want ErrShuttingDown", name, res.err)
			}
		}
	})

	t.Run("runtime.shutdown-admission-resume", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		stack := &Stack{stores: stores.Stores{SessionLog: h.log}}
		conv, err := NewConversation(stack, "flights", h.rt,
			WithConversationRuns(h.runs),
			WithConversationEventLog(stores.NewMemoryEventLog()),
			WithConversationCheckpoints(h.cps),
			WithConversationCredentialSource(&resumeCreds{}),
			WithConversationApprovalPolicy(&authPolicySource{}),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		if err := stack.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		res := collectStream(conv.Resume(types.WithPrincipal(context.Background(), authOp), token, Approve()))
		if !errors.Is(res.err, types.ErrShuttingDown) {
			t.Fatalf("resume after shutdown = %v, want ErrShuttingDown", res.err)
		}
	})
}

func TestActiveRun(t *testing.T) {
	appendSession := func(t *testing.T, conv *conversation, sessionID string) {
		t.Helper()
		if _, err := conv.log.Append(types.WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: "u1"}), sessionID, 0, userMsg("seed")); err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}

	t.Run("streams.active-run-found", func(t *testing.T) {
		rt := &identityRT{entered: make(chan struct{}), gate: make(chan struct{})}
		conv, _, _ := lifecycleConversation(t, rt)
		res := make(chan streamResult, 1)
		go func() { res <- collectStream(conv.Send(principalCtx(context.Background()), "s1", userMsg("hi"))) }()
		<-rt.entered
		id, err := conv.ActiveRun(principalCtx(context.Background()), "s1")
		if err != nil {
			t.Fatalf("active run: %v", err)
		}
		if id == "" || id != liveRunID(conv, "s1") {
			t.Fatalf("active run id %q, live map %q", id, liveRunID(conv, "s1"))
		}
		close(rt.gate)
		<-res
	})

	t.Run("streams.detached-run-discoverable", func(t *testing.T) {
		rt := &identityRT{entered: make(chan struct{}), gate: make(chan struct{})}
		conv, _, _ := lifecycleConversation(t, rt, Detached())
		res := make(chan streamResult, 1)
		go func() { res <- collectStream(conv.Send(principalCtx(context.Background()), "s1", userMsg("hi"))) }()
		<-rt.entered
		id, err := conv.ActiveRun(principalCtx(context.Background()), "s1")
		if err != nil {
			t.Fatalf("detached active run: %v", err)
		}
		if id == "" {
			t.Fatal("detached run not discoverable while live")
		}
		close(rt.gate)
		<-res
	})

	t.Run("streams.active-run-absent", func(t *testing.T) {
		conv, _, _ := lifecycleConversation(t, lifecycleDoneRT{})
		appendSession(t, conv, "s-empty")
		id, err := conv.ActiveRun(principalCtx(context.Background()), "s-empty")
		if !errors.Is(err, types.ErrRunNotActive) || id != "" {
			t.Fatalf("absent = (%q, %v), want ErrRunNotActive with no id", id, err)
		}
	})

	t.Run("streams.active-run-terminal-or-expired", func(t *testing.T) {
		// Terminal: the run finished, so the session has no active run.
		conv, runs, _ := lifecycleConversation(t, lifecycleDoneRT{})
		appendSession(t, conv, "s-fin")
		res := collectStream(conv.Send(principalCtx(context.Background()), "s-fin", userMsg("hi")))
		if res.err != nil {
			t.Fatalf("send: %v", res.err)
		}
		id, err := conv.ActiveRun(principalCtx(context.Background()), "s-fin")
		if !errors.Is(err, types.ErrRunNotActive) || id != "" {
			t.Fatalf("terminal = (%q, %v), want ErrRunNotActive with no id", id, err)
		}

		// Suspended: the run holds no live lease.
		ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: "u1"})
		appendSession(t, conv, "s-susp")
		lease, err := runs.Start(ctx, stores.Run{SessionID: "s-susp", RunID: "run-susp", Flow: "chat"}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if err := runs.Suspend(ctx, lease, types.ResumeToken("tok")); err != nil {
			t.Fatalf("suspend: %v", err)
		}
		id, err = conv.ActiveRun(principalCtx(context.Background()), "s-susp")
		if !errors.Is(err, types.ErrRunNotActive) || id != "" {
			t.Fatalf("suspended = (%q, %v), want ErrRunNotActive with no id", id, err)
		}

		// Expired: the lease lapsed on the store clock even though the
		// local live map still names the run.
		rt := &identityRT{entered: make(chan struct{}), gate: make(chan struct{})}
		conv, _, advance := lifecycleConversation(t, rt)
		resCh := make(chan streamResult, 1)
		go func() { resCh <- collectStream(conv.Send(principalCtx(context.Background()), "s-exp", userMsg("hi"))) }()
		<-rt.entered
		advance(2 * stores.LeaseTTL)
		id, err = conv.ActiveRun(principalCtx(context.Background()), "s-exp")
		if !errors.Is(err, types.ErrRunNotActive) || id != "" {
			t.Fatalf("expired = (%q, %v), want ErrRunNotActive with no id", id, err)
		}
		close(rt.gate)
		<-resCh
	})

	t.Run("streams.active-run-owner-authorized", func(t *testing.T) {
		rt := &identityRT{entered: make(chan struct{}), gate: make(chan struct{})}
		conv, _, _ := lifecycleConversation(t, rt)
		res := make(chan streamResult, 1)
		go func() { res <- collectStream(conv.Send(principalCtx(context.Background()), "s1", userMsg("hi"))) }()
		<-rt.entered
		other := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t-b", Subject: "u2"})
		id, err := conv.ActiveRun(other, "s1")
		if !errors.Is(err, types.ErrSessionForbidden) || id != "" {
			t.Fatalf("unauthorized = (%q, %v), want ErrSessionForbidden with no id", id, err)
		}
		close(rt.gate)
		<-res
	})

	t.Run("streams.active-run-cross-instance", func(t *testing.T) {
		rt := &identityRT{entered: make(chan struct{}), gate: make(chan struct{})}
		conv, runs, _ := lifecycleConversation(t, rt)
		res := make(chan streamResult, 1)
		go func() { res <- collectStream(conv.Send(principalCtx(context.Background()), "s1", userMsg("hi"))) }()
		<-rt.entered
		// A second conversation on another instance shares only the runs
		// store; its live map is empty, so discovery rides the finder.
		other, err := NewConversation(conv.stack, "chat", &lifecycleDoneRT{},
			WithConversationRuns(runs),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))))
		if err != nil {
			t.Fatalf("second conversation: %v", err)
		}
		id, err := other.ActiveRun(principalCtx(context.Background()), "s1")
		if err != nil {
			t.Fatalf("cross-instance active run: %v", err)
		}
		if id == "" || id != liveRunID(conv, "s1") {
			t.Fatalf("cross-instance id %q, want %q", id, liveRunID(conv, "s1"))
		}
		close(rt.gate)
		<-res
	})
}
