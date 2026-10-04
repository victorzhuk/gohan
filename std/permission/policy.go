// Package permission supplies the std defaults the permission gate and the
// resume path consume: per-tier approval policies, session grants, expiry
// handling and the pending-approval cap.
package permission

import (
	"fmt"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
)

// DefaultMaxPending is the pending-approval cap a policy without an explicit
// MaxPending gets, per subject and per tenant.
const DefaultMaxPending = 20

// TierPolicy returns the std default policy for one risk tier. tool names
// the tool whose approval scope the medium and high tiers require;
// irreversible raises the high tier's quorum to two distinct approvers.
func TierPolicy(tier types.RiskTier, tool string, irreversible bool) permission.ApprovalPolicy {
	switch tier {
	case types.RiskHigh:
		q := 1
		if irreversible {
			q = 2
		}
		return permission.ApprovalPolicy{
			Scope:                  "approve:" + tool,
			SeparateFromOriginator: true,
			Quorum:                 q,
			MaxPending:             DefaultMaxPending,
		}
	case types.RiskMedium:
		return permission.ApprovalPolicy{
			Scope:      "approve:" + tool,
			MaxPending: DefaultMaxPending,
		}
	default:
		return permission.ApprovalPolicy{
			Scope:      "session:write",
			MaxPending: DefaultMaxPending,
		}
	}
}

// Evaluator applies one tier's policy to the approval attempts of a single
// pending request. A refused attempt changes no state, so the token stays
// usable for an eligible approver.
type Evaluator struct {
	policy     permission.ApprovalPolicy
	tier       types.RiskTier
	owner      string
	originator string
	approved   map[string]bool
}

// NewEvaluator starts the evaluation for one request: owner is the session
// owner's subject, originator the subject that triggered the suspended call.
func NewEvaluator(policy permission.ApprovalPolicy, tier types.RiskTier, owner, originator string) *Evaluator {
	return &Evaluator{
		policy:     policy,
		tier:       tier,
		owner:      owner,
		originator: originator,
		approved:   map[string]bool{},
	}
}

// Approve records one approver under the policy. It refuses with
// ErrApproverNotEligible when the approver holds neither the session
// ownership nor the policy's scope, and for the high tier when the
// approver is the originator or already counted toward the quorum.
func (e *Evaluator) Approve(approver types.Principal) error {
	if err := e.check(approver); err != nil {
		return err
	}
	e.approved[approver.Subject] = true
	return nil
}

// Satisfied reports whether the quorum of distinct approvers is met.
func (e *Evaluator) Satisfied() bool {
	q := e.policy.Quorum
	if q < 1 {
		q = 1
	}
	return len(e.approved) >= q
}

func (e *Evaluator) check(approver types.Principal) error {
	eligible := approver.Subject == e.owner || has(approver.Scopes, e.policy.Scope)
	if !eligible {
		return fmt.Errorf("%w: missing scope %q", permission.ErrApproverNotEligible, e.policy.Scope)
	}
	if e.tier == types.RiskHigh && e.policy.SeparateFromOriginator && approver.Subject == e.originator {
		return fmt.Errorf("%w: separate_from_originator", permission.ErrApproverNotEligible)
	}
	if e.approved[approver.Subject] {
		return fmt.Errorf("%w: subject %q already approved", permission.ErrApproverNotEligible, approver.Subject)
	}
	return nil
}

func has(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}
