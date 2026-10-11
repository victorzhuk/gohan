package gohan_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	std "github.com/victorzhuk/gohan/std"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// replayExecTool is the run_code stand-in: it counts its executions, so a
// journal replay is observable as a constant count.
type replayExecTool struct {
	execs int
}

func (t *replayExecTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "run_code", Effect: types.SideEffect}
}

func (t *replayExecTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	t.execs++
	return types.ToolResult{Content: []types.Block{types.Text{Text: "42"}}}, nil
}

func TestSandboxReplayNoReexec(t *testing.T) {
	t.Run("sandbox.replay-no-reexec", func(t *testing.T) {
		journal := stores.NewMemoryJournal()
		tool := &replayExecTool{}
		lookup := func(name string) (types.ToolSpec, bool) {
			return tool.Spec(), name == "run_code"
		}
		sessionID := func(context.Context) string { return "s1" }
		runs := stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		)
		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}},
			gohantest.ToolCall("run_code", map[string]any{"code": "print(42)"}),
			gohantest.Text("done"),
			gohantest.ToolCall("run_code", map[string]any{"code": "print(42)"}),
			gohantest.Text("done"),
		)
		stack, err := gohan.Build(
			gohan.AllowAnonymous(),
			gohan.WithStores(stores.Stores{
				SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
				Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
				Journal:     journal,
				Runs:        runs,
			}),
			gohan.WithModels(model),
			gohan.WithNativeAgent(gohan.NativeSpec{
				Request: gohan.FlowRequest{Name: "chat"},
				Profile: "native",
				Tools:   []types.Tool{tool},
				Decider: verifyAllowDecider{},
				ToolChain: chains.ToolChain{{
					Name: "journal",
					Kind: chains.KindJournal,
					Use: std.Journal(journal,
						std.WithJournalSpecs(lookup),
						std.WithJournalSessionID(sessionID),
					),
				}},
				Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
				},
			}),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
		msg := types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "run"}}}
		for i := range 2 {
			conv, err := gohan.NewNativeConversation(stack, "chat",
				gohan.WithConversationRuns(runs),
				gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
				gohan.WithConversationApprovalPolicy(verifyAllowPolicy{}),
			)
			if err != nil {
				t.Fatalf("new native conversation: %v", err)
			}
			for _, serr := range conv.Send(ctx, "s1", msg) {
				if serr != nil {
					t.Fatalf("send %d: %v", i, serr)
				}
			}
		}
		if tool.execs != 1 {
			t.Fatalf("run_code executed %d times, want the replay to keep it at 1", tool.execs)
		}
		entries, err := journal.ByFingerprint(ctx, "s1", std.CanonicalFingerprint("run_code", []byte(`{"code":"print(42)"}`)))
		if err != nil || len(entries) != 1 || entries[0].State != stores.Completed {
			t.Fatalf("journal entries %+v err %v, want one Completed entry", entries, err)
		}
	})
}
