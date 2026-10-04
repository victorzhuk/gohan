package types

import (
	"context"
	"testing"

	"encoding/json/jsontext"
)

// rulesDecider stands in for a rules-based implementation: deterministic,
// no model call, always fully confident.
type rulesDecider struct{}

func (rulesDecider) Decide(_ context.Context, state string) (Decision[bool], error) {
	return Decision[bool]{Value: state == "allow", Confidence: 1}, nil
}

// llmDecider stands in for an LLM implementation: raw model output, validated
// against the decision schema before it may become a Decision.
type llmDecider struct {
	output jsontext.Value
}

func (d llmDecider) Decide(_ context.Context, _ string) (Decision[jsontext.Value], error) {
	if err := ValidateToolArgs(d.output); err != nil {
		return Decision[jsontext.Value]{}, err
	}
	return Decision[jsontext.Value]{Value: d.output, Confidence: 0.7}, nil
}

func TestDeciderContract(t *testing.T) {
	t.Run("decider.rules-confidence-one", func(t *testing.T) {
		d, err := rulesDecider{}.Decide(context.Background(), "allow")
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if d.Confidence != 1 {
			t.Errorf("Confidence = %v, want 1", d.Confidence)
		}
		if !d.Value {
			t.Errorf("Value = false, want true")
		}
	})

	t.Run("decider.schema-validated", func(t *testing.T) {
		d, err := llmDecider{output: jsontext.Value(`[1,2]`)}.Decide(context.Background(), "s")
		if err == nil {
			t.Fatal("Decide with schema-mismatched output: want error, got nil")
		}
		if d.Value != nil || d.Confidence != 0 {
			t.Errorf("Decision = %+v, want zero value on error", d)
		}
	})
}
