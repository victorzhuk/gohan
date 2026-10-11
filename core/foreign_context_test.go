package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// observedCtx records the caller lineage a governed tool call saw.
type observedCtx struct {
	principal   types.Principal
	principalOK bool
	cred        types.Credential
	credOK      bool
	info        types.RunInfo
	infoOK      bool
	deadline    bool
}

// inspectTool is the flow's governed tool: it records the caller lineage
// its ctx carries, then succeeds.
type inspectTool struct {
	mu   sync.Mutex
	seen []observedCtx
}

func (t *inspectTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "confirm", Risk: types.RiskHigh, Effect: types.SideEffect}
}

func (t *inspectTool) Call(ctx context.Context, _ json.RawMessage) (types.ToolResult, error) {
	var o observedCtx
	o.principal, o.principalOK = PrincipalFrom(ctx)
	o.cred, o.credOK = CredentialFrom(ctx)
	o.info, o.infoOK = types.RunInfoFrom(ctx)
	_, o.deadline = ctx.Deadline()
	t.mu.Lock()
	t.seen = append(t.seen, o)
	t.mu.Unlock()
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

func (t *inspectTool) observations() []observedCtx {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]observedCtx(nil), t.seen...)
}

// lineageRT suspends after the first tool call, so the same governed tool
// runs once on Send and once on Resume. It binds the backend call id the
// way the shipped adapter does.
type lineageRT struct {
	tool  *inspectTool
	mu    sync.Mutex
	ag    runtime.AgentRun
	steps int
}

func (r *lineageRT) Name() string                         { return "lineage.test" }
func (r *lineageRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *lineageRT) Start(_ context.Context, ag runtime.AgentRun) (runtime.State, error) {
	r.mu.Lock()
	r.ag = ag
	r.mu.Unlock()
	return runtime.State{}, nil
}

func (r *lineageRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	r.steps++
	step := r.steps
	ag := r.ag
	r.mu.Unlock()
	for _, t := range ag.Tools {
		if t.Spec().Name != "confirm" {
			continue
		}
		if _, err := t.Call(types.WithToolUse(ctx, types.ToolUse{ID: "call-1", Name: "confirm"}), json.RawMessage(`{}`)); err != nil {
			return st, nil, runtime.Continue, err
		}
	}
	if step == 1 {
		st.Pending = []types.ToolUse{{ID: "call-1", Name: "confirm", Args: json.RawMessage(`{}`)}}
		return st, nil, runtime.Continue, &types.SuspendError{Reason: types.HumanApproval}
	}
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1}, nil, runtime.DoneStatus, nil
}

func lineageCallerCtx() (context.Context, context.CancelFunc) {
	ctx := types.WithPrincipal(context.Background(), authOwner)
	ctx = WithCredential(ctx, types.Credential{Token: "stale"})
	ctx = types.WithRunInfo(ctx, types.RunInfo{RunID: "root", RootRunID: "root"})
	return context.WithTimeout(ctx, time.Hour)
}

func TestForeignContextPropagation(t *testing.T) {
	t.Run("runtime.foreign-ctx-preserved", func(t *testing.T) {
		assertForeignContextPropagation(t)
	})
}

