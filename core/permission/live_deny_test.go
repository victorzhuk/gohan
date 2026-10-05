package permission

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestLiveDenyPrecedesTaintAndDecider(t *testing.T) {
	ctx := context.Background()
	spec := types.ToolSpec{Name: "search", Effect: types.ReadOnly}

	t.Run("deny precedes taint ask", func(t *testing.T) {
		hookCalled := false
		gate := Gate(nil,
			WithSpecLookup(gateSpec(spec)),
			WithTaintHook(func(context.Context, types.ToolSpec, jsontext.Value) (types.TaintAction, []types.ArgTaint) {
				hookCalled = true
				return types.TaintAsk, nil
			}),
			WithLiveDeny(func(context.Context, *ToolInvocation) (string, bool) {
				return "emergency stop", true
			}),
		)
		result, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			t.Fatal("denied call reached tool")
			return types.ToolResult{}, nil
		})(ctx, types.ToolUse{ID: "c1", Name: spec.Name})
		if err != nil {
			t.Fatalf("err = %v, want no approval ask", err)
		}
		if hookCalled {
			t.Fatal("taint hook ran after live deny")
		}
		if result.Outcome != types.Failed || result.Error == nil || result.Error.Message != "tool search denied: emergency stop" {
			t.Fatalf("result = %+v, want failed result with live-deny reason", result)
		}
	})

	t.Run("deny precedes decider allow", func(t *testing.T) {
		deciderCalled := false
		decider := decideFunc(func(context.Context, *ToolInvocation) (types.Decision[Verdict], error) {
			deciderCalled = true
			return types.Decision[Verdict]{Value: Allow, Confidence: 1}, nil
		})
		decision := DecideCall(ctx, decider, &ToolInvocation{Spec: spec}, WithLiveDeny(func(context.Context, *ToolInvocation) (string, bool) {
			return "emergency stop", true
		}))
		if decision.Verdict != DenyVerdict || decision.Reason != "emergency stop" {
			t.Fatalf("decision = %+v, want live denial", decision)
		}
		if deciderCalled {
			t.Fatal("decider ran after live deny")
		}
	})
}

func TestNilLiveDenyPreservesTaintAndDecider(t *testing.T) {
	ctx := context.Background()
	spec := types.ToolSpec{Name: "search", Effect: types.ReadOnly}
	lookup := func(context.Context, *ToolInvocation) (string, bool) { return "", false }

	t.Run("taint ask", func(t *testing.T) {
		gate := Gate(nil,
			WithSpecLookup(gateSpec(spec)),
			WithLiveDeny(lookup),
			WithTaintHook(func(context.Context, types.ToolSpec, jsontext.Value) (types.TaintAction, []types.ArgTaint) {
				return types.TaintAsk, nil
			}),
		)
		_, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			t.Fatal("taint ask reached tool")
			return types.ToolResult{}, nil
		})(ctx, types.ToolUse{ID: "c2", Name: spec.Name})
		var ask *AskError
		if !errors.As(err, &ask) {
			t.Fatalf("err = %v, want AskError", err)
		}
	})

	t.Run("decider allow", func(t *testing.T) {
		decider := decideFunc(func(context.Context, *ToolInvocation) (types.Decision[Verdict], error) {
			return types.Decision[Verdict]{Value: Allow, Confidence: 1}, nil
		})
		decision := DecideCall(ctx, decider, &ToolInvocation{Spec: spec}, WithLiveDeny(lookup))
		if decision.Verdict != Allow {
			t.Fatalf("decision = %+v, want allow", decision)
		}
	})
}
