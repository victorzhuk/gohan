package gohan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
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
		Calls: calls,
		Limits: types.RunLimits{MaxToolCalls: 100},
		Gate: gate,
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

func TestNativeBatchGateDecider(t *testing.T) {
	t.Run("denied side effect never executes", func(t *testing.T) {
		cfg := gateTestConfig(funcDecider(func(ctx context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
			if inv.Spec.Effect != types.SideEffect {
				return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
			}
			return types.Decision[permission.Verdict]{Value: permission.DenyVerdict, Confidence: 1}, nil
		}))
		report, executed := runGatedBatch(t, nativeBatchGate(cfg), "search", "wipe")
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
		gate := nativeBatchGate(cfg)
		dec := gate(context.Background(), types.ToolUse{ID: "wipe", Name: "wipe"})
		if dec.Outcome != runtime.BatchAsk {
			t.Fatalf("outcome = %v, want ask", dec.Outcome)
		}
	})

	t.Run("nil decider keeps the defaults", func(t *testing.T) {
		gate := nativeBatchGate(gateTestConfig(nil))
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
		gate := nativeBatchGate(cfg)
		dec := gate(context.Background(), types.ToolUse{ID: "ghost", Name: "ghost"})
		if dec.Outcome != runtime.BatchDeny {
			t.Fatalf("outcome = %v, want deny", dec.Outcome)
		}
	})
}
