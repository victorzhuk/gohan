package permission

import (
	"context"
	"encoding/json/jsontext"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// ProviderVerdict says how a provider-executed tool enters the model
// request: omitted, declared, or declared with the provider's approval
// flag.
type ProviderVerdict int

const (
	ProviderOmit ProviderVerdict = iota
	ProviderDeclare
	ProviderDeclareApproval
)

// ProviderToolDecision is the pre-call gate verdict translated for the
// request assembler. Decision keeps the raw gate result so the assembler
// can name the reason on an omitted tool.
type ProviderToolDecision struct {
	Verdict  ProviderVerdict
	Decision Decision

	inv *ToolInvocation
}

// Invocation returns the pending call the decision was made on.
func (d ProviderToolDecision) Invocation() *ToolInvocation {
	return d.inv
}

// DecideProviderTool runs the ordinary gate on a registered provider-executed
// spec with empty arguments, before request assembly. Ordering inside the
// gate is the sealed one: scopes, live deny, taint hook, session grant and
// decider. Run wiring flows through WithRunInfo and the other gate options.
func DecideProviderTool(ctx context.Context, d types.Decider[*ToolInvocation, Verdict], spec types.ToolSpec, opts ...Option) ProviderToolDecision {
	g := newGate(d, opts)
	inv := &ToolInvocation{
		Spec: spec,
		Call: types.ToolUse{Name: spec.Name, Args: jsontext.Value(`{}`)},
		Run:  g.runLookup(ctx),
	}
	res := g.decideCall(ctx, inv)
	dec := ProviderToolDecision{Decision: res, inv: inv}
	switch res.Verdict {
	case Allow:
		dec.Verdict = ProviderDeclare
	case Ask:
		dec.Verdict = ProviderDeclareApproval
	default:
		dec.Verdict = ProviderOmit
	}
	return dec
}

// ProviderApproval maps a provider approval request to the suspension
// payload: an ApprovalRequest built from the pending call, with ArgOrigins
// from the taint hook when the pre-call gate produced them. Callers judge
// expiry by their own clock.
func ProviderApproval(inv *ToolInvocation, lastApproved jsontext.Value, expires time.Time) ApprovalRequest {
	return NewApprovalRequest(inv, lastApproved, expires)
}
