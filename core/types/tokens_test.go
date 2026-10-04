package types

import (
	"context"
	"testing"
)

type fixedEstimator struct{ n int }

func (f fixedEstimator) Estimate(ModelRequest, Caps) int { return f.n }

type fixedCounter struct{ n int }

func (f fixedCounter) Count(context.Context, ModelRequest) (int, error) { return f.n, nil }

func TestTokenBudget(t *testing.T) {
	t.Run("budget-fields", func(t *testing.T) {
		b := TokenBudget{Limit: 184000, Reserved: 8000, Estimated: 12000, Ratio: 1.5}
		if b.Limit != 184000 || b.Reserved != 8000 || b.Estimated != 12000 || b.Ratio != 1.5 {
			t.Fatalf("budget = %+v, want limit 184000 reserved 8000 estimated 12000 ratio 1.5", b)
		}
	})

	t.Run("estimator-port", func(t *testing.T) {
		var est TokenEstimator = fixedEstimator{n: 42}
		if est.Estimate(ModelRequest{}, Caps{}) != 42 {
			t.Fatal("estimator did not return its fixed count")
		}
	})

	t.Run("counter-port", func(t *testing.T) {
		var counter TokenCounter = fixedCounter{}
		if _, err := counter.Count(context.Background(), ModelRequest{}); err != nil {
			t.Fatalf("Count: %v", err)
		}
	})
}
