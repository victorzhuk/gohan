package gohan

import (
	"context"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/guard"
	"github.com/victorzhuk/gohan/std/redact"
)

type scenarioDetector struct{}

func (scenarioDetector) Detect(_ context.Context, blocks []types.Block) ([]types.Span, error) {
	var spans []types.Span
	for i, block := range blocks {
		text, ok := block.(types.Text)
		if !ok {
			continue
		}
		for _, entity := range []string{"Ada Lovelace", "Anna Petrova"} {
			start := strings.Index(text.Text, entity)
			if start >= 0 {
				spans = append(spans, types.Span{Block: i, Start: start, End: start + len(entity), Kind: types.EntityPerson})
			}
		}
	}
	return spans, nil
}

func scenarioRedactor() *redact.Redactor {
	return redact.New(redact.KeyFunc(func(context.Context, string) ([]byte, error) { return []byte("fixed-key"), nil }))
}

func scenarioPolicy() types.RedactionPolicy {
	return types.RedactionPolicy{Detectors: []types.Detector{scenarioDetector{}}}
}

func TestRedactionScenarios(t *testing.T) {
	t.Run("redaction.pseudonym-stable-within-session", func(t *testing.T) {
		redactor := scenarioRedactor()
		ctx := types.WithRunInfo(context.Background(), types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1"}})
		store := stores.NewMemoryRedactionMaps()
		assemble := nativeAssemble("chat", chains.PromptSet{}, func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{Messages: append([]types.Message(nil), in.Input...)}, nil
		}, redactor, scenarioPolicy(), store, blobAssembly{})
		var token string
		for _, turn := range []string{"Anna Petrova", "other", "other", "Anna Petrova"} {
			req, err := assemble(ctx, types.AssembleInput{Run: types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1"}}, Input: []types.Message{{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: turn}}}}})
			if err != nil {
				t.Fatal(err)
			}
			got := req.Messages[0].Blocks[0].(types.Text).Text
			if turn == "Anna Petrova" {
				if token != "" && token != got {
					t.Fatalf("token changed: %q != %q", token, got)
				}
				token = got
				if strings.Contains(got, turn) {
					t.Fatalf("model received raw entity: %q", got)
				}
			}
		}
		history, err := store.Get(ctx, "t1", "s1")
		if err != nil {
			t.Fatal(err)
		}
		if history.Entries[token].Value != "Anna Petrova" {
			t.Fatalf("map entry = %+v", history.Entries[token])
		}
	})

	t.Run("redaction.streaming-rehydration", func(t *testing.T) {
		redactor := scenarioRedactor()
		ctx := types.WithRunInfo(context.Background(), types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1"}})
		blocks, mapping, err := redactor.Redact(ctx, []types.Block{types.Text{Text: "Anna Petrova"}}, scenarioPolicy())
		if err != nil {
			t.Fatal(err)
		}
		token := blocks[0].(types.Text).Text
		chunks := make(chan types.Text, 2)
		chunks <- types.Text{Text: token[:len(token)/2]}
		chunks <- types.Text{Text: token[len(token)/2:]}
		close(chunks)
		w := guard.NewWindowed(nil, 1, nil)
		w.Redactor, w.Map = redactor, mapping
		var got strings.Builder
		if err := w.GuardStream(ctx, chunks, func(out types.Text) error { got.WriteString(out.Text); return nil }); err != nil {
			t.Fatal(err)
		}
		if got.String() != "Anna Petrova" {
			t.Fatalf("client output = %q", got.String())
		}
	})

	t.Run("redaction.hallucinated-token-dropped", func(t *testing.T) {
		counter := &scenarioCounter{}
		w := guard.NewWindowed(nil, 1, nil)
		w.Redactor, w.Counter = scenarioRedactor(), counter
		chunks := make(chan types.Text, 1)
		chunks <- types.Text{Text: "<gohan:invented>"}
		close(chunks)
		var got strings.Builder
		if err := w.GuardStream(context.Background(), chunks, func(out types.Text) error { got.WriteString(out.Text); return nil }); err != nil {
			t.Fatal(err)
		}
		if got.String() != "" || counter.n != 1 {
			t.Fatalf("output=%q unknown count=%d", got.String(), counter.n)
		}
	})

	t.Run("guards.redaction", func(t *testing.T) {
		redactor := scenarioRedactor()
		input := []types.Block{types.Text{Text: "Book for Ada Lovelace"}}
		decision, err := guard.NewRedactionGuard(redactor, scenarioPolicy()).Decide(context.Background(), guards.GuardInput{Stage: types.StageInput, Blocks: input})
		if err != nil {
			t.Fatal(err)
		}
		if decision.Value.Action != guards.Rewrite {
			t.Fatalf("action = %v", decision.Value.Action)
		}
		got := decision.Value.Blocks[0].(types.Text).Text
		if strings.Contains(got, "Ada Lovelace") {
			t.Fatalf("rewritten input contains entity: %q", got)
		}
	})
}

type scenarioCounter struct{ n int64 }

func (c *scenarioCounter) Add(_ context.Context, name string, value int64) {
	if name == "gohan.redact.unknown_token" {
		c.n += value
	}
}
