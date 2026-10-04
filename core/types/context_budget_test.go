package types

import (
	"context"
	"errors"
	"iter"
	"testing"
)

type pinnedEstimator struct {
	n int
}

func (e pinnedEstimator) Estimate(ModelRequest, Caps) int { return e.n }

type countingModel struct {
	profile ModelProfile
	counts  int
	fail    error
}

func (m *countingModel) Profile() ModelProfile { return m.profile }

func (m *countingModel) Generate(context.Context, ModelRequest) iter.Seq2[ModelChunk, error] {
	return func(func(ModelChunk, error) bool) {}
}

func (m *countingModel) Count(context.Context, ModelRequest) (int, error) {
	m.counts++
	if m.fail != nil {
		return 0, m.fail
	}
	return 190000, nil
}

type plainModel struct {
	profile ModelProfile
}

func (m plainModel) Profile() ModelProfile { return m.profile }

func (m plainModel) Generate(context.Context, ModelRequest) iter.Seq2[ModelChunk, error] {
	return func(func(ModelChunk, error) bool) {}
}

func TestContextBudget(t *testing.T) {
	t.Run("model.budget-reserves-output", func(t *testing.T) {
		p := ModelProfile{ContextWindow: 200000}
		req := ModelRequest{Options: ModelOptions{MaxTokens: 8000}}
		b := ContextBudget(p, req, pinnedEstimator{n: 190000}, 0)
		if b.Limit != 188000 {
			t.Fatalf("Limit = %d, want 188000", b.Limit)
		}
		if !b.Over() {
			t.Fatalf("estimate 190000 over Limit %d reported not over", b.Limit)
		}
	})
	t.Run("model.token-counter-optional", func(t *testing.T) {
		p := ModelProfile{ContextWindow: 200000}
		req := ModelRequest{Options: ModelOptions{MaxTokens: 8000}}
		m := &countingModel{profile: p}
		ok, err := NeedsCompaction(context.Background(), p, req, m)
		if err != nil {
			t.Fatalf("NeedsCompaction: %v", err)
		}
		if !ok {
			t.Fatal("NeedsCompaction = false, want true for a 190000-token request")
		}
		if m.counts != 1 {
			t.Fatalf("Count called %d times at the compaction decision, want 1", m.counts)
		}
		ContextBudget(p, req, nil, 0)
		if m.counts != 1 {
			t.Fatalf("budget derivation called Count %d times, want 0 on the ordinary path", m.counts-1)
		}
	})
	t.Run("margin rounds down", func(t *testing.T) {
		p := ModelProfile{ContextWindow: 200000}
		req := ModelRequest{Options: ModelOptions{MaxTokens: 8001}}
		b := ContextBudget(p, req, nil, 0)
		if b.Reserved != 12001 {
			t.Fatalf("Reserved = %d, want 12001", b.Reserved)
		}
		if b.Limit != 187999 {
			t.Fatalf("Limit = %d, want 187999", b.Limit)
		}
	})
	t.Run("absent estimator and counter", func(t *testing.T) {
		p := ModelProfile{ContextWindow: 200000}
		req := ModelRequest{Options: ModelOptions{MaxTokens: 8000}}
		b := ContextBudget(p, req, nil, 0)
		if b.Estimated != 0 || b.Over() {
			t.Fatalf("absent estimator gave Estimated %d, Over %v", b.Estimated, b.Over())
		}
		bare := plainModel{profile: p}
		ok, err := NeedsCompaction(context.Background(), p, req, bare)
		if ok || !errors.Is(err, ErrNoTokenEstimator) {
			t.Fatalf("model without TokenCounter: ok = %v, err = %v", ok, err)
		}
	})
}
