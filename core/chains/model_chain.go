package chains

import (
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// ModelFunc streams one model call.
type ModelFunc = types.ModelFunc

// ModelMiddleware wraps one model invocation.
type ModelMiddleware = types.ModelMiddleware

// ModelChain is the ordered model middleware a run composes. Index 0 is the
// outermost step.
type ModelChain []Step[ModelMiddleware]

// modelOrderingRules holds the model-side subset of the ordering rules the
// chains spec declares.
var modelOrderingRules = []orderingRule{
	{KindHedge, KindFallback, "hedge must run outside fallback"},
	{KindRouter, KindHedge, "hedge must run inside router"},
	{KindBudget, KindHooks, "budget must run outside hooks"},
}

// ValidateModelChain checks a chain against the canonical ordering
// constraints on model step kinds. An empty chain is valid: a flow built
// with it calls the raw model.
func ValidateModelChain(ch ModelChain) error {
	pos := make(map[StepKind]int, len(ch))
	for i, s := range ch {
		if prev, dup := pos[s.Kind]; dup {
			return fmt.Errorf("chains: duplicate step kind %d at %q (first at %q)", s.Kind, s.Name, ch[prev].Name)
		}
		pos[s.Kind] = i
	}
	for _, r := range modelOrderingRules {
		o, okOuter := pos[r.outer]
		n, okInner := pos[r.inner]
		if !okOuter || !okInner || o < n {
			continue
		}
		return fmt.Errorf("chains: %s: %d at %q must precede %d at %q", r.why, r.outer, ch[o].Name, r.inner, ch[n].Name)
	}
	return nil
}
