package flow

import (
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

func TestQuarantine(t *testing.T) {
	t.Run("reports-input-block-origins", func(t *testing.T) {
		m := newConstrainedTextModel("cheap", `{"total": 12}`)
		f := Quarantine[invoice](recipeStack(t, chains.PromptSet{}, m), "cheap")
		got, err := f.Invoke(gohanctx(), []types.Block{
			types.Text{Text: "invoice for June"},
			types.ToolResult{ID: "t1", Outcome: types.Succeeded},
			types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser, Name: "zhuk"}}, Text: "attach"},
		})
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if got.Value.Total != 12 {
			t.Fatalf("value = %+v, want total 12", got.Value)
		}
		want := []types.Origin{
			{Kind: types.OriginSystem, Name: ""},
			{Kind: types.OriginUser, Name: "zhuk"},
		}
		if len(got.Origins) != len(want) {
			t.Fatalf("origins = %+v, want %+v", got.Origins, want)
		}
		for i, o := range want {
			if got.Origins[i] != o {
				t.Errorf("origin %d = %+v, want %+v", i, got.Origins[i], o)
			}
		}
		if len(m.requests) != 1 || len(m.requests[0].Tools) != 0 {
			t.Fatalf("calls = %d, tools = %v, want one call with no tool", m.calls, m.requests[0].Tools)
		}
	})

	t.Run("validation-failure-refused", func(t *testing.T) {
		m := newTextModel("cheap", `{"total": 0}`)
		f := Quarantine[invoice](recipeStack(t, chains.PromptSet{}, m), "cheap")
		got, err := f.Invoke(gohanctx(), []types.Block{types.Text{Text: "invoice"}})
		if !errors.Is(err, types.ErrStructuredOutput) {
			t.Fatalf("err = %v, want ErrStructuredOutput", err)
		}
		if got.Value != (invoice{}) {
			t.Fatalf("value = %+v, want the zero value refused", got.Value)
		}
	})

	t.Run("no-repair-only-success", func(t *testing.T) {
		m := newTextModel("cheap", `{"total": 0}`, `{"total": 0}`)
		f := Quarantine[invoice](recipeStack(t, chains.PromptSet{}, m), "cheap")
		if _, err := f.Invoke(gohanctx(), []types.Block{types.Text{Text: "invoice"}}); err == nil {
			t.Fatal("Invoke err = nil, want refusal after the repair turns are spent")
		}
		if m.calls != 2 {
			t.Fatalf("model calls = %d, want the bounded attempts", m.calls)
		}
	})
}
