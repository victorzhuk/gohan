package gohan

import (
	"context"
	"encoding/json"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// foreignFlowModel is the profile-backed model Build routes the foreign
// flow's governed invocations through.
type foreignFlowModel struct{}

func (foreignFlowModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "foreign-test", Caps: types.Caps{Tools: true}}
}

func (foreignFlowModel) Generate(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return nil
}

// governedCountTool is the flow's declared side-effect tool; the runtime
// receives the chain-wrapped wrapper Build resolved, never this value.
type governedCountTool struct {
	mu    sync.Mutex
	calls int
}

func (t *governedCountTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "confirm", Risk: types.RiskHigh, Effect: types.SideEffect}
}

func (t *governedCountTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

// constructionRT records every AgentRun a Start saw. In suspend mode the
// first step parks a side-effect call on HumanApproval; a later step
// completes the run.
type constructionRT struct {
	mu      sync.Mutex
	seen    []runtime.AgentRun
	steps   int
	suspend bool
}

func (r *constructionRT) Name() string                         { return "construction.test" }
func (r *constructionRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *constructionRT) Start(_ context.Context, ag runtime.AgentRun) (runtime.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, ag)
	return runtime.State{}, nil
}

func (r *constructionRT) Step(_ context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps++
	if r.suspend && r.steps == 1 {
		st.Pending = []types.ToolUse{{ID: "c1", Name: "confirm", Args: json.RawMessage(`{"n":1}`)}}
		return st, nil, runtime.Continue, &types.SuspendError{Reason: types.HumanApproval}
	}
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1}, nil, runtime.DoneStatus, nil
}

func (r *constructionRT) runs() []runtime.AgentRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runtime.AgentRun(nil), r.seen...)
}

func foreignConstructionStack(t *testing.T, rt *constructionRT, extra ...Option) (*Stack, func()) {
	t.Helper()
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	age := func() { now = now.Add(2 * stores.LeaseTTL) }
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(clock), stores.WithMemoryRunInfo(types.RunInfoFrom))
	opts := append([]Option{
		WithModels(foreignFlowModel{}),
		WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        runs,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(clock), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom), stores.WithMemoryCheckpointRuns(runs)),
		}),
		WithApprovalPolicySource(&roundtripPolicy{}),
		WithCredentialSource(&authCreds{}),
		WithForeignAgent(ForeignSpec{
			Request:    FlowRequest{Name: "chat"},
			Profile:    "foreign-test",
			NewRuntime: func() runtime.Runtime { return rt },
			Tools:      []types.Tool{&governedCountTool{}},
		}),
	}, extra...)
	st, err := Build(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return st, age
}

func foreignConstructionConversation(t *testing.T, stack *Stack) Conversation {
	t.Helper()
	conv, err := NewForeignConversation(stack, "chat",
		WithConversationRuns(stack.stores.Runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatal(err)
	}
	return conv
}

func sendForeign(t *testing.T, conv Conversation) types.ResumeToken {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), authOwner)
	var token types.ResumeToken
	for ev, err := range conv.Send(ctx, "s1", types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}},
	}) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	return token
}

func assertGovernedRun(t *testing.T, ag runtime.AgentRun) {
	t.Helper()
	if ag.Model == nil {
		t.Error("AgentRun.Model is nil")
	}
	if ag.Tools == nil || len(ag.Tools) != 1 {
		t.Errorf("AgentRun.Tools = %v, want the flow's governed tool", ag.Tools)
	}
	if ag.Assemble == nil {
		t.Error("AgentRun.Assemble is nil")
	}
	if ag.History.Version == 0 {
		t.Errorf("AgentRun.History.Version = %d, want the loaded session history", ag.History.Version)
	}
	if ag.Save == nil {
		t.Error("AgentRun.Save is nil")
	}
}

func TestForeignRunConstruction(t *testing.T) {
	t.Run("send", func(t *testing.T) {
		rt := &constructionRT{}
		stack, _ := foreignConstructionStack(t, rt)
		conv := foreignConstructionConversation(t, stack)
		sendForeign(t, conv)
		seen := rt.runs()
		if len(seen) != 1 {
			t.Fatalf("starts = %d, want 1", len(seen))
		}
		if len(seen[0].Input) != 1 {
			t.Fatalf("Input = %v, want the sent message", seen[0].Input)
		}
		assertGovernedRun(t, seen[0])
	})

	t.Run("resume", func(t *testing.T) {
		rt := &constructionRT{suspend: true}
		stack, _ := foreignConstructionStack(t, rt)
		conv := foreignConstructionConversation(t, stack)
		token := sendForeign(t, conv)
		if token == "" {
			t.Fatal("Send: no Suspended token")
		}
		ctx := types.WithPrincipal(context.Background(), authOp)
		for ev, err := range conv.Resume(ctx, token, Continue()) {
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			if _, ok := ev.(types.Done); !ok && ev == nil {
				t.Fatalf("Resume: unexpected nil event")
			}
		}
		seen := rt.runs()
		if len(seen) != 2 {
			t.Fatalf("starts = %d, want 2", len(seen))
		}
		if len(seen[1].Input) != 0 {
			t.Fatalf("resume Input = %v, want none", seen[1].Input)
		}
		assertGovernedRun(t, seen[1])
	})

	t.Run("recovery", func(t *testing.T) {
		rt := &constructionRT{}
		stack, age := foreignConstructionStack(t, rt, WithRecoveryRuntime("chat", rt))
		ctx := types.WithPrincipal(context.Background(), authOwner)
		if _, err := stack.stores.SessionLog.Append(ctx, "s1", 0, types.Message{
			Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := stack.stores.Runs.Start(ctx, stores.Run{
			SessionID: "s1", RunID: "r1", Flow: "chat", State: stores.Running, Turn: 1, Seq: 1,
		}, stores.LeaseTTL); err != nil {
			t.Fatal(err)
		}
		age()
		if err := stack.Recover(ctx, 10); err != nil {
			t.Fatal(err)
		}
		seen := rt.runs()
		if len(seen) != 1 {
			t.Fatalf("starts = %d, want 1", len(seen))
		}
		assertGovernedRun(t, seen[0])
	})
}
