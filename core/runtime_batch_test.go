package gohan

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

type batchDecider map[string]permission.Verdict

func (m batchDecider) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	return types.Decision[permission.Verdict]{Value: m[inv.Spec.Name], Confidence: 1}, nil
}

func taintDenyHook(_ context.Context, spec types.ToolSpec, _ jsontext.Value) (types.TaintAction, []types.ArgTaint) {
	if spec.Name == "flagged" {
		return types.TaintDeny, []types.ArgTaint{{Arg: "path", Origins: []types.Origin{{Kind: types.OriginUser}}}}
	}
	return types.TaintAllow, nil
}

func batchGate(d types.Decider[*permission.ToolInvocation, permission.Verdict]) runtime.BatchGate {
	specs := map[string]types.ToolSpec{
		"search":  {Name: "search", Effect: types.ReadOnly},
		"wipe":    {Name: "wipe", Effect: types.SideEffect},
		"flagged": {Name: "flagged", Effect: types.SideEffect},
		"confirm": {Name: "confirm", Effect: types.SideEffect},
	}
	return func(ctx context.Context, call types.ToolUse) runtime.BatchDecision {
		spec, ok := specs[call.Name]
		if !ok {
			return runtime.BatchDecision{Outcome: runtime.BatchDeny, Reason: "unknown tool"}
		}
		dec := permission.DecideCall(ctx, d, &permission.ToolInvocation{Spec: spec, Call: call},
			permission.WithTaintHook(taintDenyHook))
		switch dec.Verdict {
		case permission.Allow:
			return runtime.BatchDecision{Outcome: runtime.BatchAllow}
		case permission.DenyVerdict:
			return runtime.BatchDecision{Outcome: runtime.BatchDeny, Reason: dec.Reason}
		case permission.TaintDenied:
			return runtime.BatchDecision{Outcome: runtime.BatchTaintDenied, Reason: dec.Reason}
		default:
			return runtime.BatchDecision{Outcome: runtime.BatchAsk}
		}
	}
}

type batchRecorder struct {
	events   []string
	executed []string
}

func (r *batchRecorder) gate(g runtime.BatchGate) runtime.BatchGate {
	return func(ctx context.Context, call types.ToolUse) runtime.BatchDecision {
		r.events = append(r.events, "gate:"+call.Name)
		return g(ctx, call)
	}
}

func (r *batchRecorder) exec(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
	r.events = append(r.events, "exec:"+call.Name)
	r.executed = append(r.executed, call.Name)
	return types.ToolResult{ID: call.ID, Outcome: types.Succeeded}, nil
}

func (r *batchRecorder) run(t *testing.T, calls []types.ToolUse, limits types.RunLimits, used int) (runtime.BatchReport, error) {
	t.Helper()
	g := batchGate(batchDecider{"search": permission.Allow, "wipe": permission.DenyVerdict, "confirm": permission.Ask})
	return runtime.Batch{Calls: calls, Limits: limits, Used: used, Gate: r.gate(g), Exec: r.exec}.Run(context.Background())
}

func batchCalls(names ...string) []types.ToolUse {
	calls := make([]types.ToolUse, len(names))
	for i, name := range names {
		calls[i] = types.ToolUse{ID: name, Name: name}
	}
	return calls
}

