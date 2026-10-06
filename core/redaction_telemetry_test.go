package gohan

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

func redactionTelStack(t *testing.T, tel *fakeTelemetry, buf *bytes.Buffer, out stores.OutputStore, token string) *Stack {
	t.Helper()
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))
	native := NativeSpec{
		Request: FlowRequest{Name: "chat"},
		Profile: "primary",
		Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
		},
	}
	stack, err := Build(
		AllowAnonymous(),
		WithTelemetry(tel),
		WithLogger(captureLogger(buf)),
		WithStores(stores.Stores{
			SessionLog:    stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:          runs,
			Checkpoints:   stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
			RedactionMaps: stores.NewMemoryRedactionMaps(),
			OutputStore:   out,
		}),
		WithModels(gohantest.NewScriptedModel(types.ModelProfile{Name: "primary"}, gohantest.Text(token+" acknowledged"))),
		WithRedactor(scenarioRedactor()),
		WithRedactionPolicy(scenarioPolicy()),
		WithNativeAgent(native),
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return stack
}

func assertNoEntityInAttrs(t *testing.T, prefix string, attrs []types.Attr, entities ...string) {
	t.Helper()
	for _, a := range attrs {
		got := fmt.Sprintf("%v", a.Value)
		for _, e := range entities {
			if strings.Contains(got, e) {
				t.Fatalf("%s attr %s=%q contains raw entity %q", prefix, a.Key, got, e)
			}
		}
	}
}

func TestRedactionTelemetry(t *testing.T) {
	t.Run("redactedRunCarriesNoEntity", func(t *testing.T) {
		const (
			entity  = "Anna Petrova"
			other   = "Ada Lovelace"
			session = "s1"
		)
		entities := []string{entity, other}

		redactor := scenarioRedactor()
		tel := &fakeTelemetry{}
		ctx := types.WithTelemetry(types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"}), tel)
		tokenBlocks, _, err := redactor.Redact(types.WithRunInfo(ctx, types.RunInfo{SessionID: session, Principal: types.Principal{Tenant: "t1"}}), []types.Block{types.Text{Text: entity}}, scenarioPolicy())
		if err != nil {
			t.Fatal(err)
		}
		token := tokenBlocks[0].(types.Text).Text

		var buf bytes.Buffer
		out := &sinkOutputs{OutputStore: stores.NewMemoryOutputs()}
		stack := redactionTelStack(t, tel, &buf, out, token)

		conv, err := NewNativeConversation(stack, "chat",
			WithConversationRuns(stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		)
		if err != nil {
			t.Fatalf("NewNativeConversation: %v", err)
		}
		for _, sendErr := range conv.Send(ctx, session, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "Book for " + entity + " and " + other}}}) {
			if sendErr != nil {
				t.Fatalf("Send: %v", sendErr)
			}
		}

		if len(tel.spans) == 0 {
			t.Fatal("no spans recorded; assertion would be vacuous")
		}
		for _, s := range tel.spans {
			assertNoEntityInAttrs(t, "span "+s.name, s.attrs, entities...)
		}
		for _, c := range tel.counts {
			assertNoEntityInAttrs(t, "count "+c.name, c.attrs, entities...)
		}

		if buf.Len() == 0 {
			t.Fatal("no log records emitted; assertion would be vacuous")
		}
		for _, e := range entities {
			if strings.Contains(buf.String(), e) {
				t.Fatalf("entity %q reached the log:\n%s", e, buf.String())
			}
		}

		if len(out.blocks) == 0 {
			t.Fatal("no output written to the output store; pseudonym assertion would be vacuous")
		}
		found := false
		for _, blocks := range out.blocks {
			for _, b := range blocks {
				txt, ok := b.(types.Text)
				if !ok {
					continue
				}
				for _, e := range entities {
					if strings.Contains(txt.Text, e) {
						t.Fatalf("output contains raw entity %q: %q", e, txt.Text)
					}
				}
				if strings.Contains(txt.Text, token) {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("output store carries no pseudonym %q", token)
		}
	})
}
