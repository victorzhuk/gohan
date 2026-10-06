package gohan_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

type verifyAllowDecider struct{}

func (verifyAllowDecider) Decide(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
}

func TestVerifySend(t *testing.T) {
	t.Run("tools.verify-reconciles-unknown", func(t *testing.T) {
		journal := stores.NewMemoryJournal()
		audit := stores.NewMemoryAuditLog()
		booking := gohantest.NewFakeTool("book",
			gohantest.WithToolResult(types.ToolResult{
				Outcome: types.Unknown,
				Error:   &types.ToolError{Kind: types.OutcomeUnknown, Message: "timeout"},
			}),
			gohantest.WithToolSpec(func(spec *types.ToolSpec) {
				spec.Effect = types.SideEffect
				spec.Verify = func(_ context.Context, _ json.RawMessage, _ types.ToolResult) (types.Outcome, error) {
					return types.Succeeded, nil
				}
			}),
		)
		specLookup := func(name string) (types.ToolSpec, bool) {
			spec := booking.Spec()
			return spec, name == spec.Name
		}
		sessionID := func(context.Context) string { return "session" }
		model := gohantest.NewScriptedModel(types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}},
			gohantest.ToolCall("book", map[string]any{"guest": "ada"}),
			gohantest.Text("booked"),
		)
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			principal, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: principal}, ok
		}))
		stack, err := gohan.Build(
			gohan.AllowAnonymous(),
			gohan.WithStores(stores.Stores{
				SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
				Journal:    journal,
				Runs:       runs,
			}),
			gohan.WithModels(model),
			gohan.WithVerifier(&std.Verifier{Journal: journal, Specs: specLookup}),
			gohan.WithNativeAgent(gohan.NativeSpec{
				Request: gohan.FlowRequest{Name: "chat"},
				Profile: "native",
				Tools:   []types.Tool{booking},
				ToolChain: chains.ToolChain{{
					Name: "journal",
					Kind: chains.KindJournal,
					Use: std.Journal(journal,
						std.WithJournalSpecs(specLookup),
						std.WithJournalSessionID(sessionID),
					),
				}},
				Decider: verifyAllowDecider{},
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
		ctx = gohan.WithDecisionAudit(ctx, audit)
		var done types.Done
		seenDone := false
		for ev, err := range conv.Send(ctx, "session", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "book ada"}}}) {
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			if d, ok := ev.(types.Done); ok {
				done = d
				seenDone = true
			}
		}
		if !seenDone {
			t.Fatal("Send emitted no Done event")
		}
		args, err := json.Marshal(map[string]any{"guest": "ada"})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		entries, err := journal.ByFingerprint(ctx, "session", std.CanonicalFingerprint("book", args))
		if err != nil {
			t.Fatalf("ByFingerprint: %v", err)
		}
		reconciled := false
		for _, entry := range entries {
			if entry.Tool == "book" && entry.Result.Outcome == types.Succeeded {
				reconciled = true
			}
		}
		if !reconciled {
			t.Fatalf("journal entries = %+v, want recorded result Outcome Succeeded", entries)
		}
		modelSawVerified := false
		for _, req := range model.Requests() {
			for _, message := range req.Messages {
				for _, block := range message.Blocks {
					if result, ok := block.(types.ToolResult); ok && result.Outcome == types.Succeeded {
						modelSawVerified = true
					}
				}
			}
		}
		if !modelSawVerified {
			t.Fatal("model-visible result did not contain the verified Succeeded outcome")
		}
		if len(done.Uncertain) != 0 {
			t.Fatalf("run uncertainty = %v, want empty", done.Uncertain)
		}
	})
}
