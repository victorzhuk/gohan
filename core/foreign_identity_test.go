package gohan

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// foreignIdentityRT is the backend runtime stand-in: its Step calls the flow's
// governed tools the way a foreign backend does, with a per-call context
// the test prepares. The backend alone decides which call ids it binds.
type foreignIdentityRT struct {
	mu      sync.Mutex
	ag      runtime.AgentRun
	prepare func(ctx context.Context, call types.ToolUse) context.Context
	calls   []types.ToolUse
	results []types.ToolResult
	inner   []types.ToolUse
	steps   int
}

func (r *foreignIdentityRT) Name() string                         { return "identity.test" }
func (r *foreignIdentityRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *foreignIdentityRT) Start(_ context.Context, ag runtime.AgentRun) (runtime.State, error) {
	r.mu.Lock()
	r.ag = ag
	r.mu.Unlock()
	return runtime.State{}, nil
}

func (r *foreignIdentityRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	r.steps++
	step := r.steps
	ag := r.ag
	r.mu.Unlock()
	status := runtime.Continue
	if step >= 2 {
		status = runtime.DoneStatus
	}
	pending := []types.ToolUse{
		{ID: "call-1", Name: "confirm", Args: json.RawMessage(`{"n":1}`)},
	}
	if step == 2 {
		pending = []types.ToolUse{
			{ID: "call-2", Name: "confirm", Args: json.RawMessage(`{"n":2}`)},
		}
	}
	for _, use := range pending {
		var tool types.Tool
		for _, cand := range ag.Tools {
			if cand.Spec().Name == use.Name {
				tool = cand
			}
		}
		if tool == nil {
			return st, nil, runtime.DoneStatus, nil
		}
		callCtx := ctx
		if r.prepare != nil {
			callCtx = r.prepare(ctx, use)
		}
		res, err := tool.Call(callCtx, use.Args)
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		r.mu.Lock()
		r.calls = append(r.calls, use)
		r.results = append(r.results, res)
		if carried, ok := types.ToolUseFrom(callCtx); ok {
			r.inner = append(r.inner, carried)
		}
		r.mu.Unlock()
	}
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1}, nil, status, nil
}

func identityConversation(t *testing.T, rt runtime.Runtime, tools ...types.Tool) Conversation {
	t.Helper()
	_, conv, _, _ := foreignIdentityStack(t, rt, tools...)
	return conv
}

// foreignIdentityStack builds the same foreign fixture as the
// conversation-only helper and hands back the stack, the runs store and a
// clock-aging func, so a test can plant a stale Running run and drive it
// through Recover.
func foreignIdentityStack(t *testing.T, rt runtime.Runtime, tools ...types.Tool) (*Stack, Conversation, *stores.MemoryRuns, func()) {
	t.Helper()
	if len(tools) == 0 {
		tools = []types.Tool{&governedCountTool{}}
	}
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	age := func() { now = now.Add(2 * stores.LeaseTTL) }
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(clock), stores.WithMemoryRunInfo(types.RunInfoFrom))
	stack, err := Build([]Option{
		WithModels(foreignFlowModel{}),
		WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        runs,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(clock), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom), stores.WithMemoryCheckpointRuns(runs)),
		}),
		WithApprovalPolicySource(&roundtripPolicy{}),
		WithCredentialSource(&authCreds{}),
		WithRecoveryRuntime("chat", rt),
		WithForeignAgent(ForeignSpec{
			Request:    FlowRequest{Name: "chat"},
			Profile:    "foreign-test",
			NewRuntime: func() runtime.Runtime { return rt },
			Tools:      tools,
			Decider:    prepAllowDecider{},
		}),
	}...)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := NewForeignConversation(stack, "chat",
		WithConversationRuns(stack.stores.Runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatal(err)
	}
	return stack, conv, runs, age
}

func TestGovernedForeignCallIdentity(t *testing.T) {
	newRT := func(prepare func(context.Context, types.ToolUse) context.Context) *foreignIdentityRT {
		return &foreignIdentityRT{prepare: prepare}
	}

	t.Run("runtime.foreign-call-identity-required", func(t *testing.T) {
		rt := newRT(nil) // the backend binds no call id
		conv := identityConversation(t, rt)
		sendForeign(t, conv)
		rt.mu.Lock()
		defer rt.mu.Unlock()
		if len(rt.results) == 0 {
			t.Fatal("no tool result observed, want the failed identity result")
		}
		res := rt.results[0]
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("result = %+v, want one Failed(Permanent) result", res)
		}
		if res.ID != "" {
			t.Errorf("result ID = %q, want empty", res.ID)
		}
		if res.Error.Message != runtime.NotExecutedPrefix+types.ErrForeignCallID.Error() {
			t.Errorf("message = %q, want %q", res.Error.Message, runtime.NotExecutedPrefix+types.ErrForeignCallID.Error())
		}
		if len(rt.inner) != 0 {
			t.Errorf("inner carried calls = %v, want none: the tool implementation must not run", rt.inner)
		}
	})

	t.Run("runtime.foreign-call-id-round-trip", func(t *testing.T) {
		rt := newRT(func(ctx context.Context, call types.ToolUse) context.Context {
			return types.WithToolUse(ctx, call)
		})
		conv := identityConversation(t, rt)
		sendForeign(t, conv)
		rt.mu.Lock()
		defer rt.mu.Unlock()
		if len(rt.inner) == 0 || rt.inner[0].ID != "call-1" {
			t.Fatalf("carried ids = %v, want call-1 inside the implementation", idsOf(rt.inner))
		}
		if len(rt.results) == 0 || rt.results[0].Outcome != types.Succeeded {
			t.Fatalf("result = %+v, want a succeeded valid call", rt.results)
		}
	})

	t.Run("carried-name-never-overrides", func(t *testing.T) {
		rt := newRT(func(ctx context.Context, call types.ToolUse) context.Context {
			return types.WithToolUse(ctx, types.ToolUse{ID: call.ID, Name: "impostor", Args: json.RawMessage(`{"n":99}`)})
		})
		conv := identityConversation(t, rt)
		sendForeign(t, conv)
		rt.mu.Lock()
		defer rt.mu.Unlock()
		if len(rt.inner) == 0 || len(rt.results) == 0 || rt.results[0].Outcome != types.Succeeded {
			t.Fatalf("carried = %v results = %v, want the registered tool's name and args to win", rt.inner, rt.results)
		}
	})

	t.Run("runtime.foreign-call-id-distinct", func(t *testing.T) {
		rt := newRT(func(ctx context.Context, call types.ToolUse) context.Context {
			return types.WithToolUse(ctx, call)
		})
		conv := identityConversation(t, rt)
		sendForeign(t, conv)
		rt.mu.Lock()
		defer rt.mu.Unlock()
		got := idsOf(rt.inner)
		if len(got) != 2 || got[0] != "call-1" || got[1] != "call-2" {
			t.Fatalf("carried ids = %v, want [call-1 call-2] under their own ids", got)
		}
		for _, res := range rt.results {
			if res.Outcome != types.Succeeded {
				t.Fatalf("result = %+v, want both valid calls succeeded", res)
			}
		}
	})
}

func idsOf(calls []types.ToolUse) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.ID)
	}
	return out
}
