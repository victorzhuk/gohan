package gohan

import (
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestModelBuildScenarios(t *testing.T) {
	t.Run("model.extra-collision", func(t *testing.T) {
		spec := nativeSpec("agent", "primary")
		spec.ModelOptions.Extra = map[string]any{"max_tokens": 10}
		_, err := Build(
			WithModels(fakeModel{profile: profile("primary", true)}),
			WithNativeAgent(spec),
		)
		var collision types.ErrExtraGovernedKey
		if !errors.As(err, &collision) || collision.Key != "max_tokens" {
			t.Fatalf("Build error = %v, want governed max_tokens collision", err)
		}
	})

	t.Run("model.undeclared-fidelity", func(t *testing.T) {
		spec := nativeSpec("agent", "primary")
		spec.Request.Blocks = []types.BlockKind{types.KindFile}
		_, err := Build(
			WithModels(fakeModel{profile: profile("primary", true)}),
			WithNativeAgent(spec),
		)
		if err == nil || !strings.Contains(err.Error(), string(types.KindFile)) {
			t.Fatalf("Build error = %v, want undeclared KindFile fidelity", err)
		}
	})
}
