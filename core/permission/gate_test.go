package permission

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type decideFunc func(ctx context.Context, inv *ToolInvocation) (types.Decision[Verdict], error)

func (f decideFunc) Decide(ctx context.Context, inv *ToolInvocation) (types.Decision[Verdict], error) {
	return f(ctx, inv)
}

func gateSpec(specs ...types.ToolSpec) func(string) (types.ToolSpec, bool) {
	byName := make(map[string]types.ToolSpec, len(specs))
	for _, s := range specs {
		byName[s.Name] = s
	}
	return func(name string) (types.ToolSpec, bool) {
		s, ok := byName[name]
		return s, ok
	}
}

func gateRun(subject string, scopes ...string) func(context.Context) types.RunInfo {
	return func(context.Context) types.RunInfo {
		return types.RunInfo{SessionID: "s1", Principal: types.Principal{Subject: subject, Scopes: scopes}}
	}
}

func TestPermissionGate(t *testing.T) {
	ctx := context.Background()
	booking := types.ToolSpec{
		Name:           "create_booking",
		Effect:         types.SideEffect,
		RequiredScopes: []string{"booking:write"},
	}
	read := types.ToolSpec{Name: "search", Effect: types.ReadOnly}

	t.Run("chains.scope-before-decider", func(t *testing.T) {
		consulted := false
		decider := decideFunc(func(context.Context, *ToolInvocation) (types.Decision[Verdict], error) {
			consulted = true
			return types.Decision[Verdict]{Value: Allow, Confidence: 1}, nil
		})
		gate := Gate(decider,
			WithSpecLookup(gateSpec(booking)),
			WithRunInfo(gateRun("op")),
		)
		ran := false
		res, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			ran = true
			return types.ToolResult{Outcome: types.Succeeded}, nil
		})(ctx, types.ToolUse{ID: "c1", Name: "create_booking"})
		if err != nil {
			t.Fatalf("scope denial returned error: %v", err)
		}
		if res.Outcome != types.Failed {
			t.Fatalf("Outcome = %v, want %v", res.Outcome, types.Failed)
		}
		if res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("Error = %v, want permanent denial", res.Error)
		}
		if ran {
			t.Fatal("denied call must not reach the tool")
		}
		if consulted {
			t.Fatal("decider must not be consulted when scopes fail")
		}
	})

	t.Run("chains.denied-call-not-journaled", func(t *testing.T) {
		journal := stores.NewMemoryJournal()
		gate := Gate(nil,
			WithSpecLookup(gateSpec(booking)),
			WithRunInfo(gateRun("op")),
		)
		res, _ := gate(func(_ context.Context, _ types.ToolUse) (types.ToolResult, error) {
			key := types.CallKey{SessionID: "s1", CallID: "c1"}
			if _, _, err := journal.Reserve(ctx, key, "fp-booking"); err != nil {
				return types.ToolResult{}, err
			}
			return types.ToolResult{Outcome: types.Succeeded}, nil
		})(ctx, types.ToolUse{ID: "c1", Name: "create_booking"})
		if res.Outcome != types.Failed {
			t.Fatalf("Outcome = %v, want %v", res.Outcome, types.Failed)
		}
		entries, err := journal.ByFingerprint(ctx, "s1", "fp-booking")
		if err != nil {
			t.Fatalf("ByFingerprint: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("denied call left %d journal entries, want 0", len(entries))
		}
	})

	t.Run("decider.failure-defaults-closed", func(t *testing.T) {
		decider := decideFunc(func(context.Context, *ToolInvocation) (types.Decision[Verdict], error) {
			return types.Decision[Verdict]{}, errors.New("decider offline")
		})
		gate := Gate(decider,
			WithSpecLookup(gateSpec(read)),
			WithRunInfo(gateRun("op")),
		)
		_, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			return types.ToolResult{Outcome: types.Succeeded}, nil
		})(ctx, types.ToolUse{ID: "c2", Name: "search"})
		var ask *AskError
		if !errors.As(err, &ask) {
			t.Fatalf("err = %v, want AskError", err)
		}
		if !errors.Is(err, ErrApprovalRequired) {
			t.Fatalf("err = %v, want %v in chain", err, ErrApprovalRequired)
		}
		if ask.Invocation.Spec.Name != "search" {
			t.Fatalf("Invocation.Spec.Name = %q, want %q", ask.Invocation.Spec.Name, "search")
		}
	})

	t.Run("side effect asks without decider", func(t *testing.T) {
		gate := Gate(nil,
			WithSpecLookup(gateSpec(types.ToolSpec{Name: "pay", Effect: types.SideEffect})),
			WithRunInfo(gateRun("op")),
		)
		_, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			t.Fatal("SideEffect must not execute before approval")
			return types.ToolResult{}, nil
		})(ctx, types.ToolUse{ID: "c3", Name: "pay"})
		if !errors.Is(err, ErrApprovalRequired) {
			t.Fatalf("err = %v, want %v", err, ErrApprovalRequired)
		}
	})

	t.Run("read only allows without decider", func(t *testing.T) {
		gate := Gate(nil,
			WithSpecLookup(gateSpec(read)),
			WithRunInfo(gateRun("op")),
		)
		res, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			return types.ToolResult{Outcome: types.Succeeded}, nil
		})(ctx, types.ToolUse{ID: "c4", Name: "search"})
		if err != nil {
			t.Fatalf("read-only call returned error: %v", err)
		}
		if res.Outcome != types.Succeeded {
			t.Fatalf("Outcome = %v, want %v", res.Outcome, types.Succeeded)
		}
	})

	t.Run("low confidence asks", func(t *testing.T) {
		decider := decideFunc(func(context.Context, *ToolInvocation) (types.Decision[Verdict], error) {
			return types.Decision[Verdict]{Value: Allow, Confidence: 0.4}, nil
		})
		gate := Gate(decider,
			MinConfidence(0.9),
			WithSpecLookup(gateSpec(read)),
			WithRunInfo(gateRun("op")),
		)
		_, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			return types.ToolResult{Outcome: types.Succeeded}, nil
		})(ctx, types.ToolUse{ID: "c5", Name: "search"})
		if !errors.Is(err, ErrApprovalRequired) {
			t.Fatalf("err = %v, want %v", err, ErrApprovalRequired)
		}
	})

	t.Run("taint deny fails the call", func(t *testing.T) {
		hook := func(context.Context, types.ToolSpec, jsontext.Value) (types.TaintAction, []types.ArgTaint) {
			return types.TaintDeny, []types.ArgTaint{{Arg: "note", Origins: []types.Origin{{Kind: types.OriginTool, Name: "web"}}}}
		}
		gate := Gate(nil,
			WithTaintHook(hook),
			WithSpecLookup(gateSpec(read)),
			WithRunInfo(gateRun("op")),
		)
		res, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			t.Fatal("tainted call must not execute")
			return types.ToolResult{}, nil
		})(ctx, types.ToolUse{ID: "c6", Name: "search"})
		if err != nil {
			t.Fatalf("taint denial returned error: %v", err)
		}
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("res = %+v, want permanent failure", res)
		}
	})

	t.Run("taint ask forces suspension over decider", func(t *testing.T) {
		hook := func(context.Context, types.ToolSpec, jsontext.Value) (types.TaintAction, []types.ArgTaint) {
			return types.TaintAsk, nil
		}
		decider := decideFunc(func(context.Context, *ToolInvocation) (types.Decision[Verdict], error) {
			return types.Decision[Verdict]{Value: Allow, Confidence: 1}, nil
		})
		gate := Gate(decider,
			WithTaintHook(hook),
			WithSpecLookup(gateSpec(read)),
			WithRunInfo(gateRun("op")),
		)
		_, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			return types.ToolResult{Outcome: types.Succeeded}, nil
		})(ctx, types.ToolUse{ID: "c7", Name: "search"})
		var ask *AskError
		if !errors.As(err, &ask) {
			t.Fatalf("err = %v, want AskError", err)
		}
	})
}
