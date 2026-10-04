// Package chains defines agent chains as data: ordered, named steps the
// runtime composes over a model or tool call. Core ships no steps.
package chains

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// ToolFunc invokes one tool and streams nothing back: one result, one error.
type ToolFunc func(ctx context.Context, call types.ToolUse) (types.ToolResult, error)

// ToolMiddleware wraps one tool invocation.
type ToolMiddleware func(next ToolFunc) ToolFunc

// StepKind classifies a step so ordering constraints can be validated
// without executing the chain.
type StepKind int

const (
	KindGuard StepKind = iota
	KindGate
	KindJournal
	KindLimit
	KindRetry
	KindFallback
	KindHedge
	KindTelemetry
	KindRedact
	KindCache
	KindUser
	KindHooks
	KindRouter
	KindBudget
)

// Step is one named position in a chain. Applies lets the step skip tools
// whose spec it does not govern; a nil Applies applies to every tool.
type Step[M any] struct {
	Name    string
	Kind    StepKind
	Applies func(types.ToolSpec) bool
	Use     M
}

// ToolChain is the ordered tool middleware a run composes. Index 0 is the
// outermost step.
type ToolChain []Step[ToolMiddleware]

// StepError names the step a panic or error escaped from.
type StepError = types.StepError

// orderingRule states that outer must precede inner, because the inner step
// runs inside the outer one.
type orderingRule struct {
	outer, inner StepKind
	why          string
}

var orderingRules = []orderingRule{
	{KindGate, KindJournal, "journal must run inside gate"},
	{KindFallback, KindRetry, "retry must run inside fallback"},
	{KindFallback, KindHedge, "hedge must run outside fallback"},
	{KindRouter, KindHedge, "hedge must run inside router"},
	{KindBudget, KindHooks, "budget must run outside hooks"},
}

// ValidateToolChain checks a chain against the canonical ordering
// constraints on step kinds. An empty chain is valid: a flow built with it
// calls the raw tools.
func ValidateToolChain(ch ToolChain) error {
	pos := make(map[StepKind]int, len(ch))
	for i, s := range ch {
		if prev, dup := pos[s.Kind]; dup {
			return fmt.Errorf("chains: duplicate step kind %d at %q (first at %q)", s.Kind, s.Name, ch[prev].Name)
		}
		pos[s.Kind] = i
	}
	for _, r := range orderingRules {
		o, okOuter := pos[r.outer]
		n, okInner := pos[r.inner]
		if !okOuter || !okInner || o < n {
			continue
		}
		return fmt.Errorf("chains: %s: %d at %q must precede %d at %q", r.why, r.outer, ch[o].Name, r.inner, ch[n].Name)
	}
	return nil
}

// RunToolChain composes the chain over fn so that step 0 is outermost and
// fn is the innermost call, then executes it. A nil Use passes the call
// through. A panic inside a step is recovered as a StepError naming it.
func RunToolChain(ctx context.Context, ch ToolChain, fn ToolFunc) (types.ToolResult, error) {
	next := fn
	for i := len(ch) - 1; i >= 0; i-- {
		step := ch[i]
		use := step.Use
		inner := next
		next = func(ctx context.Context, call types.ToolUse) (res types.ToolResult, err error) {
			defer func() {
				r := recover()
				if r == nil {
					return
				}
				if e, isErr := r.(error); isErr {
					err = &StepError{Step: step.Name, Err: e}
					return
				}
				err = &StepError{Step: step.Name, Err: fmt.Errorf("%v", r)}
			}()
			if use == nil {
				return inner(ctx, call)
			}
			return use(inner)(ctx, call)
		}
	}
	return next(ctx, types.ToolUse{})
}
