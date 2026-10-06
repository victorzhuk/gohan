package conformance

import (
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestGovernedKeyProblemNamesCollision(t *testing.T) {
	problem := types.ProblemOf(types.ErrExtraGovernedKey{Key: "max_tokens"})
	if problem.Code != types.CodeConfiguration || problem.Fields["key"] != "max_tokens" {
		t.Fatalf("Problem = %#v, want configuration problem naming max_tokens", problem)
	}
}
func TestModelBuildScenarios(t *testing.T) {
	BuildScenarios(t)
}
