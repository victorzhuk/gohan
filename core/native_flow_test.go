package gohan

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

type recipeModel struct{ requests []types.ModelRequest }

func (m *recipeModel) Profile() types.ModelProfile { return types.ModelProfile{Name: "cheap"} }

func (m *recipeModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.requests = append(m.requests, req)
	return func(yield func(types.ModelChunk, error) bool) {
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: `{}`}, nil)
	}
}

func TestRecipePath(t *testing.T) {
	t.Run("prompts accessor returns the resolved set", func(t *testing.T) {
		want := chains.PromptSet{RepairInstruction: "repair now", Version: "3"}
		s, err := Build(WithPrompts(want))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if got := s.Prompts(); got != want {
			t.Fatalf("Prompts = %+v, want %+v", got, want)
		}
	})

	t.Run("require prompts names the consumer and the field", func(t *testing.T) {
		err := RequirePrompts(chains.PromptSet{Version: "1"}, "flow.extract", "RepairInstruction")
		if err == nil {
			t.Fatal("empty required field returned no error")
		}
		if !strings.Contains(err.Error(), "flow.extract") || !strings.Contains(err.Error(), "RepairInstruction") {
			t.Fatalf("err = %v, want the consumer and the field named", err)
		}
		if err := RequirePrompts(chains.PromptSet{RepairInstruction: "x"}, "flow.extract", "RepairInstruction"); err != nil {
			t.Fatalf("filled field refused: %v", err)
		}
	})

	t.Run("recipe model composes the stack middleware", func(t *testing.T) {
		m := &recipeModel{}
		mw := func(next types.ModelFunc) types.ModelFunc {
			return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
				req.System = append(req.System, types.Text{Text: "middleware-marker"})
				return next(ctx, req)
			}
		}
		s, err := Build(WithModels(m), WithModelMiddleware(mw))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		rc, err := s.RecipeModel("cheap")
		if err != nil {
			t.Fatalf("RecipeModel: %v", err)
		}
		if rc.Provider != "cheap" {
			t.Fatalf("Provider = %q, want cheap", rc.Provider)
		}
		for _, err := range rc.Call(context.Background(), types.ModelRequest{}) {
			if err != nil {
				t.Fatalf("call: %v", err)
			}
		}
		if len(m.requests) != 1 {
			t.Fatalf("model calls = %d, want 1", len(m.requests))
		}
		if len(m.requests[0].System) != 1 || m.requests[0].System[0].(types.Text).Text != "middleware-marker" {
			t.Fatalf("model saw system = %v, want the middleware block", m.requests[0].System)
		}
	})

	t.Run("unknown profile is refused before a call", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if _, err := s.RecipeModel("absent"); err == nil || !strings.Contains(err.Error(), "absent") {
			t.Fatalf("err = %v, want the unknown profile named", err)
		}
	})
}
