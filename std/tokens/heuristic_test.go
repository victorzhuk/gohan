package tokens

import (
	"encoding/json"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestHeuristic(t *testing.T) {
	req := types.ModelRequest{
		System: []types.Block{
			types.Text{Text: "You are a helpful assistant."},
		},
		Messages: []types.Message{
			{ID: "m1", Role: types.RoleUser, Blocks: []types.Block{
				types.Text{Text: "Hello, world."},
			}},
		},
	}
	caps := types.Caps{Tools: true}

	t.Run("model.heuristic-deterministic", func(t *testing.T) {
		first := Heuristic.Estimate(req, caps)
		second := Heuristic.Estimate(req, caps)
		if first != second {
			t.Fatalf("repeat estimate changed: %d then %d", first, second)
		}
	})

	t.Run("repeat", func(t *testing.T) {
		want := Heuristic.Estimate(req, caps)
		for range 10 {
			if got := Heuristic.Estimate(req, caps); got != want {
				t.Fatalf("estimate %d diverged from %d across repeated calls", got, want)
			}
		}
	})

	t.Run("order-independent", func(t *testing.T) {
		a := types.ModelRequest{
			Messages: []types.Message{
				{ID: "m1", Role: types.RoleUser, Blocks: []types.Block{
					types.Text{Text: "first message"},
					types.Text{Text: "second message"},
				}},
			},
			Tools: []types.ToolSpec{
				{Name: "alpha", Description: "tool one"},
				{Name: "beta", Description: "tool two"},
			},
		}
		b := a
		b.Messages[0].Blocks = []types.Block{
			types.Text{Text: "second message"},
			types.Text{Text: "first message"},
		}
		b.Tools = []types.ToolSpec{a.Tools[1], a.Tools[0]}
		if got, want := Heuristic.Estimate(b, caps), Heuristic.Estimate(a, caps); got != want {
			t.Fatalf("reordered equivalent input: %d, want %d", got, want)
		}
	})

	t.Run("monotonic", func(t *testing.T) {
		for _, unit := range []string{"a", "é", "x"} {
			prev := 0
			for n := 1; n <= 200; n++ {
				r := types.ModelRequest{Messages: []types.Message{
					{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: repeat(unit, n)}}},
				}}
				got := Heuristic.Estimate(r, caps)
				if got < prev {
					t.Fatalf("estimate dropped from %d to %d at n=%d for unit %q", prev, got, n, unit)
				}
				prev = got
			}
		}
	})

	t.Run("multibyte", func(t *testing.T) {
		ascii := types.ModelRequest{Messages: []types.Message{
			{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: repeat("a", 100)}}},
		}}
		heavy := types.ModelRequest{Messages: []types.Message{
			{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: repeat("é", 100)}}},
		}}
		if got, want := Heuristic.Estimate(heavy, caps), Heuristic.Estimate(ascii, caps); got <= want {
			t.Fatalf("multibyte-dense text estimated %d, want more than ASCII %d", got, want)
		}
	})

	t.Run("tool-schema", func(t *testing.T) {
		bare := types.ModelRequest{}
		with := types.ModelRequest{Tools: []types.ToolSpec{
			{
				Name:        "lookup",
				Description: "Find a record.",
				Schema:      json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
			},
		}}
		bareN := Heuristic.Estimate(bare, caps)
		withN := Heuristic.Estimate(with, caps)
		if withN <= bareN {
			t.Fatalf("tool schema added %d tokens, want more than %d", withN, bareN)
		}
		sameSchema := Heuristic.Estimate(types.ModelRequest{Tools: with.Tools}, caps)
		if delta := withN - bareN; sameSchema != delta {
			t.Fatalf("schema alone estimated %d, want the %d delta it added", sameSchema, delta)
		}
	})
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for range n {
		out = append(out, s...)
	}
	return string(out)
}
