package gohan

import (
	"context"
	"encoding/json"
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
}
