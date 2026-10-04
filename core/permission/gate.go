// Package permission gates tool calls: the scope check runs first and
// denies hard, an optional decider refines the verdict, and every Ask
// suspends the call for human approval. Core holds the gate skeleton and
// the approval contract; per-tier policies and grants are std/permission.
package permission

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/victorzhuk/gohan/core/types"
)

// Verdict is a permission decision for one tool invocation.
type Verdict int

const (
	Allow Verdict = iota
	DenyVerdict
	Ask
)

// ErrApprovalRequired marks a call the gate suspended for human approval.
var ErrApprovalRequired = errors.New("gohan: human approval required")

// AskError carries the invocation the gate suspended. The run layer turns
// it into a Suspended event with reason HumanApproval.
type AskError struct {
	Invocation *ToolInvocation
}

func (e *AskError) Error() string {
	return fmt.Sprintf("gohan: tool %q needs human approval", e.Invocation.Spec.Name)
}

func (e *AskError) Unwrap() error { return ErrApprovalRequired }

// ToolInvocation is the input the gate's decider sees. The run wiring
// supplies Spec and Run per call; Taints are filled by the taint hook when
// one is wired.
type ToolInvocation struct {
	Spec   types.ToolSpec
	Call   types.ToolUse
	Run    types.RunInfo
	Taints []types.ArgTaint
}

// Option configures the gate.
type Option func(*gate)

type gate struct {
	decider       types.Decider[*ToolInvocation, Verdict]
	minConfidence float64
	taintHook     types.TaintHook
	specLookup    func(name string) (types.ToolSpec, bool)
	runLookup     func(ctx context.Context) types.RunInfo
}

// MinConfidence asks whenever the decider's confidence falls below x. The
// default is 1, so only a certain decision lets a call through.
func MinConfidence(x float64) Option {
	return func(g *gate) { g.minConfidence = x }
}

// WithTaintHook installs the taint slot. The hook runs after the hard
// blocks and before the decider; a gate without one skips taint
// enforcement until std/taint ships its matcher.
func WithTaintHook(h types.TaintHook) Option {
	return func(g *gate) { g.taintHook = h }
}

// WithSpecLookup resolves the ToolSpec for a call. The registry is the
// assembled tool set, so a miss is an unknown tool and denies the call.
func WithSpecLookup(lookup func(name string) (types.ToolSpec, bool)) Option {
	return func(g *gate) { g.specLookup = lookup }
}

// WithRunInfo resolves the run a call belongs to. Outside a run the gate
// sees a zero RunInfo, whose principal holds no scopes.
func WithRunInfo(lookup func(ctx context.Context) types.RunInfo) Option {
	return func(g *gate) { g.runLookup = lookup }
}

// Gate builds the permission middleware for the tool chain.
func Gate(d types.Decider[*ToolInvocation, Verdict], opts ...Option) types.ToolMiddleware {
	g := &gate{
		decider:       d,
		minConfidence: 1,
		specLookup:    func(string) (types.ToolSpec, bool) { return types.ToolSpec{}, false },
		runLookup:     func(context.Context) types.RunInfo { return types.RunInfo{} },
	}
	for _, opt := range opts {
		opt(g)
	}
	return g.middleware
}

func (g *gate) middleware(next types.ToolFunc) types.ToolFunc {
	return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
		spec, ok := g.specLookup(call.Name)
		if !ok {
			return denied(call.Name, "unknown tool"), nil
		}
		inv := &ToolInvocation{Spec: spec, Call: call, Run: g.runLookup(ctx)}

		if missing := missingScopes(inv.Run.Principal.Scopes, spec.RequiredScopes); len(missing) > 0 {
			return denied(call.Name, "missing scope "+strings.Join(missing, ", ")), nil
		}

		if g.taintHook != nil {
			action, taints := g.taintHook(ctx, spec, call.Args)
			inv.Taints = taints
			switch action {
			case types.TaintDeny:
				return taintDenied(call.Name, taints), nil
			case types.TaintAsk:
				return types.ToolResult{}, &AskError{Invocation: inv}
			}
		}

		switch g.decide(ctx, inv) {
		case Allow:
			return next(ctx, call)
		case DenyVerdict:
			return denied(call.Name, "denied by policy"), nil
		default:
			return types.ToolResult{}, &AskError{Invocation: inv}
		}
	}
}

// decide consults the decider. Without one the defaults are closed:
// ReadOnly and Idempotent pass, SideEffect asks. A decider failure asks —
// a broken decider must never widen permission.
func (g *gate) decide(ctx context.Context, inv *ToolInvocation) Verdict {
	if g.decider == nil {
		if inv.Spec.Effect == types.SideEffect {
			return Ask
		}
		return Allow
	}
	d, err := g.decider.Decide(ctx, inv)
	if err != nil {
		return Ask
	}
	if d.Confidence < g.minConfidence {
		return Ask
	}
	return d.Value
}

func missingScopes(have, want []string) []string {
	var missing []string
	for _, w := range want {
		if !slices.Contains(have, w) {
			missing = append(missing, w)
		}
	}
	return missing
}

func denied(tool, reason string) types.ToolResult {
	return types.ToolResult{
		Outcome: types.Failed,
		Error:   &types.ToolError{Kind: types.Permanent, Message: fmt.Sprintf("tool %s denied: %s", tool, reason)},
	}
}

func taintDenied(tool string, taints []types.ArgTaint) types.ToolResult {
	if len(taints) == 0 {
		return denied(tool, "denied by taint policy")
	}
	t := taints[0]
	return types.ToolResult{
		Outcome: types.Failed,
		Error: &types.ToolError{
			Kind:    types.Permanent,
			Message: fmt.Sprintf("tool %s denied: %s", tool, types.TaintDenied{Arg: t.Arg, Origins: t.Origins}),
		},
	}
}
