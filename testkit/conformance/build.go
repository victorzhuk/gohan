package conformance

import (
	"context"
	"errors"
	"iter"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
)

func BuildScenarios(t *testing.T) {
	t.Helper()
	t.Run("model.extra-collision", func(t *testing.T) {
		spec := gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "agent"},
			Profile: "primary",
			ModelOptions: types.ModelOptions{
				Extra: map[string]any{"max_tokens": 10},
			},
		}
		_, err := gohan.Build(
			gohan.WithModels(buildModel{}),
			gohan.WithNativeAgent(spec),
		)
		var collision types.ErrExtraGovernedKey
		if !errors.As(err, &collision) || collision.Key != "max_tokens" {
			t.Fatalf("Build error = %v, want governed max_tokens collision", err)
		}
	})
	t.Run("model.undeclared-fidelity", func(t *testing.T) {
		spec := gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "agent", Blocks: []types.BlockKind{types.KindFile}},
			Profile: "primary",
		}
		_, err := gohan.Build(
			gohan.WithModels(buildModel{}),
			gohan.WithNativeAgent(spec),
		)
		if err == nil || !errors.Is(err, gohan.ErrFidelityUndeclared) {
			t.Fatalf("Build error = %v, want undeclared fidelity", err)
		}
	})
}

type buildModel struct{}

func (buildModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "primary"}
}

func (buildModel) Generate(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(func(types.ModelChunk, error) bool) {}
}
