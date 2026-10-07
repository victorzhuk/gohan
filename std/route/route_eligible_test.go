package route

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

var errEligible = errors.New("transient")

type eligibleModel struct{ name string }

func (m eligibleModel) Profile() types.ModelProfile { return types.ModelProfile{Name: m.name} }

func (eligibleModel) Generate(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return nil
}

func TestRouterEligible(t *testing.T) {
	r, err := NewRouter([]types.Model{eligibleModel{"a"}, eligibleModel{"b"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("closed-breakers-admit", func(t *testing.T) {
		if got := len(r.Eligible()); got != 2 {
			t.Fatalf("eligible = %d, want 2", got)
		}
	})
	t.Run("open-breaker-excluded", func(t *testing.T) {
		for range defaultOpenThreshold {
			r.RecordFailure("a", &types.ModelError{Class: types.ClassTransient, Err: errEligible})
		}
		got := r.Eligible()
		if len(got) != 1 || got[0].Profile().Name != "b" {
			t.Fatalf("eligible = %d, want only b", len(got))
		}
	})
	t.Run("half-open-probe-not-consumed", func(t *testing.T) {
		br := r.Breaker("a")
		br.window, br.halfOpenDelay = 0, 0
		time.Sleep(time.Millisecond)
		if br.State() != HalfOpen {
			t.Fatalf("state = %v, want half-open", br.State())
		}
		if got := r.Eligible(); len(got) != 1 {
			t.Fatalf("eligible = %d, want only b: half-open must not be free admission", len(got))
		}
		if br.probing {
			t.Fatal("Eligible consumed the half-open probe")
		}
		if got := r.Candidates(); len(got) != 2 {
			t.Fatalf("candidates = %d, want both: Candidates consumes the probe", len(got))
		}
		if !br.probing {
			t.Fatal("Candidates did not consume the half-open probe")
		}
	})
}
