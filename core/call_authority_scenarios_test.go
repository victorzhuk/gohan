package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func callAuthorityReceipt(t *testing.T, callID, args string) stores.History {
	t.Helper()
	msg, err := approvalReceiptMessage(approvalReceipt{
		RunID:      "run-1",
		Generation: 1,
		CallID:     callID,
		Tool:       "wipe",
		Args:       json.RawMessage(args),
		Approvers:  []types.Principal{{Subject: "op", Tenant: "t1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return stores.History{Messages: []types.Message{msg}}
}

func TestCallAuthorityScenarios(t *testing.T) {
	runCtx := types.WithRunInfo(context.Background(), types.RunInfo{RunID: "run-1"})
	ctx := context.Background()

	t.Run("permission.ordinary-approval-call-bound", func(t *testing.T) {
		gate := nativeBatchGate(gateTestConfig(nil), callAuthorityReceipt(t, "c1", `{"a":1}`))
		later := types.ToolUse{ID: "c2", Name: "wipe", Args: json.RawMessage(`{"a":1}`)}
		if dec := gate(runCtx, later); dec.Outcome != runtime.BatchAsk {
			t.Fatalf("outcome = %v, want ask: a later call on the same fingerprint needs its own decision", dec.Outcome)
		}
		otherRun := types.WithRunInfo(context.Background(), types.RunInfo{RunID: "run-2"})
		sameID := types.ToolUse{ID: "c1", Name: "wipe", Args: json.RawMessage(`{"a":1}`)}
		if dec := gate(otherRun, sameID); dec.Outcome != runtime.BatchAsk {
			t.Fatalf("outcome = %v, want ask: the approval recorded another run", dec.Outcome)
		}
		var executed []string
		report, err := runtime.Batch{
			Calls:  []types.ToolUse{later},
			Limits: types.RunLimits{MaxToolCalls: 10},
			Gate:   gate,
			Exec: func(_ context.Context, call types.ToolUse) (types.ToolResult, error) {
				executed = append(executed, call.ID)
				return types.ToolResult{ID: call.ID, Outcome: types.Succeeded}, nil
			},
		}.Run(runCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(executed) != 0 {
			t.Fatalf("executed = %v, want no side effect from the earlier approval", executed)
		}
		if report.Suspend == nil || report.Suspend.Call.ID != "c2" {
			t.Fatalf("suspend = %+v, want the later call held for its own decision", report.Suspend)
		}
	})

	t.Run("permission.approval-full-arguments-bound", func(t *testing.T) {
		specs := []types.ToolSpec{{Name: "wipe", Effect: types.SideEffect, FingerprintFields: []string{"a"}}}
		gate := nativeBatchGate(&resolvedNativeConfig{specs: specs}, callAuthorityReceipt(t, "c1", `{"a":1,"note":"one"}`))
		changed := types.ToolUse{ID: "c1", Name: "wipe", Args: json.RawMessage(`{"note":"two","a":1}`)}
		if dec := gate(runCtx, changed); dec.Outcome != runtime.BatchAsk {
			t.Fatalf("outcome = %v, want ask: the approval does not authorize a changed argument outside the fingerprint fields", dec.Outcome)
		}
	})

	t.Run("permission.tightened-scope-refused", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		src := &authPolicySource{}
		conv := h.conv(t, src, &authCreds{})
		src.scope = "approve:flights"
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()))
		if len(errs) == 0 || !errors.Is(errs[0], types.ErrApproverNotEligible) {
			t.Fatalf("approval under tightened scope: got %v, want ErrApproverNotEligible", errs)
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Fatalf("refused approval consumed the token: %v", err)
		}
		if h.rt.i != 0 {
			t.Errorf("refused approval executed the run: %d steps", h.rt.i)
		}
		src.scope = ""
		errs = h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()))
		if len(errs) != 0 {
			t.Fatalf("eligible approval after the policy restored: got %v", errs)
		}
	})

	t.Run("permission.tightened-votes-revalidated", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 2)
		src := &authPolicySource{quorum: 2}
		conv := h.conv(t, src, &authCreds{})
		if errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve())); len(errs) != 0 {
			t.Fatalf("first approval: got %v", errs)
		}
		src.scope = "approve:flights"
		op2 := types.Principal{
			Tenant: "acme", Subject: "op2",
			Scopes: []string{types.ScopeSessionRead, types.ScopeSessionWrite, "approve:tool", "approve:flights"},
		}
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, op2), token, Approve()))
		if len(errs) != 0 {
			t.Fatalf("second approval under tightened policy: got %v", errs)
		}
		if h.rt.i != 0 {
			t.Errorf("stale vote completed quorum: %d executions", h.rt.i)
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Fatalf("token consumed before the current-policy quorum: %v", err)
		}
		errs = h.errors(t, conv.Resume(types.WithPrincipal(ctx, op2), token, Approve()))
		if len(errs) == 0 || !errors.Is(errs[0], types.ErrApproverNotEligible) {
			t.Fatalf("duplicate vote under tightened policy: got %v, want ErrApproverNotEligible", errs)
		}
		if h.rt.i != 0 {
			t.Errorf("duplicate vote executed the run: %d steps", h.rt.i)
		}
		cp, err := h.cps.Peek(ctx, token)
		if err != nil {
			t.Fatalf("duplicate refusal consumed the token: %v", err)
		}
		env, _, err := decodeCheckpoint(cp, h.rt, "flights")
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Approvals[0].ApprovedBy) != 2 {
			t.Fatalf("recorded approvers: %+v, want op and op2 once each", env.Approvals[0].ApprovedBy)
		}
		src.scope = ""
		op3 := types.Principal{
			Tenant: "acme", Subject: "op3",
			Scopes: []string{types.ScopeSessionRead, types.ScopeSessionWrite, "approve:tool"},
		}
		errs = h.errors(t, conv.Resume(types.WithPrincipal(ctx, op3), token, Approve()))
		if len(errs) != 0 {
			t.Fatalf("qualifying approval after the policy restored: got %v", errs)
		}
		if h.rt.i != 1 {
			t.Errorf("tool executions after the restored policy completed quorum: got %d, want 1", h.rt.i)
		}
		if _, err := h.cps.Peek(ctx, token); err == nil {
			t.Fatalf("completed approval left the token pending")
		}
	})
}
