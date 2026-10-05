package permission

import (
	"context"
	"errors"
	"testing"

	corepermission "github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
)

func TestPolicySource(t *testing.T) {
	var src corepermission.ApprovalPolicySource = PolicySource{}

	t.Run("delegates-to-tier-policy", func(t *testing.T) {
		for _, tc := range []struct {
			risk        types.RiskTier
			tool        string
			reversible  bool
			wantScope   string
			wantQuorum  int
			wantSeparat bool
		}{
			{types.RiskHigh, "deploy_prod", true, "approve:deploy_prod", 1, true},
			{types.RiskHigh, "wire_transfer", false, "approve:wire_transfer", 2, true},
			{types.RiskMedium, "send_refund", true, "approve:send_refund", 0, false},
			{types.RiskLow, "lookup", true, "session:write", 0, false},
		} {
			p, err := src.ApprovalPolicy(context.Background(), tc.risk, tc.tool, tc.reversible)
			if err != nil {
				t.Fatalf("%s policy: unexpected error %v", tc.tool, err)
			}
			if p.Scope != tc.wantScope || p.Quorum != tc.wantQuorum || p.SeparateFromOriginator != tc.wantSeparat {
				t.Fatalf("%s policy = %+v, want scope %q quorum %d separate %v", tc.tool, p, tc.wantScope, tc.wantQuorum, tc.wantSeparat)
			}
			if p.MaxPending != DefaultMaxPending {
				t.Fatalf("%s MaxPending = %d, want %d", tc.tool, p.MaxPending, DefaultMaxPending)
			}
		}
	})

	t.Run("high-risk-owner-still-needs-scope", func(t *testing.T) {
		p, err := src.ApprovalPolicy(context.Background(), types.RiskHigh, "deploy_prod", true)
		if err != nil {
			t.Fatal(err)
		}
		ev := NewEvaluator(p, types.RiskHigh, "owner", "op")
		err = ev.Approve(types.Principal{Subject: "owner"})
		if !errors.Is(err, corepermission.ErrApproverNotEligible) {
			t.Fatalf("scope-less owner on high risk: got %v, want ErrApproverNotEligible", err)
		}
		if err := ev.Approve(types.Principal{Subject: "owner", Scopes: []string{"approve:deploy_prod"}}); err != nil {
			t.Fatalf("scoped owner refused: %v", err)
		}
	})

	t.Run("low-risk-owner-exception-holds", func(t *testing.T) {
		p, err := src.ApprovalPolicy(context.Background(), types.RiskLow, "lookup", true)
		if err != nil {
			t.Fatal(err)
		}
		ev := NewEvaluator(p, types.RiskLow, "owner", "op")
		if err := ev.Approve(types.Principal{Subject: "owner"}); err != nil {
			t.Fatalf("owner refused on low risk: %v", err)
		}
	})
}

func TestApprovalPolicy(t *testing.T) {
	t.Run("permission.self-approval-refused-high-risk", func(t *testing.T) {
		p := TierPolicy(types.RiskHigh, "deploy_prod", false)
		if !p.SeparateFromOriginator {
			t.Fatal("high-risk policy does not separate from the originator")
		}
		ev := NewEvaluator(p, types.RiskHigh, "owner", "op")
		err := ev.Approve(types.Principal{Subject: "op", Scopes: []string{"approve:deploy_prod"}})
		if !errors.Is(err, corepermission.ErrApproverNotEligible) {
			t.Fatalf("originator approval: got %v, want ErrApproverNotEligible", err)
		}
		if ev.Satisfied() {
			t.Fatal("refused approval satisfied the quorum")
		}
		if err := ev.Approve(types.Principal{Subject: "sec", Scopes: []string{"approve:deploy_prod"}}); err != nil {
			t.Fatalf("eligible approver refused: %v", err)
		}
	})

	t.Run("permission.approve-scope-required-medium", func(t *testing.T) {
		p := TierPolicy(types.RiskMedium, "send_refund", false)
		ev := NewEvaluator(p, types.RiskMedium, "owner", "op")
		err := ev.Approve(types.Principal{Subject: "clerk", Scopes: []string{"session:write"}})
		if !errors.Is(err, corepermission.ErrApproverNotEligible) {
			t.Fatalf("session:write-only approver: got %v, want ErrApproverNotEligible", err)
		}
		if err := ev.Approve(types.Principal{Subject: "owner", Scopes: nil}); err != nil {
			t.Fatalf("owner refused: %v", err)
		}
		other := NewEvaluator(p, types.RiskMedium, "someone_else", "op")
		if err := other.Approve(types.Principal{Subject: "fin", Scopes: []string{"approve:send_refund"}}); err != nil {
			t.Fatalf("approve-scope holder refused: %v", err)
		}
	})

	t.Run("permission.quorum-two-approvers", func(t *testing.T) {
		p := TierPolicy(types.RiskHigh, "wire_transfer", true)
		if p.Quorum != 2 {
			t.Fatalf("irreversible high-risk quorum: got %d, want 2", p.Quorum)
		}
		ev := NewEvaluator(p, types.RiskHigh, "owner", "op")
		first := types.Principal{Subject: "fin", Scopes: []string{"approve:wire_transfer"}}
		if err := ev.Approve(first); err != nil {
			t.Fatalf("first approver refused: %v", err)
		}
		if ev.Satisfied() {
			t.Fatal("quorum satisfied after one approver")
		}
		if err := ev.Approve(first); !errors.Is(err, corepermission.ErrApproverNotEligible) {
			t.Fatalf("repeat approval by same subject: got %v, want ErrApproverNotEligible", err)
		}
		if err := ev.Approve(types.Principal{Subject: "sec", Scopes: []string{"approve:wire_transfer"}}); err != nil {
			t.Fatalf("second approver refused: %v", err)
		}
		if !ev.Satisfied() {
			t.Fatal("quorum unsatisfied after two distinct approvers")
		}
	})
}
