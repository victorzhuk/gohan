package guard

import (
	"context"
	"errors"
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

type failingRedactor struct{}

func (failingRedactor) Redact(context.Context, []types.Block, types.RedactionPolicy) ([]types.Block, types.RedactionMap, error) {
	return nil, types.RedactionMap{}, errors.New("detector offline")
}

func (failingRedactor) Rehydrate(context.Context, []types.Block, types.RedactionMap) ([]types.Block, error) {
	return nil, nil
}

func TestRedactionGuardErrorFailsClosed(t *testing.T) {
	g := NewRedactionGuard(failingRedactor{}, types.RedactionPolicy{Detectors: []types.Detector{nil}})
	_, err := g.Decide(context.Background(), guards.GuardInput{Stage: types.StageInput, Blocks: []types.Block{types.Text{Text: "hi"}}})
	var gbe *types.GuardBlockedError
	if !errors.As(err, &gbe) {
		t.Fatalf("err = %v, want *types.GuardBlockedError", err)
	}
	if gbe.Stage != types.StageInput {
		t.Errorf("Stage = %v, want StageInput", gbe.Stage)
	}
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