func assertForeignContextPropagation(t *testing.T) {
	tool := &inspectTool{}
	rt := &lineageRT{tool: tool}
	conv := identityConversation(t, rt, tool)

	caller, cancel := lineageCallerCtx()
	defer cancel()
	var token types.ResumeToken
	for ev, err := range conv.Send(caller, "s1", types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}},
	}) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("Send did not suspend, want the approval token")
	}

	sendSeen := tool.observations()
	if len(sendSeen) == 0 {
		t.Fatal("the tool never ran on Send")
	}
	o := sendSeen[0]
	if !o.principalOK || o.principal.Tenant != authOwner.Tenant || o.principal.Subject != authOwner.Subject {
		t.Errorf("Send principal: got (%+v, %v), want authOwner", o.principal, o.principalOK)
	}
	if !o.infoOK {
		t.Error("Send RunInfoFrom: ok = false, want the caller run info")
	}
	if !o.credOK {
		t.Error("Send CredentialFrom: ok = false, want the caller credential")
	}
	if !o.deadline {
		t.Error("Send deadline: none observed, want the caller deadline")
	}

	resumeCtx := types.WithPrincipal(context.Background(), authOp)
	for ev, err := range conv.Resume(resumeCtx, token, Continue()) {
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		_ = ev
	}
	seen := tool.observations()
	if len(seen) < 2 {
		t.Fatalf("tool runs = %d, want one on Send and one on Resume", len(seen))
	}
	o = seen[len(seen)-1]
	// The resumed drive carries the checkpoint's originator, never the
	// resumer: the transport principal authorizes the resume, the run
	// identity stays the run's own.
	if !o.principalOK || o.principal.Subject != authOwner.Subject || o.principal.Tenant != authOwner.Tenant {
		t.Errorf("Resume principal: got (%+v, %v), want the checkpoint originator", o.principal, o.principalOK)
	}
	if !o.credOK || o.cred.Token != "fresh" {
		t.Errorf("Resume credential: got (%+v, %v), want the re-issued credential", o.cred, o.credOK)
	}

	// Recover: a stale Running run over the same fixture re-drives under
	// the reaping caller's lineage — the governed tool call observes the
	// same principal, credential, run identity and deadline the Send and
	// Resume paths carry.
	recTool := &inspectTool{}
	recRT := &lineageRT{tool: recTool}
	recStack, recConv, recRuns, age := foreignIdentityStack(t, recRT, recTool)
	caller2, cancel2 := lineageCallerCtx()
	defer cancel2()
	for _, err := range recConv.Send(caller2, "s1", types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}},
	}) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	h, err := recStack.stores.SessionLog.Load(types.WithPrincipal(context.Background(), authOwner), "s1")
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if _, err := recRuns.Start(context.Background(), stores.Run{
		SessionID: "s1", RunID: "r2", Flow: "chat", State: stores.Running, Turn: 1, Seq: h.Version,
	}, stores.LeaseTTL); err != nil {
		t.Fatal(err)
	}
	age()
	reaper, cancelReaper := lineageCallerCtx()
	defer cancelReaper()
	if err := recStack.Recover(reaper, 10); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	recSeen := recTool.observations()
	if len(recSeen) < 2 {
		t.Fatalf("recovery tool runs = %d, want one on Send and one on Recover", len(recSeen))
	}
	o = recSeen[len(recSeen)-1]
	if !o.principalOK || o.principal.Tenant != authOwner.Tenant || o.principal.Subject != authOwner.Subject {
		t.Errorf("Recover principal: got (%+v, %v), want the caller principal", o.principal, o.principalOK)
	}
	if !o.infoOK {
		t.Error("Recover RunInfoFrom: ok = false, want the caller run info")
	}
	if !o.credOK || o.cred.Token != "fresh" {
		t.Errorf("Recover credential: got (%+v, %v), want the re-issued credential", o.cred, o.credOK)
	}
	if !o.deadline {
		t.Error("Recover deadline: none observed, want the caller deadline")
	}
}

// cancelTool is the flow's read-only tool: it blocks until its context is
// cancelled, records that, and reports the cancellation.
type cancelTool struct {
	mu        sync.Mutex
	sawCancel bool
}

func (t *cancelTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "confirm", Effect: types.ReadOnly}
}

func (t *cancelTool) Call(ctx context.Context, _ json.RawMessage) (types.ToolResult, error) {
	<-ctx.Done()
	t.mu.Lock()
	t.sawCancel = true
	t.mu.Unlock()
	return types.ToolResult{}, ctx.Err()
}

func (t *cancelTool) cancelled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sawCancel
}

// cancelRT drives the read-only tool on its first step.
type cancelRT struct {
	tool *cancelTool
}

func (r *cancelRT) Name() string                         { return "cancel.test" }
func (r *cancelRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *cancelRT) Start(_ context.Context, _ runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *cancelRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if _, err := r.tool.Call(ctx, json.RawMessage(`{}`)); err != nil {
		return st, nil, runtime.Continue, err
	}
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1}, nil, runtime.DoneStatus, nil
}

func TestForeignContextPropagationReadOnlyCancelsInFlight(t *testing.T) {
	tool := &cancelTool{}
	conv := identityConversation(t, &cancelRT{tool: tool}, tool)

	caller, cancel := context.WithCancel(types.WithPrincipal(context.Background(), authOwner))
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()

	var runErr error
	for _, err := range conv.Send(caller, "s1", types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}},
	}) {
		runErr = err
	}
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("Send error = %v, want context.Canceled", runErr)
	}
	if !tool.cancelled() {
		t.Fatal("the read-only tool never saw the cancellation")
	}
}
