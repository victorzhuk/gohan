package permission

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// ExpiryAction says what happens to a pending approval whose token runs out.
type ExpiryAction int

const (
	RejectOnExpiry ExpiryAction = iota
	EscalateOnExpiry
)

// ApprovalPolicy is the per-risk-tier rule evaluated inside Resume before an
// approval is accepted. MaxPending caps the approvals one subject and tenant
// may hold at once; zero leaves the cap to std/permission, which supplies
// the default of 20.
type ApprovalPolicy struct {
	Scope                  string
	SeparateFromOriginator bool
	Quorum                 int
	Escalation             []string
	MaxPending             int
}

// Eligibility is the resolved set of subjects that may grant one request.
type Eligibility struct {
	Scopes          []string
	ExcludeSubjects []string
	Quorum          int
}

// ApprovedVia records how an approval was granted when a principal acts for
// another subject through token exchange.
type ApprovedVia struct {
	Scope     string
	Policy    types.RiskTier
	ActingFor string
}

// ErrApproverNotEligible reports a principal that may not approve a request.
// The sentinel's home is the floor (types), so errors.Is matches the same
// error whichever package declares the reference.
var ErrApproverNotEligible = types.ErrApproverNotEligible

// ApprovalPolicySource resolves the policy a request is judged by. The
// consumer owns the interface; assembly supplies the implementation. The
// policy is resolved when a request is created and again before an
// approval or edit is accepted. An error never grants permission.
type ApprovalPolicySource interface {
	ApprovalPolicy(ctx context.Context, risk types.RiskTier, tool string, reversible bool) (ApprovalPolicy, error)
}

// ApprovalRequest is the payload a suspended call carries.
type ApprovalRequest struct {
	Tool         types.ToolSpec
	Call         types.ToolUse
	Fingerprint  stores.Fingerprint
	Risk         types.RiskTier
	Reversible   bool
	ReadBack     string
	ArgOrigins   map[string]types.Origin
	DiffFromLast json.RawMessage
	Consequence  string
	ExpiresAt    time.Time
	OnExpiry     ExpiryAction
	Eligible     Eligibility
}

// NewApprovalRequest builds the suspension payload for one invocation.
// lastApproved carries the previous approved call's arguments for the same
// tool in the session, or nil on the first call. expires is judged by the
// caller's clock.
func NewApprovalRequest(inv *ToolInvocation, lastApproved jsontext.Value, expires time.Time) ApprovalRequest {
	return ApprovalRequest{
		Tool:         inv.Spec,
		Call:         inv.Call,
		Risk:         inv.Spec.Risk,
		ReadBack:     inv.Spec.ReadBack,
		ArgOrigins:   argOrigins(inv.Call.Args, inv.Taints),
		DiffFromLast: diffFromLast(lastApproved, inv.Call.Args),
		ExpiresAt:    expires,
	}
}

// CheckEligibility reports whether the approver may grant req. It reads only
// its inputs and returns before any store or token state changes, so a
// refused approval leaves a pending token usable for an eligible approver.
func CheckEligibility(req ApprovalRequest, approver types.Principal) error {
	for _, s := range req.Eligible.Scopes {
		if !slices.Contains(approver.Scopes, s) {
			return fmt.Errorf("%w: missing scope %q", ErrApproverNotEligible, s)
		}
	}
	if slices.Contains(req.Eligible.ExcludeSubjects, approver.Subject) {
		return fmt.Errorf("%w: subject %q is excluded", ErrApproverNotEligible, approver.Subject)
	}
	return nil
}

// argOrigins names where each top-level argument came from: model inference
// unless a taint matched the argument against untrusted session content.
func argOrigins(args jsontext.Value, taints []types.ArgTaint) map[string]types.Origin {
	origins := map[string]types.Origin{}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil || fields == nil {
		origins[""] = types.Origin{Kind: types.OriginModel}
		return origins
	}
	for k := range fields {
		origins[k] = types.Origin{Kind: types.OriginModel}
	}
	for _, t := range taints {
		if _, ok := origins[t.Arg]; ok && len(t.Origins) > 0 {
			origins[t.Arg] = t.Origins[0]
		}
	}
	return origins
}

// diffDelta is one changed argument: its previous and its new value.
type diffDelta struct {
	From any `json:"from"`
	To   any `json:"to"`
}

// argsDiff is the shape DiffFromLast renders; empty sections are omitted and
// object keys are sorted.
type argsDiff struct {
	Added   map[string]any       `json:"added,omitempty"`
	Removed []string             `json:"removed,omitempty"`
	Changed map[string]diffDelta `json:"changed,omitempty"`
}

// diffFromLast compares the two calls' arguments and renders the change as
// canonical JSON. Equal or missing predecessors yield nil.
func diffFromLast(prev, cur jsontext.Value) json.RawMessage {
	var prevArgs, curArgs map[string]any
	hasPrev := json.Unmarshal(prev, &prevArgs) == nil && prevArgs != nil
	hasCur := json.Unmarshal(cur, &curArgs) == nil && curArgs != nil
	if !hasPrev || !hasCur {
		return nil
	}

	d := argsDiff{}
	for k, v := range curArgs {
		before, ok := prevArgs[k]
		if !ok {
			if d.Added == nil {
				d.Added = map[string]any{}
			}
			d.Added[k] = v
			continue
		}
		if !sameJSON(before, v) {
			if d.Changed == nil {
				d.Changed = map[string]diffDelta{}
			}
			d.Changed[k] = diffDelta{From: before, To: v}
		}
	}
	for k := range prevArgs {
		if _, ok := curArgs[k]; !ok {
			d.Removed = append(d.Removed, k)
		}
	}
	slices.Sort(d.Removed)

	if d.Added == nil && d.Changed == nil && len(d.Removed) == 0 {
		return nil
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil
	}
	return raw
}

func sameJSON(a, b any) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ab) == string(bb)
}
