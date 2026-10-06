package gohan

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type sinkOutputs struct {
	stores.OutputStore
	refs   []string
	blocks [][]types.Block
}

func (o *sinkOutputs) Put(ctx context.Context, ri types.RunInfo, content []types.Block) (string, error) {
	ref, err := o.OutputStore.Put(ctx, ri, content)
	if err != nil {
		return "", err
	}
	o.refs = append(o.refs, ref)
	o.blocks = append(o.blocks, content)
	return ref, nil
}

func redactionSinkStack(t *testing.T, out stores.OutputStore, opts ...Option) (*Stack, error) {
	t.Helper()
	redactor := scenarioRedactor()
	all := append([]Option{
		WithStores(stores.Stores{
			SessionLog:    stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:          stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom)),
			Checkpoints:   stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
			RedactionMaps: stores.NewMemoryRedactionMaps(),
			OutputStore:   out,
		}),
		WithModels(fakeModel{profile: profile("primary", true)}),
		WithRedactor(redactor),
		WithRedactionPolicy(scenarioPolicy()),
		WithNativeAgent(NativeSpec{Request: FlowRequest{Name: "chat"}, Profile: "primary"}),
	}, opts...)
	return Build(all...)
}

func TestNativeConversationCarriesRedactionConfig(t *testing.T) {
	t.Run("nativeConstructionWired", func(t *testing.T) {
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))
		stack, err := redactionSinkStack(t, stores.NewMemoryOutputs())
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
		conv, err := NewNativeConversation(stack, "chat", WithConversationRuns(runs), WithConversationEventLog(events))
		if err != nil {
			t.Fatalf("NewNativeConversation: %v", err)
		}
		c, ok := conv.(*conversation)
		if !ok {
			t.Fatal("native conversation is not *conversation")
		}
		if c.redactor == nil || len(c.redactionPolicy.Detectors) == 0 {
			t.Fatal("native conversation carries no redactor or policy")
		}
		if c.redactionMaps == nil || c.outputs == nil {
			t.Fatal("native conversation carries no redaction map store or output store")
		}
	})
}

func TestOutputStoreSinkRedacts(t *testing.T) {
	t.Run("outputStoreStoresTokens", func(t *testing.T) {
		redactor := scenarioRedactor()
		ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
		tokenBlocks, _, err := redactor.Redact(types.WithRunInfo(ctx, types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1"}}), []types.Block{types.Text{Text: "Anna Petrova"}}, scenarioPolicy())
		if err != nil {
			t.Fatal(err)
		}
		token := tokenBlocks[0].(types.Text).Text

		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))
		cps := stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))
		out := &sinkOutputs{OutputStore: stores.NewMemoryOutputs()}
		stack, err := redactionSinkStack(t, out)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		conv, err := NewConversation(stack, "chat", &sinkRuntime{token: token},
			WithConversationRuns(runs),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
			WithConversationCheckpoints(cps),
		)
		if err != nil {
			t.Fatalf("NewConversation: %v", err)
		}
		for _, sendErr := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "Book for Anna Petrova"}}}) {
			if sendErr != nil {
				t.Fatalf("Send: %v", sendErr)
			}
		}
		if len(out.blocks) == 0 {
			t.Fatal("no output written to the output store")
		}
		for i, blocks := range out.blocks {
			for _, b := range blocks {
				if txt, ok := b.(types.Text); ok && strings.Contains(txt.Text, "Anna Petrova") {
					t.Fatalf("output %d contains the raw entity: %q", i, txt.Text)
				}
			}
		}
		found := false
		for _, blocks := range out.blocks {
			for _, b := range blocks {
				if txt, ok := b.(types.Text); ok && strings.Contains(txt.Text, token) {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("output store holds no pseudonym %q", token)
		}
	})
}
