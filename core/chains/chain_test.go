package chains

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestChain(t *testing.T) {
	t.Run("chains.empty-chains", func(t *testing.T) {
		var calls int
		raw := func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			calls++
			return types.ToolResult{ID: "raw"}, nil
		}
		if err := ValidateToolChain(nil); err != nil {
			t.Fatalf("empty chain must validate: %v", err)
		}
		res, err := RunToolChain(context.Background(), nil, raw)
		if err != nil {
			t.Fatalf("empty chain run: %v", err)
		}
		if calls != 1 {
			t.Fatalf("raw tool calls = %d, want 1", calls)
		}
		if res.ID != "raw" {
			t.Fatalf("result ID = %q, want the tool's own", res.ID)
		}
	})

	t.Run("chains.step-named-failure", func(t *testing.T) {
		ch := ToolChain{
			{Name: "gate", Kind: KindGate, Use: func(next ToolFunc) ToolFunc { return next }},
			{Name: "audit-probe", Kind: KindUser, Use: func(next ToolFunc) ToolFunc {
				return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
					panic(errors.New("probe"))
				}
			}},
		}
		_, err := RunToolChain(context.Background(), ch, func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			t.Fatal("inner tool reached past the panicking step")
			return types.ToolResult{}, nil
		})
		se, ok := errors.AsType[*StepError](err)
		if !ok {
			t.Fatalf("err = %T (%v), want *StepError", err, err)
		}
		if se.Step != "audit-probe" {
			t.Fatalf("StepError.Step = %q, want %q", se.Step, "audit-probe")
		}
		if se.Err.Error() != "probe" {
			t.Fatalf("StepError.Err = %v, want the wrapped panic", se.Err)
		}
	})

	t.Run("chains.cache-replace-is-free", func(t *testing.T) {
		cached := types.ToolResult{ID: "call-1", Content: []types.Block{types.Text{Text: "hit"}}}
		ch := ToolChain{
			{Name: "cache", Kind: KindCache, Use: func(next ToolFunc) ToolFunc {
				return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
					return cached, nil
				}
			}},
			{Name: "journal", Kind: KindJournal, Use: func(next ToolFunc) ToolFunc { return next }},
		}
		if err := ValidateToolChain(ch); err != nil {
			t.Fatalf("cache-outside chain must validate: %v", err)
		}
		var called bool
		res, err := RunToolChain(context.Background(), ch, func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			called = true
			return types.ToolResult{}, nil
		})
		if err != nil {
			t.Fatalf("cache hit run: %v", err)
		}
		if called {
			t.Fatal("inner tool ran behind a cache Replace")
		}
		if len(res.Content) != 1 || res.Content[0].(types.Text).Text != "hit" {
			t.Fatalf("result = %+v, want the cached one", res)
		}
	})

	t.Run("ordering", func(t *testing.T) {
		bad := ToolChain{
			{Name: "journal", Kind: KindJournal},
			{Name: "gate", Kind: KindGate},
		}
		if err := ValidateToolChain(bad); err == nil {
			t.Fatal("journal outside gate must not validate")
		}
		badHedge := ToolChain{
			{Name: "hedge", Kind: KindHedge},
			{Name: "router", Kind: KindRouter},
		}
		if err := ValidateToolChain(badHedge); err == nil {
			t.Fatal("hedge outside router must not validate")
		}
		badBudget := ToolChain{
			{Name: "hooks", Kind: KindHooks},
			{Name: "budget", Kind: KindBudget},
		}
		if err := ValidateToolChain(badBudget); err == nil {
			t.Fatal("hooks outside budget must not validate")
		}
		if err := ValidateToolChain(ToolChain{{Name: "cache", Kind: KindCache}}); err != nil {
			t.Fatalf("single-step chain must validate: %v", err)
		}
	})
}
