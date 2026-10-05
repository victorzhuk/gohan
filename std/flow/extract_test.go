package flow

import (
	"context"
	"errors"
	"iter"
	"testing"

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

type invoice struct {
	Total int `json:"total" bounds:"min=1"`
}

func TestFlowRecipes(t *testing.T) {
	t.Run("flow.extract-recipe", func(t *testing.T) {
		m := newTextModel("cheap", `{"total": 12}`)
		f := Extract[invoice](m)
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
		got, err := Extract[invoice](m).Invoke(gohanctx(), []types.Block{types.Text{Text: "invoice"}})
		if !errors.Is(err, types.ErrStructuredOutput) {
			t.Fatalf("err = %v, want ErrStructuredOutput", err)
		}
		if got != (invoice{}) {
			t.Fatalf("result = %+v, want the zero value refused", got)
		}
	})

	t.Run("no-tool-offered", func(t *testing.T) {
		m := newTextModel("cheap", `{"total": 3}`)
		if _, err := Extract[invoice](m).Invoke(gohanctx(), nil); err != nil {
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
}
