package eino

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// preemptBlockingModel parks the turn's only chunk until the run's
// cancellation or the test's release arrives, after emitting one unappended
// delta through the run-scoped sink.
type preemptBlockingModel struct {
	entered   chan struct{}
	release   chan struct{}
	cancelled chan struct{}
}

func (m *preemptBlockingModel) Profile() types.ModelProfile { return conformanceProfile }

func (m *preemptBlockingModel) Generate(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if sink, _ := types.SinkFrom(ctx); sink != nil {
			sink.Emit(ctx, types.TextDelta{Turn: 1, MessageID: assistantID, Delta: "partial"})
		}
		close(m.entered)
		select {
		case <-ctx.Done():
			close(m.cancelled)
			yield(types.ModelChunk{}, ctx.Err())
		case <-m.release:
			yield(types.ModelChunk{}, errors.New("blocking model released without cancellation"))
		}
	}
}

// shieldTool plays the shielded side effect: it finishes under the cancel
// shield, journals its result through the sink and reports success no
// matter what the step's cancellation did.
type shieldTool struct {
	entered chan struct{}
	release chan struct{}

	mu  sync.Mutex
	ran int
}

func (t *shieldTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "book", Risk: types.RiskHigh, Effect: types.SideEffect}
}

func (t *shieldTool) Call(ctx context.Context, _ json.RawMessage) (types.ToolResult, error) {
	t.mu.Lock()
	t.ran++
	t.mu.Unlock()
	close(t.entered)
	select {
	case <-ctx.Done():
	case <-t.release:
	}
	res := types.ToolResult{ID: "call_book", Outcome: types.Succeeded}
	if sink, _ := types.SinkFrom(ctx); sink != nil {
		sink.Emit(ctx, types.ToolFinished{Turn: 1, Result: res})
	}
	return res, nil
}

func (t *shieldTool) calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ran
}

func passAssemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	return types.ModelRequest{Messages: in.History}, nil
}

// preemptStack builds a stack whose chat flow is eino-driven, with the
// memory stores and the doubles the stack paths need.
func preemptStack(t *testing.T, model types.Model) (*gohan.Stack, gohan.Conversation) {
	t.Helper()
	runsStore := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))
	stack, err := gohan.Build(
		gohan.WithModels(model),
		gohan.WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        runsStore,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		}),
		gohan.WithApprovalPolicySource(preemptPolicy{}),
		gohan.WithCredentialSource(preemptCreds{}),
		gohan.WithForeignAgent(gohan.ForeignSpec{
			Request: gohan.FlowRequest{Name: "chat"},
			Profile: conformanceProfile.Name,
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: in.History}, nil
			},
			NewRuntime: func() runtime.Runtime {
				rt, err := New(context.Background(), &adk.ChatModelAgent{})
				if err != nil {
					t.Fatalf("new eino runtime: %v", err)
				}
				return rt
			},
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := gohan.NewForeignConversation(stack, "chat",
		gohan.WithConversationRuns(runsStore),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("foreign conversation: %v", err)
	}
	return stack, conv
}

type preemptPolicy struct{}

func (preemptPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{Scope: "approve:tool", Quorum: 1}, nil
}

type preemptCreds struct{}

func (preemptCreds) Credentials(context.Context, types.Principal) (types.Credential, error) {
	return types.Credential{}, nil
}

func preemptPrincipal(ctx context.Context) context.Context {
	return types.WithPrincipal(ctx, types.Principal{Tenant: "t1", Subject: "u1"})
}

type preemptStream struct {
	evs []types.Event
	err error
}

func collectPreemptStream(seq iter.Seq2[types.Event, error]) <-chan preemptStream {
	ch := make(chan preemptStream, 1)
	go func() {
		var out preemptStream
		for ev, err := range seq {
			if err != nil {
				out.err = err
				break
			}
			out.evs = append(out.evs, ev)
		}
		ch <- out
	}()
	return ch
}

func lastPreemptEvent(evs []types.Event) types.Event {
	if len(evs) == 0 {
		return nil
	}
	return evs[len(evs)-1]
}

