package permission

import (
	"errors"
	"testing"

	corepermission "github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
)

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
