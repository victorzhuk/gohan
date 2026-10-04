package guard

import (
	"context"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/types"
)

// contextRules reject instruction-like content in notes and provider
// output. They are stricter than InjectionRules because the source is
// untrusted by construction: a note or a provider span can carry text
// planted by a tool result.
var contextRules = append(InjectionRules(), []Rule{
	{
		Name:     "context-directive",
		Contains: []string{"you must", "never reveal", "do not tell the user", "system prompt"},
		Action:   guards.Block,
	},
	{
		Name:     "context-exfiltration",
		Contains: []string{"email the export", "send the export", "forward the conversation"},
		Action:   guards.Block,
	},
}...)

// userRules apply to OriginUser and OriginSystem context, whose source the
// caller already stands behind; only the injection patterns run.
var userRules = InjectionRules()

// ContextGuard rejects imperative or instruction-like context. Content
// carrying a tool-result or provider origin meets the full context rule
// set; user and system origin content meets only the injection rules.
type ContextGuard struct {
	strict *Rules
	user   *Rules
}

// NewContextGuard builds the default context guard.
func NewContextGuard() *ContextGuard {
	return &ContextGuard{
		strict: NewRules("context", contextRules...),
		user:   NewRules("context-user", userRules...),
	}
}

// Name reports the decider name recorded by decision observability.
func (g *ContextGuard) Name() string { return "context" }

// Decide implements guards.Guard.
func (g *ContextGuard) Decide(ctx context.Context, in guards.GuardInput) (types.Decision[guards.GuardVerdict], error) {
	if strictContext(in) {
		return g.strict.Decide(ctx, in)
	}
	return g.user.Decide(ctx, in)
}

func strictContext(in guards.GuardInput) bool {
	switch in.Origin.Kind {
	case types.OriginTool, types.OriginProvider, types.OriginModel, types.OriginOperator:
		return true
	}
	for _, b := range in.Blocks {
		switch b.BlockOrigin().Kind {
		case types.OriginTool, types.OriginProvider, types.OriginModel, types.OriginOperator:
			return true
		}
	}
	return false
}
