package permission

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestLiveControlPrecedesTaintAndDecider(t *testing.T) {
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
			WithLiveControl(func(context.Context, *ToolInvocation) (types.TaintAction, string) {
				return types.TaintDeny, "emergency stop"
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
		decision := DecideCall(ctx, decider, &ToolInvocation{Spec: spec}, WithLiveControl(func(context.Context, *ToolInvocation) (types.TaintAction, string) {
			return types.TaintDeny, "emergency stop"
		}))
		if decision.Verdict != DenyVerdict || decision.Reason != "emergency stop" {
			t.Fatalf("decision = %+v, want live denial", decision)
		}
		if deciderCalled {
			t.Fatal("decider ran after live deny")
		}
	})

	t.Run("plain ask forces approval", func(t *testing.T) {
		gate := Gate(nil,
			WithSpecLookup(gateSpec(spec)),
			WithLiveControl(func(context.Context, *ToolInvocation) (types.TaintAction, string) {
				return types.TaintAsk, "needs human confirmation"
			}),
		)
		_, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			t.Fatal("asked call reached tool")
			return types.ToolResult{}, nil
		})(ctx, types.ToolUse{ID: "c2", Name: spec.Name})
		var ask *AskError
		if !errors.As(err, &ask) {
			t.Fatalf("err = %v, want AskError", err)
		}
	})

	t.Run("control wait suspends with empty wake and arms the clock", func(t *testing.T) {
		gate := Gate(nil,
			WithSpecLookup(gateSpec(spec)),
			WithControlWait(3*time.Second),
			WithLiveControl(func(context.Context, *ToolInvocation) (types.TaintAction, string) {
				return types.TaintAsk, ControlWaitReason
			}),
		)
		_, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			t.Fatal("waiting call reached tool")
			return types.ToolResult{}, nil
		})(ctx, types.ToolUse{ID: "c3", Name: spec.Name})
		var susp *types.SuspendError
		if !errors.As(err, &susp) {
			t.Fatalf("err = %v, want AwaitingControl suspension", err)
		}
		if susp.Reason != types.AwaitingControl {
			t.Fatalf("reason = %v, want AwaitingControl", susp.Reason)
		}
		if susp.WakeAt.IsZero() || time.Until(susp.WakeAt) > 3*time.Second {
			t.Fatalf("wake at %v, want within the control wait", susp.WakeAt)
		}
	})

	t.Run("allow never widens a hard deny", func(t *testing.T) {
		decision := DecideCall(ctx, nil, &ToolInvocation{
			Spec: types.ToolSpec{Name: "deploy", Effect: types.SideEffect, RequiredScopes: []string{"deploy:run"}},
		}, WithLiveControl(func(context.Context, *ToolInvocation) (types.TaintAction, string) {
			return types.TaintAllow, ""
		}))
		if decision.Verdict != DenyVerdict {
			t.Fatalf("decision = %+v, want the scope denial to stand", decision)
		}
	})
}

func TestNilLiveControlPreservesTaintAndDecider(t *testing.T) {
	ctx := context.Background()
	spec := types.ToolSpec{Name: "search", Effect: types.ReadOnly}
	lookup := func(context.Context, *ToolInvocation) (types.TaintAction, string) { return types.TaintAllow, "" }

	t.Run("taint ask", func(t *testing.T) {
		gate := Gate(nil,
			WithSpecLookup(gateSpec(spec)),
			WithLiveControl(lookup),
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
		decision := DecideCall(ctx, decider, &ToolInvocation{Spec: spec}, WithLiveControl(lookup))
		if decision.Verdict != Allow {
			t.Fatalf("decision = %+v, want allow", decision)
		}
	})
}
