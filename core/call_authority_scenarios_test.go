package gohan

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
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

	t.Run("tools.exact-numeric-fingerprint", func(t *testing.T) {
		spec := types.ToolSpec{Name: "wipe", Effect: types.SideEffect}
		args92 := json.RawMessage(`{"n":9007199254740992}`)
		args93 := json.RawMessage(`{"n":9007199254740993}`)
		if ToolFingerprint(spec, args92) == ToolFingerprint(spec, args93) {
			t.Fatal("adjacent integers share an approval fingerprint: authority minted for one would cover the other")
		}
		if !strings.HasPrefix(string(ToolFingerprint(spec, args92)), "exact-v1:") {
			t.Fatal("approval fingerprint lacks the exact-v1 version prefix: a legacy rounded grant could not be told apart")
		}
		nested92 := json.RawMessage(`{"a":{"n":9007199254740992}}`)
		nested93 := json.RawMessage(`{"a":{"n":9007199254740993}}`)
		if ToolFingerprint(spec, nested92) == ToolFingerprint(spec, nested93) {
			t.Fatal("adjacent integers inside a nested value share an approval fingerprint")
		}
		if ToolFingerprint(spec, json.RawMessage(`{"a":{"n":1e2},"b":[true,null,"x"]}`)) != ToolFingerprint(spec, json.RawMessage(`{"b":[true,null,"x"],"a":{"n":1.0e2}}`)) {
			t.Fatal("decimal-equivalent nested arguments with reordered members produced different fingerprints")
		}

		req := permission.NewApprovalRequest(
			&permission.ToolInvocation{Spec: spec, Call: types.ToolUse{ID: "c3", Name: "wipe", Args: args93}},
			jsontext.Value(`{"n":9007199254740992}`), time.Time{},
		)
		var diff struct {
			Changed map[string]struct {
				From json.Number `json:"from"`
				To   json.Number `json:"to"`
			} `json:"changed"`
		}
		if err := json.Unmarshal(req.DiffFromLast, &diff); err != nil {
			t.Fatalf("DiffFromLast is not JSON: %v", err)
		}
		got := diff.Changed["n"]
		if got.From != "9007199254740992" || got.To != "9007199254740993" {
			t.Fatalf("DiffFromLast changed[n] = %s -> %s, want the exact adjacent integers", got.From, got.To)
		}
		equivalent := permission.NewApprovalRequest(
			&permission.ToolInvocation{Spec: spec, Call: types.ToolUse{ID: "c3", Name: "wipe", Args: jsontext.Value(`{"n":1e2}`)}},
			jsontext.Value(`{"n":100.0}`), time.Time{},
		)
		if equivalent.DiffFromLast != nil {
			t.Fatalf("DiffFromLast = %s, want nil for decimal-equivalent arguments", equivalent.DiffFromLast)
		}

		at := time.Now()
		store := &scenarioGrantStore{}
		if err := store.Put(context.Background(), "s1", permission.Grant{
			Tool: "wipe", Fingerprint: ToolFingerprint(spec, args92),
			Subject: "op", Approver: "op", ExpiresAt: at.Add(time.Hour),
		}); err != nil {
			t.Fatalf("store grant: %v", err)
		}
		session := types.WithRunInfo(ctx, types.RunInfo{
			SessionID: "s1", Principal: types.Principal{Tenant: "t1", Subject: "op"},
		})
		gate := nativeBatchGate(gateTestConfig(nil), stores.History{}, store)
		if dec := gate(session, types.ToolUse{ID: "c1", Name: "wipe", Args: args92}); dec.Outcome != runtime.BatchAllow {
			t.Fatalf("outcome = %v, want allow: the grant minted for this exact value covers its own call", dec.Outcome)
		}
		if dec := gate(session, types.ToolUse{ID: "c2", Name: "wipe", Args: args93}); dec.Outcome != runtime.BatchAsk {
			t.Fatalf("outcome = %v, want ask: the 9007199254740992 grant must not cover 9007199254740993", dec.Outcome)
		}
	})

	t.Run("permission.legacy-rounded-grant-refused", func(t *testing.T) {
		legacy92 := legacyRoundedFingerprint(t, "wipe", `{"n":9007199254740992}`)
		legacy93 := legacyRoundedFingerprint(t, "wipe", `{"n":9007199254740993}`)
		if legacy92 != legacy93 {
			t.Fatalf("legacy encodings %q and %q differ: the fixture must reproduce the rounded legacy format the grant was stored under", legacy92, legacy93)
		}
		at := time.Now()
		store := &scenarioGrantStore{}
		if err := store.Put(context.Background(), "s1", permission.Grant{
			Tool: "wipe", Fingerprint: legacy92,
			Subject: "op", Approver: "op", ExpiresAt: at.Add(time.Hour),
		}); err != nil {
			t.Fatalf("store legacy grant: %v", err)
		}
		session := types.WithRunInfo(ctx, types.RunInfo{
			SessionID: "s1", Principal: types.Principal{Tenant: "t1", Subject: "op"},
		})
		gate := nativeBatchGate(gateTestConfig(nil), stores.History{}, store)
		for _, args := range []json.RawMessage{json.RawMessage(`{"n":9007199254740992}`), json.RawMessage(`{"n":9007199254740993}`)} {
			if dec := gate(session, types.ToolUse{ID: "c1", Name: "wipe", Args: args}); dec.Outcome != runtime.BatchAsk {
				t.Fatalf("outcome = %v for %s, want ask: a legacy rounded grant needs renewed approval before execution", dec.Outcome, args)
			}
		}
	})
}

// legacyRoundedFingerprint reproduces the pre-fix rounded fingerprint format
// from its own decoding, independent of the current fingerprint functions,
// so the scenario proves the stored legacy string rather than the new code.
func legacyRoundedFingerprint(t *testing.T, tool, args string) stores.Fingerprint {
	t.Helper()
	var rounded map[string]any
	if err := json.Unmarshal([]byte(args), &rounded); err != nil {
		t.Fatalf("decode legacy arguments: %v", err)
	}
	b, err := json.Marshal(rounded)
	if err != nil {
		t.Fatalf("encode legacy arguments: %v", err)
	}
	return stores.Fingerprint(tool + ":" + string(b))
}

// scenarioGrantStore matches live grants on tool, fingerprint and subject,
// the identity the wired grant stores match on.
type scenarioGrantStore struct {
	grants []permission.Grant
}

func (s *scenarioGrantStore) Put(_ context.Context, _ string, g permission.Grant) error {
	s.grants = append(s.grants, g)
	return nil
}

func (s *scenarioGrantStore) Find(_ context.Context, _ string, tool string, fp stores.Fingerprint, subject string) (permission.Grant, bool, error) {
	for _, g := range s.grants {
		if g.Tool == tool && g.Fingerprint == fp && g.Subject == subject && g.ExpiresAt.After(time.Now()) {
			return g, true, nil
		}
	}
	return permission.Grant{}, false, nil
}
