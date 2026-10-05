package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type funcDecider func(ctx context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error)

func (f funcDecider) Decide(ctx context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	return f(ctx, inv)
}

func gateTestSpecs() []types.ToolSpec {
	return []types.ToolSpec{
		{Name: "search", Effect: types.ReadOnly},
		{Name: "cache", Effect: types.Idempotent},
		{Name: "wipe", Effect: types.SideEffect},
	}
}

func gateTestConfig(decider types.Decider[*permission.ToolInvocation, permission.Verdict]) *resolvedNativeConfig {
	cfg := &resolvedNativeConfig{specs: gateTestSpecs()}
	cfg.decider = decider
	return cfg
}

func runGatedBatch(t *testing.T, gate runtime.BatchGate, names ...string) (runtime.BatchReport, []string) {
	t.Helper()
	calls := make([]types.ToolUse, len(names))
	for i, name := range names {
		calls[i] = types.ToolUse{ID: name, Name: name}
	}
	var executed []string
	report, err := runtime.Batch{
		Calls:  calls,
		Limits: types.RunLimits{MaxToolCalls: 100},
		Gate:   gate,
		Exec: func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			executed = append(executed, call.Name)
			return types.ToolResult{ID: call.ID, Outcome: types.Succeeded}, nil
		},
	}.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return report, executed
}

func TestNativeBatchGateSessionGrant(t *testing.T) {
	approveWipe := func(t *testing.T, args string) stores.History {
		t.Helper()
		msg, err := approvalReceiptMessage(approvalReceipt{
			RunID:  "run-1",
			CallID: "c1",
			Tool:   "wipe",
			Args:   json.RawMessage(args),
		})
		if err != nil {
			t.Fatal(err)
		}
		return stores.History{Messages: []types.Message{msg}}
	}
	askDecider := gateTestConfig(nil)

	t.Run("approved call executes on the next drive without asking", func(t *testing.T) {
		gate := nativeBatchGate(askDecider, approveWipe(t, `{"a":1}`))
		dec := gate(context.Background(), types.ToolUse{ID: "wipe", Name: "wipe", Args: json.RawMessage(`{"a":1}`)})
		if dec.Outcome != runtime.BatchAllow {
			t.Fatalf("outcome = %v, want allow for the approved fingerprint", dec.Outcome)
		}
	})

	t.Run("ungranted side effect still asks", func(t *testing.T) {
		gate := nativeBatchGate(askDecider, approveWipe(t, `{"a":1}`))
		dec := gate(context.Background(), types.ToolUse{ID: "wipe", Name: "wipe", Args: json.RawMessage(`{"a":2}`)})
		if dec.Outcome != runtime.BatchAsk {
			t.Fatalf("outcome = %v, want ask for changed arguments", dec.Outcome)
		}
		dec = gate(context.Background(), types.ToolUse{ID: "cache", Name: "cache"})
		if dec.Outcome != runtime.BatchAllow {
			t.Fatalf("outcome = %v, want idempotent call unaffected by the grant", dec.Outcome)
		}
	})

	t.Run("no receipt in history asks", func(t *testing.T) {
		gate := nativeBatchGate(askDecider, stores.History{})
		dec := gate(context.Background(), types.ToolUse{ID: "wipe", Name: "wipe", Args: json.RawMessage(`{"a":1}`)})
		if dec.Outcome != runtime.BatchAsk {
			t.Fatalf("outcome = %v, want ask", dec.Outcome)
		}
	})

	t.Run("live deny wins over a recorded approval", func(t *testing.T) {
		cfg := gateTestConfig(funcDecider(func(ctx context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
			return types.Decision[permission.Verdict]{Value: permission.DenyVerdict, Confidence: 1}, nil
		}))
		gate := nativeBatchGate(cfg, approveWipe(t, `{"a":1}`))
		dec := gate(context.Background(), types.ToolUse{ID: "wipe", Name: "wipe", Args: json.RawMessage(`{"a":1}`)})
		if dec.Outcome != runtime.BatchDeny {
			t.Fatalf("outcome = %v, want deny", dec.Outcome)
		}
	})

	t.Run("malformed receipt grants nothing", func(t *testing.T) {
		h := stores.History{Messages: []types.Message{{
			Role: types.RoleAssistant,
			Meta: map[string]any{ApprovalReceiptKey: map[string]any{"tool": "wipe"}},
		}}}
		gate := nativeBatchGate(askDecider, h)
		dec := gate(context.Background(), types.ToolUse{ID: "wipe", Name: "wipe", Args: json.RawMessage(`{"a":1}`)})
		if dec.Outcome != runtime.BatchAsk {
			t.Fatalf("outcome = %v, want ask", dec.Outcome)
		}
	})
}

func TestNativeBatchGateDecider(t *testing.T) {
	t.Run("denied side effect never executes", func(t *testing.T) {
		cfg := gateTestConfig(funcDecider(func(ctx context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
			if inv.Spec.Effect != types.SideEffect {
				return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
			}
			return types.Decision[permission.Verdict]{Value: permission.DenyVerdict, Confidence: 1}, nil
		}))
		report, executed := runGatedBatch(t, nativeBatchGate(cfg, stores.History{}), "search", "wipe")
		if len(executed) != 1 || executed[0] != "search" {
			t.Fatalf("executed = %v, want only search", executed)
		}
		if len(report.Results) != 2 || report.Results[1].Call.Name != "wipe" {
			t.Fatalf("results = %+v, want both calls in order", report.Results)
		}
		res := report.Results[1].Result
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("result = %+v, want Failed(Permanent)", res)
		}
		if !strings.HasPrefix(res.Error.Message, runtime.NotExecutedPrefix) {
			t.Fatalf("reason = %q, want the not_executed prefix", res.Error.Message)
		}
		if report.Suspend != nil {
			t.Fatalf("unexpected suspension of %v", report.Suspend.Call.Name)
		}
	})

	t.Run("decider error asks instead of allowing", func(t *testing.T) {
		cfg := gateTestConfig(funcDecider(func(ctx context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
			return types.Decision[permission.Verdict]{}, errors.New("policy store down")
		}))
		gate := nativeBatchGate(cfg, stores.History{})
		dec := gate(context.Background(), types.ToolUse{ID: "wipe", Name: "wipe"})
		if dec.Outcome != runtime.BatchAsk {
			t.Fatalf("outcome = %v, want ask", dec.Outcome)
		}
	})

	t.Run("nil decider keeps the defaults", func(t *testing.T) {
		gate := nativeBatchGate(gateTestConfig(nil), stores.History{})
		for name, want := range map[string]runtime.BatchOutcome{
			"search": runtime.BatchAllow,
			"cache":  runtime.BatchAllow,
			"wipe":   runtime.BatchAsk,
		} {
			dec := gate(context.Background(), types.ToolUse{ID: name, Name: name})
			if dec.Outcome != want {
				t.Fatalf("%s outcome = %v, want %v", name, dec.Outcome, want)
			}
		}
	})

	t.Run("unknown tool denies even with a decider", func(t *testing.T) {
		cfg := gateTestConfig(funcDecider(func(ctx context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
			return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
		}))
		gate := nativeBatchGate(cfg, stores.History{})
		dec := gate(context.Background(), types.ToolUse{ID: "ghost", Name: "ghost"})
		if dec.Outcome != runtime.BatchDeny {
			t.Fatalf("outcome = %v, want deny", dec.Outcome)
		}
	})
}
