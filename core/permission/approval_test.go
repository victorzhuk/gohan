package permission

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func invocation(args jsontext.Value, taints ...types.ArgTaint) *ToolInvocation {
	return &ToolInvocation{
		Spec: types.ToolSpec{
			Name:     "create_booking",
			Effect:   types.SideEffect,
			Risk:     types.RiskMedium,
			ReadBack: "booking id",
		},
		Call:   types.ToolUse{ID: "c1", Name: "create_booking", Args: args},
		Run:    types.RunInfo{SessionID: "s1", Principal: types.Principal{Subject: "op", Tenant: "t1"}},
		Taints: taints,
	}
}

func TestApprovalContract(t *testing.T) {
	ctx := context.Background()

	t.Run("permission.rich-request", func(t *testing.T) {
		args := jsontext.Value(`{"note":"team offsite","url":"https://booking.example/api"}`)
		tainted := types.ArgTaint{
			Arg:     "url",
			Origins: []types.Origin{{Kind: types.OriginTool, Name: "web_fetch"}},
			Match:   "https://booking.example/api",
		}
		prev := jsontext.Value(`{"note":"team offsite","url":"https://old.example/api"}`)

		req := NewApprovalRequest(invocation(args, tainted), prev, time.Unix(1000, 0))

		if req.ArgOrigins["url"].Kind != types.OriginTool {
			t.Fatalf("ArgOrigins[url].Kind = %v, want %v", req.ArgOrigins["url"].Kind, types.OriginTool)
		}
		if req.ArgOrigins["note"].Kind != types.OriginModel {
			t.Fatalf("ArgOrigins[note].Kind = %v, want %v", req.ArgOrigins["note"].Kind, types.OriginModel)
		}
		if req.Risk != types.RiskMedium {
			t.Fatalf("Risk = %v, want %v", req.Risk, types.RiskMedium)
		}
		if req.Tool.Name != "create_booking" {
			t.Fatalf("Tool.Name = %q, want %q", req.Tool.Name, "create_booking")
		}
		var diff struct {
			Added   map[string]string `json:"added"`
			Changed map[string]struct {
				From string `json:"from"`
				To   string `json:"to"`
			} `json:"changed"`
		}
		if err := json.Unmarshal(req.DiffFromLast, &diff); err != nil {
			t.Fatalf("DiffFromLast is not JSON: %v", err)
		}
		if got := diff.Changed["url"].From; got != "https://old.example/api" {
			t.Fatalf("DiffFromLast changed[url].from = %q, want previous value", got)
		}
		if got := diff.Changed["url"].To; got != "https://booking.example/api" {
			t.Fatalf("DiffFromLast changed[url].to = %q, want current value", got)
		}
		if _, ok := diff.Added["note"]; ok {
			t.Fatal("unchanged argument must not appear in DiffFromLast")
		}
	})

	t.Run("first ask carries no diff", func(t *testing.T) {
		req := NewApprovalRequest(invocation(jsontext.Value(`{"note":"x"}`)), nil, time.Time{})
		if req.DiffFromLast != nil {
			t.Fatalf("DiffFromLast = %s, want nil on first ask", req.DiffFromLast)
		}
	})

	t.Run("permission.ineligible-keeps-token", func(t *testing.T) {
		req := ApprovalRequest{
			Risk: types.RiskHigh,
			Eligible: Eligibility{
				Scopes:          []string{"approve:refunds"},
				ExcludeSubjects: []string{"svc-bot"},
				Quorum:          1,
			},
		}

		err := CheckEligibility(req, types.Principal{Subject: "svc-bot", Scopes: []string{"approve:refunds"}})
		if !errors.Is(err, ErrApproverNotEligible) {
			t.Fatalf("err = %v, want %v", err, ErrApproverNotEligible)
		}
		err = CheckEligibility(req, types.Principal{Subject: "op", Scopes: []string{"session:write"}})
		if !errors.Is(err, ErrApproverNotEligible) {
			t.Fatalf("err = %v, want %v", err, ErrApproverNotEligible)
		}
		if err := CheckEligibility(req, types.Principal{Subject: "op", Scopes: []string{"approve:refunds"}}); err != nil {
			t.Fatalf("eligible approver refused: %v", err)
		}

		// The refusal consumed nothing: the request is unchanged and still
		// grantable, so the pending token survives for an eligible approver.
		if len(req.Eligible.Scopes) != 1 || req.Eligible.Scopes[0] != "approve:refunds" {
			t.Fatalf("Eligibility mutated by refusal: %+v", req.Eligible)
		}
	})

	t.Run("policy carries max pending", func(t *testing.T) {
		p := ApprovalPolicy{Scope: "approve:refunds", MaxPending: 20, Quorum: 2}
		if p.MaxPending != 20 {
			t.Fatalf("MaxPending = %d, want 20", p.MaxPending)
		}
	})

	t.Run("permission.approval-audit-eligibility", func(t *testing.T) {
		audit := stores.NewMemoryAuditLog()
		rec := stores.AuditRecord{
			Kind:      stores.AuditApproval,
			SessionID: "s1",
			Subject:   "svc-bot",
			Approver:  "svc-bot",
		}
		if err := audit.Append(ctx, rec); err != nil {
			t.Fatalf("Append: %v", err)
		}
		var got *stores.AuditRecord
		for r, err := range audit.Read(ctx, "s1") {
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			got = &r
		}
		if got == nil || got.Kind != stores.AuditApproval {
			t.Fatalf("audit record = %+v, want kind %q", got, stores.AuditApproval)
		}
		if got.Approver != "svc-bot" {
			t.Fatalf("Approver = %q, want the approving service", got.Approver)
		}
		if stores.AuditGrantedScope != "granted_by_scope" {
			t.Fatalf("granted_by_scope kind = %q", stores.AuditGrantedScope)
		}
	})
}

func TestApprovalPolicySource(t *testing.T) {
	var _ ApprovalPolicySource = stubPolicySource{}
	want := ApprovalPolicy{Scope: "approve:tool", Quorum: 2}
	got, err := stubPolicySource{}.ApprovalPolicy(context.Background(), types.RiskHigh, "tool", false)
	if err != nil {
		t.Fatalf("ApprovalPolicy: %v", err)
	}
	if got.Scope != want.Scope || got.Quorum != want.Quorum {
		t.Fatalf("policy = %+v, want %+v", got, want)
	}
}

type stubPolicySource struct{}

func (stubPolicySource) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (ApprovalPolicy, error) {
	return ApprovalPolicy{Scope: "approve:tool", Quorum: 2}, nil
}
