package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/conformance"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// einoEcho is an imported eino InvokableTool: it satisfies eino's tool
// port directly and counts its invocations.
type einoEcho struct{ called int }

func (*einoEcho) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "echo", Desc: "echoes its input"}, nil
}

func (e *einoEcho) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	e.called++
	var in struct {
		In string `json:"in"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &in); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"text":%q}`, in.In), nil
}

// recorderDecider records the batch gate decision and returns a fixed
// verdict, so the test observes when the gate ran relative to the chain.
type recorderDecider struct {
	obs     *conformance.Observations
	verdict permission.Verdict
}

func (d recorderDecider) Decide(_ context.Context, _ *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	d.obs.Steps = append(d.obs.Steps, conformance.StepGate)
	return types.Decision[permission.Verdict]{Value: d.verdict, Confidence: 1}, nil
}

// noteMiddleware records one chain step around the tool call.
func noteMiddleware(obs *conformance.Observations) types.ToolMiddleware {
	return func(next types.ToolFunc) types.ToolFunc {
		return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			obs.Steps = append(obs.Steps, "note")
			return next(ctx, call)
		}
	}
}

func foreignTestStack(t *testing.T, model types.Model, bridged types.Tool, dec types.Decider[*permission.ToolInvocation, permission.Verdict], obs *conformance.Observations) (*gohan.Stack, gohan.Conversation) {
	t.Helper()
	runsStore := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))
	stack, err := gohan.Build(
		gohan.WithModels(model),
		gohan.WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        runsStore,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		}),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "chat"},
			Profile: conformanceProfile.Name,
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: in.History}, nil
			},
			Tools:     []types.Tool{bridged},
			ToolChain: chains.ToolChain{{Name: "note", Kind: chains.KindUser, Use: noteMiddleware(obs)}},
			Decider:   dec,
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, "chat",
		gohan.WithConversationRuns(runsStore),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		gohan.WithConversationApprovalPolicy(allowPolicy{}),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return stack, conv
}

type allowPolicy struct{}

func (allowPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{}, nil
}

func foreignPrincipalCtx(ctx context.Context) context.Context {
	return types.WithPrincipal(ctx, types.Principal{Tenant: "t1", Subject: "u1"})
}

func sendCollect(t *testing.T, conv gohan.Conversation, ctx context.Context) []types.Event {
	t.Helper()
	var evs []types.Event
	for ev, err := range conv.Send(ctx, "s1", types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}},
	}) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		evs = append(evs, ev)
	}
	return evs
}

func toolResultBlocks(t *testing.T, evs []types.Event) []types.ToolResult {
	t.Helper()
	var out []types.ToolResult
	for _, ev := range evs {
		if f, ok := ev.(types.ToolFinished); ok {
			out = append(out, f.Result)
		}
	}
	return out
}

func doneEvent(t *testing.T, evs []types.Event) types.Done {
	t.Helper()
	var done types.Done
	for _, ev := range evs {
		if d, ok := ev.(types.Done); ok {
			done = d
		}
	}
	if done.Reason != types.StopCompleted {
		t.Fatalf("done = %+v, want Done{StopCompleted}", done)
	}
	return done
}

// TestForeignToolUnderNative drives the exported bridge as a registered
// NativeSpec tool through Build, NewNativeConversation and Send: the full
// public path an imported eino tool takes under the governed native batch.
func TestForeignToolUnderNative(t *testing.T) {
	t.Run("runtime.foreign-tool-under-native", func(t *testing.T) {
		echo := &einoEcho{}
		bridged, err := NewForeignTool(context.Background(), echo, types.ToolSpec{Name: "echo", Effect: types.ReadOnly})
		if err != nil {
			t.Fatalf("NewForeignTool: %v", err)
		}
		obs := &conformance.Observations{}
		_, conv := foreignTestStack(t,
			gohantest.NewScriptedModel(conformanceProfile,
				gohantest.ToolCall("echo", map[string]string{"in": "hi"}),
				gohantest.Text("done"),
			),
			bridged,
			recorderDecider{obs: obs, verdict: permission.Allow},
			obs,
		)
		evs := sendCollect(t, conv, foreignPrincipalCtx(context.Background()))
		doneEvent(t, evs)

		results := toolResultBlocks(t, evs)
		if len(results) != 1 {
			t.Fatalf("tool results = %d, want 1", len(results))
		}
		if results[0].Outcome == types.Failed {
			t.Fatalf("result = %+v, want the mapped call to succeed", results[0])
		}
		if len(results[0].Content) != 1 {
			t.Fatalf("content blocks = %d, want the one verbatim Text block", len(results[0].Content))
		}
		text, ok := results[0].Content[0].(types.Text)
		if !ok {
			t.Fatalf("content[0] = %T, want types.Text", results[0].Content[0])
		}
		if text.Text != `{"text":"hi"}` {
			t.Fatalf("mapped text = %q, want the upstream string verbatim", text.Text)
		}
		if echo.called != 1 {
			t.Fatalf("foreign tool calls = %d, want 1", echo.called)
		}
		if len(obs.Steps) != 2 || obs.Steps[0] != conformance.StepGate || obs.Steps[1] != "note" {
			t.Fatalf("steps %v, want [gate note]: the batch gate decided before the chain middleware ran", obs.Steps)
		}
	})

	t.Run("denied_call_blocked", func(t *testing.T) {
		echo := &einoEcho{}
		bridged, err := NewForeignTool(context.Background(), echo, types.ToolSpec{Name: "echo", Effect: types.ReadOnly})
		if err != nil {
			t.Fatalf("NewForeignTool: %v", err)
		}
		obs := &conformance.Observations{}
		_, conv := foreignTestStack(t,
			gohantest.NewScriptedModel(conformanceProfile,
				gohantest.ToolCall("echo", map[string]string{"in": "hi"}),
				gohantest.Text("done"),
			),
			bridged,
			recorderDecider{obs: obs, verdict: permission.DenyVerdict},
			obs,
		)
		evs := sendCollect(t, conv, foreignPrincipalCtx(context.Background()))
		doneEvent(t, evs)

		results := toolResultBlocks(t, evs)
		if len(results) != 1 {
			t.Fatalf("tool results = %d, want 1", len(results))
		}
		res := results[0]
		if res.Outcome != types.Failed {
			t.Fatalf("outcome = %v, want Failed", res.Outcome)
		}
		if res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("error = %+v, want one permanent ToolError", res.Error)
		}
		if res.Error.Message != runtime.NotExecutedPrefix+"denied by policy" {
			t.Fatalf("message = %q, want %q", res.Error.Message, runtime.NotExecutedPrefix+"denied by policy")
		}
		if echo.called != 0 {
			t.Fatalf("foreign tool calls = %d, want 0 behind a denied call", echo.called)
		}
		if len(obs.Steps) != 1 || obs.Steps[0] != conformance.StepGate {
			t.Fatalf("steps %v, want only [gate]: a denied call never enters the chain", obs.Steps)
		}
	})
}
