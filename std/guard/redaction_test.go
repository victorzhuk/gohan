package guard

import (
	"context"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/types"
)

type redactionFixture struct{}

func (redactionFixture) Redact(_ context.Context, blocks []types.Block, _ types.RedactionPolicy) ([]types.Block, types.RedactionMap, error) {
	text := blocks[0].(types.Text)
	text.Text = strings.ReplaceAll(text.Text, "Ada Lovelace", "[person]")
	return []types.Block{text}, types.RedactionMap{}, nil
}

func (redactionFixture) Rehydrate(context.Context, []types.Block, types.RedactionMap) ([]types.Block, error) {
	return nil, nil
}

func TestRedactionGuardRewritesInput(t *testing.T) {
	guard := NewRedactionGuard(redactionFixture{}, types.RedactionPolicy{Detectors: []types.Detector{nil}})
	decision, err := guard.Decide(context.Background(), guards.GuardInput{Stage: types.StageInput, Blocks: []types.Block{types.Text{Text: "Ask Ada Lovelace"}}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Value.Action != guards.Rewrite {
		t.Fatalf("action = %v, want Rewrite", decision.Value.Action)
	}
	got := decision.Value.Blocks[0].(types.Text).Text
	if strings.Contains(got, "Ada Lovelace") || !strings.Contains(got, "[person]") {
		t.Fatalf("rewritten text = %q", got)
	}
}
