package sandbox

import (
	"errors"

	"github.com/victorzhuk/gohan/core/types"
)

// ErrEffectLoosened is returned when construction requests a weaker
// effect than the policy derives.
var ErrEffectLoosened = errors.New("gohan: sandbox effect cannot be loosened")

// deriveEffect maps the policy onto the declared effect: a per-call
// workspace without egress only reads, a per-session workspace survives
// calls, and any egress host reaches the world outside the run.
func deriveEffect(p types.SandboxPolicy) types.Effect {
	if len(p.Egress.Allow) > 0 {
		return types.SideEffect
	}
	if p.Lifecycle == types.PerSession {
		return types.Idempotent
	}
	return types.ReadOnly
}

func effectName(e types.Effect) string {
	switch e {
	case types.Idempotent:
		return "idempotent"
	case types.SideEffect:
		return "side_effect"
	default:
		return "read_only"
	}
}
