package flow

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

// scriptedModel replays per-call chunk streams and records every request
// it received.
type scriptedModel struct {
	profile  types.ModelProfile
	streams  [][]streamItem
	calls    int
	requests []types.ModelRequest
}

type streamItem struct {
	chunk types.ModelChunk
	err   error
}

func (m *scriptedModel) Profile() types.ModelProfile { return m.profile }

func (m *scriptedModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.requests = append(m.requests, req)
	i := m.calls
	m.calls++
	if i >= len(m.streams) {
		i = len(m.streams) - 1
	}
	return func(yield func(types.ModelChunk, error) bool) {
		for _, item := range m.streams[i] {
			if item.err != nil {
				yield(types.ModelChunk{}, item.err)
				return
			}
			if !yield(item.chunk, nil) {
				return
			}
		}
	}
}

func newTextModel(profile string, texts ...string) *scriptedModel {
	m := &scriptedModel{profile: types.ModelProfile{Name: profile}}
	for _, t := range texts {
		m.streams = append(m.streams, []streamItem{{chunk: types.ModelChunk{Kind: types.DeltaText, Delta: t}}})
	}
	return m
}

// recipeStack builds a Stack over m with the caller's PromptSet, the
// documented construction surface the recipes bind to.
func recipeStack(t *testing.T, prompts chains.PromptSet, models ...*scriptedModel) *gohan.Stack {
	t.Helper()
	ms := make([]types.Model, len(models))
	for i, m := range models {
		ms[i] = m
	}
	s, err := gohan.Build(append([]gohan.Option{gohan.WithModels(ms...)}, gohan.WithPrompts(prompts))...)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return s
}

type invoice struct {
	Total int `json:"total" bounds:"min=1"`
}

func TestFlowRecipes(t *testing.T) {
	t.Run("flow.extract-recipe", func(t *testing.T) {
		m := newTextModel("cheap", `{"total": 12}`)
		f := Extract[invoice](recipeStack(t, chains.PromptSet{}, m), "cheap")
		got, err := f.Invoke(gohanctx(), []types.Block{types.Text{Text: "invoice for June"}})
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if got.Total != 12 {
			t.Fatalf("result = %+v, want total 12", got)
		}
		if len(m.requests) != 1 {
			t.Fatalf("model calls = %d, want 1", len(m.requests))
		}
		req := m.requests[0]
		if len(req.Options.ResponseSchema) == 0 {
			t.Error("request carried no derived schema")
		}
		if len(req.Tools) != 0 {
			t.Errorf("tools = %v, want none offered", req.Tools)
		}
	})

	t.Run("validation-failure-refused", func(t *testing.T) {
		m := newTextModel("cheap", `{"total": 0}`)
		f := Extract[invoice](recipeStack(t, chains.PromptSet{}, m), "cheap")
		got, err := f.Invoke(gohanctx(), []types.Block{types.Text{Text: "invoice"}})
		if !errors.Is(err, types.ErrStructuredOutput) {
			t.Fatalf("err = %v, want ErrStructuredOutput", err)
		}
		if got != (invoice{}) {
			t.Fatalf("result = %+v, want the zero value refused", got)
		}
	})

	t.Run("no-tool-offered", func(t *testing.T) {
		m := newTextModel("cheap", `{"total": 3}`)
		s := recipeStack(t, chains.PromptSet{}, m)
		if _, err := Extract[invoice](s, "cheap").Invoke(gohanctx(), nil); err != nil {
			t.Fatalf("extract Invoke: %v", err)
		}
		m2 := &scriptedModel{profile: types.ModelProfile{Name: "cheap"}}
		if _, err := classifyCall(t, m2, `{"label":"refund"}`); err != nil {
			t.Fatalf("classify Invoke: %v", err)
		}
		for i, req := range append(append([]types.ModelRequest{}, m.requests...), m2.requests...) {
			if len(req.Tools) != 0 {
				t.Errorf("call %d offered %d tools, want none", i, len(req.Tools))
			}
		}
	})

	t.Run("repair-uses-caller-instruction", func(t *testing.T) {
		for _, tc := range []struct{ instruction string }{
			{"Fix the invoice fields and answer again."},
			{"Rebuild the invoice against the reported problems."},
		} {
			m := newTextModel("cheap", `{"total": 0}`, `{"total": 7}`)
			prompts := chains.PromptSet{RepairInstruction: tc.instruction, Version: "1"}
			f := Extract[invoice](recipeStack(t, prompts, m), "cheap")
			got, err := f.Invoke(gohanctx(), []types.Block{types.Text{Text: "invoice"}})
			if err != nil || got.Total != 7 {
				t.Fatalf("Invoke = (%+v, %v), want total 7", got, err)
			}
			if m.calls != 2 {
				t.Fatalf("model calls = %d, want the one repair turn", m.calls)
			}
			repairMsg := m.requests[1].Messages[len(m.requests[1].Messages)-1]
			if repairMsg.Role != types.RoleUser || len(repairMsg.Blocks) != 1 {
				t.Fatalf("repair turn = %+v, want one user block", repairMsg)
			}
			if got := repairMsg.Blocks[0].(types.Text).Text; got != tc.instruction {
				t.Errorf("repair text = %q, want the caller's RepairInstruction %q", got, tc.instruction)
			}
		}
	})

	t.Run("empty-prompt-set-adds-no-prompt-text", func(t *testing.T) {
		m := newTextModel("cheap", `{"total": 0}`, `{"total": 5}`)
		f := Extract[invoice](recipeStack(t, chains.PromptSet{}, m), "cheap")
		if _, err := f.Invoke(gohanctx(), []types.Block{types.Text{Text: "invoice"}}); err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		for i, req := range m.requests {
			if len(req.System) != 0 {
				t.Errorf("call %d carried %d system blocks, want none", i, len(req.System))
			}
		}
		repairReq := m.requests[1]
		if n := len(repairReq.Messages); n != 2 {
			t.Fatalf("repair turn messages = %d, want only the answer and its predecessor", n)
		}
		if repairReq.Messages[1].Role != types.RoleAssistant {
			t.Errorf("second message role = %s, want the assistant turn", repairReq.Messages[1].Role)
		}
	})

	t.Run("unknown-profile-refused-before-call", func(t *testing.T) {
		m := newTextModel("cheap", `{"total": 1}`)
		f := Extract[invoice](recipeStack(t, chains.PromptSet{}, m), "absent")
		_, err := f.Invoke(gohanctx(), nil)
		if err == nil || !strings.Contains(err.Error(), "absent") {
			t.Fatalf("err = %v, want the unknown profile named", err)
		}
		if m.calls != 0 {
			t.Fatalf("model calls = %d, want 0 before the provider", m.calls)
		}
	})
}
