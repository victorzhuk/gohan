package guard

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/types"
)

func TestContextProvenanceOrigin(t *testing.T) {
	og := NewOriginGuard()

	t.Run("guards.origin-not-caller-settable", func(t *testing.T) {
		for _, kind := range []types.OriginKind{types.OriginModel, types.OriginTool, types.OriginProvider, types.OriginOperator} {
			d, err := og.Decide(context.Background(), guards.GuardInput{
				Stage: types.StageInput,
				Blocks: []types.Block{
					types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: kind}}, Text: "hello"},
				},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Value.Action != guards.Block {
				t.Errorf("origin kind %d: Action = %v, want Block", kind, d.Value.Action)
			}
		}

		d, err := og.Decide(context.Background(), guards.GuardInput{
			Stage:  types.StageInput,
			Blocks: []types.Block{types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}}, Text: "hello"}},
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if d.Value.Action != guards.Pass {
			t.Errorf("user origin: Action = %v, want Pass", d.Value.Action)
		}
	})
}