func TestRuntimeBatchProtocol(t *testing.T) {
	bigLimits := types.RunLimits{MaxToolCalls: 100}

	t.Run("runtime.batch-gate-first", func(t *testing.T) {
		rec := &batchRecorder{}
		report, err := rec.run(t, batchCalls("wipe", "search"), bigLimits, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(report.Results) != 2 {
			t.Fatalf("got %d results, want 2", len(report.Results))
		}
		firstExec := -1
		for i, ev := range rec.events {
			if strings.HasPrefix(ev, "exec:") {
				firstExec = i
				break
			}
		}
		for _, ev := range rec.events[:firstExec] {
			if !strings.HasPrefix(ev, "gate:") {
				t.Fatalf("call executed before the batch was gated: %v", rec.events)
			}
		}
		if got := rec.events[firstExec]; got != "exec:search" {
			t.Fatalf("first execution = %q, want exec:search", got)
		}
	})

	t.Run("runtime.batch-limit-before-execute", func(t *testing.T) {
		rec := &batchRecorder{}
		_, err := rec.run(t, batchCalls("search", "wipe"), types.RunLimits{MaxToolCalls: 2}, 1)
		if !errors.Is(err, runtime.ErrBatchOverrun) {
			t.Fatalf("got %v, want ErrBatchOverrun", err)
		}
		if len(rec.executed) != 0 {
			t.Fatalf("executed = %v, want nothing executed", rec.executed)
		}
	})

	t.Run("runtime.batch-ask-after-allowed", func(t *testing.T) {
		rec := &batchRecorder{}
		report, err := rec.run(t, batchCalls("search", "confirm"), bigLimits, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report.Suspend == nil || report.Suspend.Call.Name != "confirm" {
			t.Fatalf("Suspend = %v, want confirm", report.Suspend)
		}
		if len(report.Suspend.Pending) != 1 || report.Suspend.Pending[0].Name != "confirm" {
			t.Fatalf("Pending = %v, want confirm", report.Suspend.Pending)
		}
		if len(rec.executed) != 1 || rec.executed[0] != "search" {
			t.Fatalf("executed = %v, want only search", rec.executed)
		}
		if report.Results[0].Result.Outcome != types.Succeeded {
			t.Fatalf("allowed result = %v, want %v", report.Results[0].Result.Outcome, types.Succeeded)
		}
	})
	t.Run("gates run before effects around an ask", func(t *testing.T) {
		rec := &batchRecorder{}
		report, err := rec.run(t, batchCalls("search", "confirm", "search", "wipe"), types.RunLimits{MaxToolCalls: 10}, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantEvents := []string{"gate:search", "gate:confirm", "gate:search", "gate:wipe", "exec:search", "exec:search"}
		if !slices.Equal(rec.events, wantEvents) {
			t.Fatalf("events = %v, want %v", rec.events, wantEvents)
		}
		if !slices.Equal(rec.executed, []string{"search", "search"}) {
			t.Fatalf("executed = %v, want both allowed calls", rec.executed)
		}
		if report.Suspend == nil || report.Suspend.Call.Name != "confirm" {
			t.Fatalf("Suspend = %v, want first ask confirm", report.Suspend)
		}
		if len(report.Suspend.Pending) != 1 || report.Suspend.Pending[0].Name != "confirm" ||
			report.Suspend.Pending[0].ID != "confirm" {
			t.Fatalf("pending = %v, want confirm in call order", report.Suspend.Pending)
		}
		if len(report.Results) != 3 || report.Results[0].Call.Name != "search" ||
			report.Results[1].Call.Name != "search" || report.Results[2].Call.Name != "wipe" {
			t.Fatalf("results = %+v, want both allowed results then denial in call order", report.Results)
		}
		if report.Results[2].Result.Outcome != types.Failed || report.Results[2].Result.Error.Kind != types.Permanent {
			t.Fatalf("denial = %+v, want Failed(Permanent)", report.Results[2].Result)
		}
	})

	t.Run("overrun makes zero gates", func(t *testing.T) {
		rec := &batchRecorder{}
		_, err := rec.run(t, batchCalls("search", "wipe"), types.RunLimits{MaxToolCalls: 1}, 0)
		if !errors.Is(err, runtime.ErrBatchOverrun) {
			t.Fatalf("got %v, want ErrBatchOverrun", err)
		}
		if len(rec.events) != 0 || len(rec.executed) != 0 {
			t.Fatalf("events = %v, executed = %v, want no gates or effects", rec.events, rec.executed)
		}
	})

	t.Run("runtime.batch-one-result-per-call", func(t *testing.T) {
		rec := &batchRecorder{}
		report, err := rec.run(t, batchCalls("search", "wipe", "search"), bigLimits, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report.Suspend != nil {
			t.Fatalf("unexpected suspension of %v", report.Suspend.Call.Name)
		}
		if len(report.Results) != 3 {
			t.Fatalf("got %d results, want 3", len(report.Results))
		}
		for i, want := range []string{"search", "wipe", "search"} {
			if report.Results[i].Call.Name != want {
				t.Fatalf("result %d is for %q, want %q", i, report.Results[i].Call.Name, want)
			}
		}
	})

	t.Run("denial is permanent and prefixed", func(t *testing.T) {
		rec := &batchRecorder{}
		report, err := rec.run(t, batchCalls("wipe"), bigLimits, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res := report.Results[0].Result
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("result = %+v, want Failed(Permanent)", res)
		}
		if !strings.HasPrefix(res.Error.Message, runtime.NotExecutedPrefix) {
			t.Fatalf("reason = %q, want the not_executed prefix", res.Error.Message)
		}
	})

	t.Run("taint denial is permanent and prefixed", func(t *testing.T) {
		rec := &batchRecorder{}
		report, err := rec.run(t, batchCalls("flagged"), bigLimits, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res := report.Results[0].Result
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("result = %+v, want Failed(Permanent)", res)
		}
		if !strings.HasPrefix(res.Error.Message, runtime.NotExecutedPrefix) {
			t.Fatalf("reason = %q, want the not_executed prefix", res.Error.Message)
		}
	})
}