func TestPreemption(t *testing.T) {
	ctx := preemptPrincipal(context.Background())

	t.Run("runtime.shutdown-rejects-new", func(t *testing.T) {
		model := gohantest.NewScriptedModel(conformanceProfile, gohantest.Text("hi"))
		stack, conv := preemptStack(t, model)
		for ev, err := range conv.Send(ctx, "s1", types.Message{
			Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}},
		}) {
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			if _, ok := ev.(types.Done); ok {
				break
			}
		}
		if err := stack.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if stack.Ready() {
			t.Fatal("Ready = true after shutdown, want false")
		}
		if err := stack.Shutdown(ctx); err != nil {
			t.Fatalf("second shutdown: %v", err)
		}
	})

	t.Run("runtime.shutdown-preempts-at-safe-point", func(t *testing.T) {
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now))
		cps := stores.NewMemoryCheckpoints()
		model := gohantest.NewScriptedModel(conformanceProfile,
			gohantest.ToolCall("echo", map[string]string{"in": "1"}),
			gohantest.Text("done"),
		)
		rt, err := New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		pre := gohan.NewPreemptor()
		lease, err := runs.Start(ctx, stores.Run{SessionID: "s1", RunID: "r1", Flow: "chat"}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("start run: %v", err)
		}
		lc := gohan.NewLifecycle(gohan.WithLifecycleRuns(runs, lease), gohan.WithLifecycleSession("s1"))
		var saved stores.Checkpoint
		ag := runtime.AgentRun{
			Model:    model,
			Assemble: passAssemble,
			Tools:    []types.Tool{gohantest.NewFakeTool("echo")},
			Save: func(_ context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
				saved = cp
				return cps.Put(ctx, cp)
			},
		}
		next, stop := iter.Pull2(gohan.DriveLifecycle(ctx, lc, pre.Runtime(rt), ag))
		defer stop()
		first, _, ok := next()
		if !ok {
			t.Fatal("stream ended before the first turn")
		}
		if _, isAsst := first.(types.AssistantMessage); !isAsst {
			t.Fatalf("first event %T, want AssistantMessage", first)
		}
		pre.Preempt()
		second, err, ok := next()
		if !ok || err != nil {
			t.Fatalf("second pull: ok=%v err=%v", ok, err)
		}
		sus, isSus := second.(types.Suspended)
		if !isSus {
			t.Fatalf("event %T, want Suspended", second)
		}
		if sus.Reason != types.Preempted || sus.Token == "" {
			t.Fatalf("suspended with reason %q token %q", sus.Reason, sus.Token)
		}
		if saved.Reason != types.Preempted {
			t.Fatalf("checkpoint reason %q, want preempted", saved.Reason)
		}
		var raw struct {
			State runtime.State `json:"state"`
		}
		if jerr := json.Unmarshal(saved.Data, &raw); jerr != nil {
			t.Fatalf("checkpoint state: %v", jerr)
		}
		if raw.State.Turn != 1 {
			t.Fatalf("checkpoint turn %d, want 1", raw.State.Turn)
		}
		if runs.SessionLeaseActive(ctx, "s1") {
			t.Fatal("lease still held after preemption")
		}
	})

	t.Run("runtime.shutdown-side-effect-completes", func(t *testing.T) {
		model := gohantest.NewScriptedModel(conformanceProfile,
			gohantest.ToolCall("book", map[string]string{"n": "1"}),
		)
		rt, err := New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		pre := gohan.NewPreemptor()
		tool := &shieldTool{entered: make(chan struct{}), release: make(chan struct{})}
		ag := runtime.AgentRun{
			Model:    model,
			Assemble: passAssemble,
			Tools:    []types.Tool{tool},
			Save:     stores.NewMemoryCheckpoints().Put,
		}
		ch := collectPreemptStream(gohan.DriveLifecycle(ctx, nil, pre.Runtime(rt), ag))
		<-tool.entered
		pre.Preempt()
		// No release: the shielded effect settles only through the
		// cancellation the wrapper injects, so the tool's completion
		// proves the shield, not a race with it.
		out := <-ch
		if out.err != nil {
			t.Fatalf("drive: %v", out.err)
		}
		sus, isSus := lastPreemptEvent(out.evs).(types.Suspended)
		if !isSus || sus.Reason != types.Preempted {
			t.Fatalf("last event %v, want Suspended{preempted}", lastPreemptEvent(out.evs))
		}
		var sawToolFinished bool
		for _, ev := range out.evs {
			if _, ok := ev.(types.ToolFinished); ok {
				sawToolFinished = true
			}
			if _, ok := ev.(types.AssistantMessage); ok {
				t.Fatal("the cancelled turn surfaced an assistant message")
			}
		}
		if !sawToolFinished {
			t.Fatal("the shielded side effect never surfaced")
		}
		if got := tool.calls(); got != 1 {
			t.Fatalf("side effect ran %d times, want 1", got)
		}
	})

	t.Run("runtime.shutdown-partial-model-call-dropped", func(t *testing.T) {
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now))
		cps := stores.NewMemoryCheckpoints()
		bm := &preemptBlockingModel{entered: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{})}
		rt, err := New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		pre := gohan.NewPreemptor()
		lease, err := runs.Start(ctx, stores.Run{SessionID: "s2", RunID: "r2", Flow: "chat"}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("start run: %v", err)
		}
		lc := gohan.NewLifecycle(gohan.WithLifecycleRuns(runs, lease), gohan.WithLifecycleSession("s2"))
		var saved stores.Checkpoint
		ag := runtime.AgentRun{
			Model:    bm,
			Assemble: passAssemble,
			Save: func(_ context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
				saved = cp
				return cps.Put(ctx, cp)
			},
		}
		ch := collectPreemptStream(gohan.DriveLifecycle(ctx, lc, pre.Runtime(rt), ag))
		<-bm.entered
		pre.Preempt()
		<-bm.cancelled
		close(bm.release)
		dropped := <-ch
		if dropped.err != nil {
			t.Fatalf("drop drive: %v", dropped.err)
		}
		sus, isSus := lastPreemptEvent(dropped.evs).(types.Suspended)
		if !isSus || sus.Reason != types.Preempted {
			t.Fatalf("last event %v, want Suspended{preempted}", lastPreemptEvent(dropped.evs))
		}
		var sawDelta bool
		for _, ev := range dropped.evs {
			if _, ok := ev.(types.TextDelta); ok {
				sawDelta = true
			}
			if _, ok := ev.(types.AssistantMessage); ok {
				t.Fatal("a partial assistant message reached the stream")
			}
		}
		if !sawDelta {
			t.Fatal("the dropped call never streamed")
		}
		var raw struct {
			State runtime.State `json:"state"`
		}
		if jerr := json.Unmarshal(saved.Data, &raw); jerr != nil {
			t.Fatalf("checkpoint state: %v", jerr)
		}
		if raw.State.Turn != 0 {
			t.Fatalf("checkpoint turn %d, want 0: the same turn re-issues", raw.State.Turn)
		}

		resumedModel := gohantest.NewScriptedModel(conformanceProfile, gohantest.Text("done"))
		rt2, err := New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		lease2, err := runs.Start(ctx, stores.Run{SessionID: "s2", RunID: "r3", Flow: "chat"}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("restart: %v", err)
		}
		lc2 := gohan.NewLifecycle(gohan.WithLifecycleRuns(runs, lease2), gohan.WithLifecycleSession("s2"),
			gohan.WithLifecycleResumeState(raw.State))
		res := <-collectPreemptStream(gohan.DriveLifecycle(ctx, lc2, pre.Runtime(rt2), runtime.AgentRun{
			Model:    resumedModel,
			Assemble: passAssemble,
		}))
		if res.err != nil {
			t.Fatalf("resume drive: %v", res.err)
		}
		done, isDone := lastPreemptEvent(res.evs).(types.Done)
		if !isDone || done.Reason != types.StopCompleted {
			t.Fatalf("last event %v, want Done{completed}", lastPreemptEvent(res.evs))
		}
		if len(resumedModel.Requests()) != 1 {
			t.Fatalf("model calls on resume = %d, want one re-issue of the dropped turn", len(resumedModel.Requests()))
		}
	})

	t.Run("runtime.preempted-resume-continue", func(t *testing.T) {
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now))
		cps := stores.NewMemoryCheckpoints()
		model := gohantest.NewScriptedModel(conformanceProfile, gohantest.Text("hello"))
		rt, err := New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		pre := gohan.NewPreemptor()
		pre.Preempt()
		lease, err := runs.Start(ctx, stores.Run{SessionID: "s3", RunID: "r4", Flow: "chat"}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("start run: %v", err)
		}
		lc := gohan.NewLifecycle(gohan.WithLifecycleRuns(runs, lease), gohan.WithLifecycleSession("s3"))
		var saved stores.Checkpoint
		ag := runtime.AgentRun{
			Model:    model,
			Assemble: passAssemble,
			Save: func(_ context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
				saved = cp
				return cps.Put(ctx, cp)
			},
		}
		out := <-collectPreemptStream(gohan.DriveLifecycle(ctx, lc, pre.Runtime(rt), ag))
		if out.err != nil {
			t.Fatalf("drive: %v", out.err)
		}
		sus, isSus := lastPreemptEvent(out.evs).(types.Suspended)
		if !isSus || sus.Reason != types.Preempted || sus.Token == "" {
			t.Fatalf("last event %v, want Suspended{preempted}", lastPreemptEvent(out.evs))
		}
		var raw struct {
			State runtime.State `json:"state"`
		}
		if jerr := json.Unmarshal(saved.Data, &raw); jerr != nil {
			t.Fatalf("checkpoint state: %v", jerr)
		}

		resumedModel := gohantest.NewScriptedModel(conformanceProfile, gohantest.Text("done"))
		rt2, err := New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		lease2, err := runs.Start(ctx, stores.Run{SessionID: "s3", RunID: "r5", Flow: "chat"}, stores.LeaseTTL)
		if err != nil {
			t.Fatalf("restart: %v", err)
		}
		lc2 := gohan.NewLifecycle(gohan.WithLifecycleRuns(runs, lease2), gohan.WithLifecycleSession("s3"),
			gohan.WithLifecycleResumeState(raw.State))
		res := <-collectPreemptStream(gohan.DriveLifecycle(ctx, lc2, gohan.NewPreemptor().Runtime(rt2), runtime.AgentRun{
			Model:    resumedModel,
			Assemble: passAssemble,
		}))
		if res.err != nil {
			t.Fatalf("resume drive: %v", res.err)
		}
		done, isDone := lastPreemptEvent(res.evs).(types.Done)
		if !isDone || done.Reason != types.StopCompleted {
			t.Fatalf("last event %v, want Done{completed} with no approver", lastPreemptEvent(res.evs))
		}
	})

	t.Run("runtime.shutdown-grace-exhausted", func(t *testing.T) {
		bm := &graceModel{entered: make(chan struct{}), release: make(chan struct{})}
		stack, conv := preemptStack(t, bm)
		gctx, gcancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer gcancel()
		res := make(chan preemptStream, 1)
		go func() {
			var out preemptStream
			for ev, err := range conv.Send(gctx, "s1", types.Message{
				Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}},
			}) {
				if err != nil {
					out.err = err
					break
				}
				out.evs = append(out.evs, ev)
			}
			res <- out
		}()
		<-bm.entered
		start := time.Now()
		out := <-res
		if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
			t.Fatalf("send ended after %v, want the full grace bound", elapsed)
		}
		if out.err == nil {
			t.Fatal("send completed, want the grace bound to end it")
		}
		// No Shutdown ran: the send's own deadline is the backstop, so the
		// stack never began shutting down and still accepts runs. The cut
		// run stays registered for Recover to reclaim.
		if !stack.Ready() {
			t.Fatal("Ready = false without Shutdown, want true")
		}
		close(bm.release)
	})
}

// graceModel parks the flow's model call until the run's grace deadline or
// the test's release ends it, so the send's own deadline is the backstop a
// turn that never lands relies on.
type graceModel struct {
	entered chan struct{}
	release chan struct{}
}

func (m *graceModel) Profile() types.ModelProfile { return conformanceProfile }

func (m *graceModel) Generate(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		close(m.entered)
		select {
		case <-ctx.Done():
			yield(types.ModelChunk{}, ctx.Err())
		case <-m.release:
			yield(types.ModelChunk{}, errors.New("grace model released"))
		}
	}
}
