package std

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/hedge"
	"github.com/victorzhuk/gohan/std/route"
	"github.com/victorzhuk/gohan/std/telemetry"
)

type chainModel struct{ name string }

func (m chainModel) Profile() types.ModelProfile { return types.ModelProfile{Name: m.name} }

func (chainModel) Generate(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return nil
}

func TestModelChainHedge(t *testing.T) {
	t.Run("chains.hedge-never-same-endpoint", func(t *testing.T) {
		r, err := route.NewRouter([]types.Model{chainModel{"only"}})
		if err != nil {
			t.Fatal(err)
		}
		ch := ModelChain(r)
		if err := chains.ValidateModelChain(ch); err != nil {
			t.Fatalf("single-target chain invalid: %v", err)
		}
		if ch[0].Kind != chains.KindRouter || ch[2].Kind != chains.KindFallback {
			t.Fatalf("hedge not between router and fallback: %v", ch)
		}
		if ch[1].Name != "hedge (inert: single target)" {
			t.Fatalf("hedge step name = %q, want the inert note", ch[1].Name)
		}
	})
	t.Run("multi-target-step-live", func(t *testing.T) {
		r, err := route.NewRouter([]types.Model{chainModel{"a"}, chainModel{"b"}})
		if err != nil {
			t.Fatal(err)
		}
		ch := ModelChain(r)
		if err := chains.ValidateModelChain(ch); err != nil {
			t.Fatalf("multi-target chain invalid: %v", err)
		}
		if ch[1].Name != "hedge" || ch[1].Use == nil {
			t.Fatalf("hedge step = %q use %v, want a live hedge", ch[1].Name, ch[1].Use != nil)
		}
	})
	t.Run("latency-window-wired", func(t *testing.T) {
		r, err := route.NewRouter([]types.Model{chainModel{"a"}, chainModel{"b"}})
		if err != nil {
			t.Fatal(err)
		}
		w := telemetry.NewLatencyWindow(0)
		ch := ModelChain(r, WithLatencyWindow(w))
		if err := chains.ValidateModelChain(ch); err != nil {
			t.Fatal(err)
		}
		w.Observe("b", 250*time.Millisecond)
		if d, ok := w.P90("b"); !ok || d != 250*time.Millisecond {
			t.Fatalf("p90 = %v ok %v, want 250ms", d, ok)
		}
	})
	t.Run("policy-option-overrides-targets", func(t *testing.T) {
		r, err := route.NewRouter([]types.Model{chainModel{"a"}, chainModel{"b"}})
		if err != nil {
			t.Fatal(err)
		}
		called := false
		ch := ModelChain(r, WithHedgePolicy(hedge.HedgePolicy{
			After:   50 * time.Millisecond,
			Targets: func() []types.Model { called = true; return r.Eligible() },
		}))
		if err := chains.ValidateModelChain(ch); err != nil {
			t.Fatal(err)
		}
		if ch[1].Use == nil {
			t.Fatal("policy option produced no hedge middleware")
		}
		_ = called
	})
}
