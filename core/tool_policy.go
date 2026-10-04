package gohan

import (
	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/types"
)

// ToolPolicy is the wiring-time trust policy for one imported tool set.
// Tools registered from own code default to Trusted; tools imported from
// an external source default to Untrusted, whose MaxEffect floor of
// ReadOnly caps their declared effect unless the wiring raises it.
type ToolPolicy struct {
	Trust         types.Trust
	MaxEffect     types.Effect
	DescribeGuard guards.Guard
}

// ApplyToolPolicy caps the spec's declared effect before gate evaluation:
// an Untrusted tool whose declared effect exceeds MaxEffect is registered
// at MaxEffect. The boolean reports whether the cap was applied, so the
// caller can count it. The gate then sees the capped effect.
func ApplyToolPolicy(spec types.ToolSpec, policy ToolPolicy) (types.ToolSpec, bool) {
	if policy.Trust != types.Untrusted || spec.Effect <= policy.MaxEffect {
		return spec, false
	}
	spec.Effect = policy.MaxEffect
	return spec, true
}
