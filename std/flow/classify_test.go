package flow

import (
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// label is the ~string label type the classify scenario uses.
type label string

// classifyCall runs one classify invocation against m and records how many
// calls the recipe spent.
func classifyCall(t *testing.T, m *scriptedModel, texts ...string) (label, error) {
	t.Helper()
	for _, x := range texts {
		m.streams = append(m.streams, []streamItem{{chunk: types.ModelChunk{Kind: types.DeltaText, Delta: x}}})
	}
	labels := []label{"refund", "billing", "other"}
	got, err := Classify(m, labels).Invoke(gohanctx(), []types.Block{types.Text{Text: "where do I upload?"}})
	return label(got), err
}

func TestFlowRecipesClassify(t *testing.T) {
	t.Run("flow.classify-recipe", func(t *testing.T) {
		m := &scriptedModel{profile: types.ModelProfile{Name: "cheap"}}
		got, err := classifyCall(t, m, `{"label":"car"}`, `{"label":"billing"}`)
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if got != "billing" {
			t.Fatalf("label = %q, want billing", got)
		}
		if m.calls != 2 {
			t.Fatalf("model calls = %d, want the one ValidateRepair retry", m.calls)
		}
		if len(m.requests[0].Options.ResponseSchema) == 0 {
			t.Error("request carried no derived schema")
		}
		if len(m.requests[0].Tools) != 0 {
			t.Errorf("tools = %v, want none offered", m.requests[0].Tools)
		}

		m2 := &scriptedModel{profile: types.ModelProfile{Name: "cheap"}}
		if _, err := classifyCall(t, m2, `{"label":"car"}`, `{"label":"car"}`); err == nil {
			t.Fatal("exhausted repair turns returned no error")
		} else {
			var me *types.ModelError
			if !errors.As(err, &me) || me.Class != types.ClassPermanent {
				t.Fatalf("err = %v, want a Permanent model error", err)
			}
		}
	})
}
