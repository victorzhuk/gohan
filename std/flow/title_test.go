package flow

import (
	"encoding/json"
	"strings"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

func assertTitleFlow(_ gohan.Flow[[]types.Block, string]) {}

func TestTitleRecipe(t *testing.T) {
	m := newConstrainedTextModel("cheap", `"Refund Policy Questions"`)
	s := recipeStack(t, chains.PromptSet{}, m)
	f := Title(s, "cheap")
	assertTitleFlow(f)
	got, err := f.Invoke(gohanctx(), []types.Block{types.Text{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}},
		Text:      "how do refunds work?",
	}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got != "Refund Policy Questions" {
		t.Fatalf("title = %q, want the decoded string", got)
	}
	if len(m.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(m.requests))
	}
	req := m.requests[0]
	if len(req.Tools) != 0 {
		t.Errorf("tools = %v, want none offered", req.Tools)
	}
	if !json.Valid(req.Options.ResponseSchema) {
		t.Errorf("request schema = %q, want a valid response schema", req.Options.ResponseSchema)
	}
	var instruction string
	var systemBlocks int
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			txt, ok := b.(types.Text)
			if !ok || txt.BlockOrigin().Kind != types.OriginSystem {
				continue
			}
			systemBlocks++
			instruction += " " + txt.Text
		}
	}
	if systemBlocks == 0 {
		t.Fatalf("assembled request carries no system instruction, want the title instruction")
	}
	if !strings.Contains(instruction, "six words") {
		t.Fatalf("title instruction %q, want it to ask for at most six words", instruction)
	}

	m2 := newConstrainedTextModel("cheap", `"one two three four five six seven eight"`)
	got, err = Title(recipeStack(t, chains.PromptSet{}, m2), "cheap").Invoke(gohanctx(), []types.Block{
		types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}}, Text: "long"},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got != "one two three four five six" {
		t.Fatalf("title = %q, want the first six words", got)
	}
}
