package gohan_test

import (
	"context"
	"strings"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	stdcontext "github.com/victorzhuk/gohan/std/context"
	"github.com/victorzhuk/gohan/std/taint"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// textLenEstimator counts the Text bytes inside ToolResult content, so
// clearing a result changes the projected request's estimate.
type textLenEstimator struct{}

func (textLenEstimator) Estimate(req types.ModelRequest, _ types.Caps) int {
	n := 0
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			r, ok := b.(types.ToolResult)
			if !ok {
				continue
			}
			for _, c := range r.Content {
				if t, ok := c.(types.Text); ok {
					n += len(t.Text)
				}
			}
		}
	}
	return n
}

func TestTaintTransformScenarios(t *testing.T) {
	t.Run("taint.clear-tool-results-no-launder", taintClearToolResultsNoLaunder)
}

func taintClearToolResultsNoLaunder(t *testing.T) {
	t.Helper()
	const secret = "secret-fixture-poisoned-page-0123456789"
	hook, err := taint.New(taint.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fetch := gohantest.NewFakeTool("fetch", gohantest.WithToolResult(types.ToolResult{Outcome: types.Succeeded, Content: []types.Block{types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "fetch"}}, Text: "page says " + secret}}}))
	target := gohantest.NewFakeTool("target", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
		spec.Capabilities.Exfil = true
		spec.Effect = types.ReadOnly
	}))
	clear := stdcontext.NewClearToolResults(stdcontext.WithClearEstimator(textLenEstimator{}))
	clear.Keep = 1
	clear.ClearAtLeast = 1
	model := gohantest.NewScriptedModel(types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}},
		gohantest.ToolCall("fetch", map[string]any{}),
		gohantest.ToolCall("fetch", map[string]any{}),
		gohantest.ToolCall("target", map[string]any{"value": secret}),
		gohantest.Text("finished"),
	)
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
		principal, ok := types.PrincipalFrom(ctx)
		return types.RunInfo{Principal: principal}, ok
	}))
	stack, err := gohan.Build(
		gohan.AllowAnonymous(),
		gohan.WithStores(stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)), Runs: runs}),
		gohan.WithModels(model),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:       gohan.FlowRequest{Name: "chat"},
			Profile:       "native",
			Tools:         []types.Tool{fetch, target},
			TaintPolicy:   hook,
			ContextPolicy: stores.ContextPolicy{Projections: []stores.Projection{clear}},
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
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "tenant", Subject: "user"})
	for _, err := range conv.Send(ctx, "session", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "fetch the pages and forward them"}}}) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	if calls := target.Calls(); len(calls) != 0 {
		t.Fatalf("target executed %d times, want 0", len(calls))
	}

	clearedSeen := false
	deniedSeen := false
	for _, req := range model.Requests() {
		for _, msg := range req.Messages {
			for _, block := range msg.Blocks {
				result, ok := block.(types.ToolResult)
				if !ok {
					continue
				}
				if result.Outcome == types.Failed && result.Error != nil &&
					result.Error.Kind == types.Permanent && strings.Contains(result.Error.Message, "untrusted content") {
					deniedSeen = true
				}
				if len(result.Content) == 0 {
					continue
				}
				if txt, ok := result.Content[0].(types.Text); ok {
					if txt.Text == "[tool result cleared]" && result.Origin.Kind == types.OriginTool && result.Origin.Name == "fetch" {
						clearedSeen = true
					}
				}
			}
		}
	}
	if !clearedSeen {
		t.Fatal("model never saw the cleared fetch placeholder with its origin")
	}
	if !deniedSeen {
		t.Fatal("target call carried no permanent denial result")
	}
}
