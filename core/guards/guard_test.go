package guards

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type funcGuard func(context.Context, GuardInput) (types.Decision[GuardVerdict], error)

func (f funcGuard) Decide(ctx context.Context, in GuardInput) (types.Decision[GuardVerdict], error) {
	return f(ctx, in)
}

func TestGuard(t *testing.T) {
	t.Run("decider-alias", func(t *testing.T) {
		var g Guard = funcGuard(func(ctx context.Context, in GuardInput) (types.Decision[GuardVerdict], error) {
			return types.Decision[GuardVerdict]{Value: GuardVerdict{Action: Block, Reason: "denied"}, Confidence: 1}, nil
		})
		d, err := g.Decide(context.Background(), GuardInput{Stage: types.StageInput})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if d.Value.Action != Block || d.Value.Reason != "denied" {
			t.Errorf("verdict = %+v, want Block/denied", d.Value)
		}
		if d.Confidence != 1 {
			t.Errorf("Confidence = %v, want 1", d.Confidence)
		}
	})

	t.Run("rewrite-carries-blocks", func(t *testing.T) {
		rewritten := []types.Block{types.Text{Text: "redacted"}}
		var g Guard = funcGuard(func(ctx context.Context, in GuardInput) (types.Decision[GuardVerdict], error) {
			return types.Decision[GuardVerdict]{Value: GuardVerdict{Action: Rewrite, Blocks: rewritten}}, nil
		})
		d, err := g.Decide(context.Background(), GuardInput{Stage: types.StageInput})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if d.Value.Action != Rewrite || len(d.Value.Blocks) != 1 {
			t.Errorf("verdict = %+v, want Rewrite with one block", d.Value)
		}
	})

	t.Run("fallback-message", func(t *testing.T) {
		want := types.Message{Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: "cannot help with that"}}}
		var f Fallback = func(ctx context.Context, err *types.GuardBlockedError) types.Message { return want }
		got := f(context.Background(), &types.GuardBlockedError{Stage: types.StageInput, Reason: "denied"})
		if got.Role != want.Role || len(got.Blocks) != 1 {
			t.Errorf("fallback message = %+v, want assistant message with one block", got)
		}
	})
}
