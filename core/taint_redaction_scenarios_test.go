package gohan

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/taint"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

var errRedactBoom = errors.New("redact boom")

// failingRedactor serves the send-path message redaction once, then fails.
// The window redaction is the second Redact call in a turn, so the failure
// lands in the effect, not in the Send entry path.
type failingRedactor struct{ calls int }

func (f *failingRedactor) Redact(_ context.Context, blocks []types.Block, _ types.RedactionPolicy) ([]types.Block, types.RedactionMap, error) {
	f.calls++
	if f.calls > 1 {
		return nil, types.RedactionMap{}, errRedactBoom
	}
	return blocks, types.RedactionMap{}, nil
}

func (failingRedactor) Rehydrate(_ context.Context, blocks []types.Block, _ types.RedactionMap) ([]types.Block, error) {
	return blocks, nil
}

func taintRedactionStack(t *testing.T, redactor types.Redactor, fetchResult string, turns ...gohantest.Turn) (*Stack, *gohantest.FakeTool, *gohantest.ScriptedModel, error) {
	t.Helper()
	hook, err := taint.New(taint.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fetch := gohantest.NewFakeTool("fetch", gohantest.WithToolResult(types.ToolResult{Outcome: types.Succeeded, Content: []types.Block{types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "fetch"}}, Text: fetchResult}}}))
	exfil := gohantest.NewFakeTool("exfil", gohantest.WithToolSpec(func(spec *types.ToolSpec) {
		spec.Capabilities.Exfil = true
		spec.Effect = types.ReadOnly
	}))
	model := gohantest.NewScriptedModel(types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}, turns...)
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))
	stack, err := Build(
		AllowAnonymous(),
		WithStores(stores.Stores{
			SessionLog:    stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:          runs,
			Checkpoints:   stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
			RedactionMaps: stores.NewMemoryRedactionMaps(),
		}),
		WithModels(model),
		WithRedactor(redactor),
		WithRedactionPolicy(scenarioPolicy()),
		WithNativeAgent(NativeSpec{
			Request:     FlowRequest{Name: "chat"},
			Profile:     "native",
			Tools:       []types.Tool{fetch, exfil},
			TaintPolicy: hook,
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	)
	return stack, exfil, model, err
}
func sendAll(t *testing.T, conv Conversation, text string) string {
	t.Helper()
	var sentErrs []string
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	for _, sendErr := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: text}}}) {
		if sendErr != nil {
			sentErrs = append(sentErrs, sendErr.Error())
		}
	}
	return strings.Join(sentErrs, "; ")
}

func TestTaintRedaction(t *testing.T) {
	t.Run("taint.matches-post-redaction", func(t *testing.T) {
		const entity = "Anna Petrova"
		redactor := scenarioRedactor()
		ri := types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1"}}
		tokenBlocks, _, err := redactor.Redact(types.WithRunInfo(context.Background(), ri), []types.Block{types.Text{Text: entity}}, scenarioPolicy())
		if err != nil {
			t.Fatal(err)
		}
		token := tokenBlocks[0].(types.Text).Text

		env := &turnEnv{
			c: turnConfig{redactor: redactor, redactionPolicy: scenarioPolicy()},
			msgs: []types.Message{{Blocks: []types.Block{types.Text{
				BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel, Name: "native"}},
				Text:      "Ledger for " + entity,
			}}}},
		}
		window, err := env.taintWindow(types.WithRunInfo(context.Background(), ri))
		if err != nil {
			t.Fatal(err)
		}
		got := window[0].(types.Text).Text
		if !strings.Contains(got, token) || strings.Contains(got, entity) {
			t.Fatalf("post-redaction window = %q, want the pseudonym %q without the raw entity", got, token)
		}

		hook, err := taint.New(taint.Default(), nil)
		if err != nil {
			t.Fatal(err)
		}
		spec := types.ToolSpec{Name: "exfil", Capabilities: types.Capabilities{Exfil: true}, Effect: types.ReadOnly}
		args := jsontext.Value(`{"q":"` + token + `"}`)
		action, taints := hook(types.WithTaintWindow(context.Background(), window), spec, args)
		if action != types.TaintDeny || len(taints) == 0 {
			t.Fatalf("redacted window verdict = %v, taints = %v; want the exfil check to fire", action, taints)
		}
		rawAction, rawTaints := hook(types.WithTaintWindow(context.Background(), rawTaintWindow(env.msgs)), spec, args)
		if rawAction != types.TaintAllow || len(rawTaints) != 0 {
			t.Fatalf("raw window verdict = %v, taints = %v; want no taint on the pseudonym argument", rawAction, rawTaints)
		}

		bare, err := (&turnEnv{msgs: env.msgs}).taintWindow(types.WithRunInfo(context.Background(), ri))
		if err != nil {
			t.Fatal(err)
		}
		if bareText := bare[0].(types.Text).Text; !strings.Contains(bareText, entity) || strings.Contains(bareText, token) {
			t.Fatalf("window without a redactor = %q, want the raw entity and no pseudonym", bareText)
		}
	})

	t.Run("window redaction error fails turn", func(t *testing.T) {
		stack, exfil, _, err := taintRedactionStack(t, &failingRedactor{}, "plain result",
			gohantest.ToolCall("exfil", map[string]any{"value": "anything"}),
			gohantest.Text("done"),
		)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		conv, err := NewNativeConversation(stack, "chat",
			WithConversationRuns(stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		)
		if err != nil {
			t.Fatalf("NewNativeConversation: %v", err)
		}
		sent := sendAll(t, conv, "plain user text")
		if !strings.Contains(sent, "redact taint window") {
			t.Fatalf("Send errors = %q, want a redact taint window failure", sent)
		}
		if calls := exfil.Calls(); len(calls) != 0 {
			t.Fatalf("exfil executed %d times, want 0", len(calls))
		}
	})
}
