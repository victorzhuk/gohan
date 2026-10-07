package eino

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

func TestRuntimeConstruction(t *testing.T) {
	t.Run("new requires agent", func(t *testing.T) {
		if _, err := New(context.Background(), nil); err == nil {
			t.Fatal("New(nil) = nil error, want nil-agent error")
		}
	})

	t.Run("name and granularity", func(t *testing.T) {
		rt, err := New(context.Background(), &adk.ChatModelAgent{})
		if err != nil {
			t.Fatalf("new runtime: %v", err)
		}
		if rt.Name() != "eino" {
			t.Fatalf("Name = %q", rt.Name())
		}
		if got := rt.Granularity(); got != runtime.GranularityTurn {
			t.Fatalf("Granularity = %v, want turn", got)
		}
	})

	t.Run("compile-time port assertion", func(t *testing.T) {
		var _ runtime.Runtime = (*Runtime)(nil)
	})

	t.Run("runtime.batch-ask-after-allowed", func(t *testing.T) {
		ctx := context.Background()
		left := gohantest.NewFakeTool("left")
		ask := &parkTool{}
		first := gohantest.NewScriptedModel(conformanceProfile,
			gohantest.ToolUses(callUse("left"), callUse("confirm")),
		)
		rt, err := New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		st, err := rt.Start(ctx, suspendAgentRun(first, left, ask))
		if err != nil {
			t.Fatal(err)
		}
		st, _, _, err = rt.Step(ctx, st)
		var se *types.SuspendError
		if !errors.As(err, &se) || se.Reason != types.HumanApproval {
			t.Fatalf("first step err = %v, want a human approval park", err)
		}
		if len(left.Calls()) != 1 {
			t.Fatalf("allowed call executions = %d, want 1 before the park", len(left.Calls()))
		}
		var cp backendCheckpoint
		if err := json.Unmarshal(st.Backend, &cp); err != nil {
			t.Fatalf("backend checkpoint: %v", err)
		}
		pending := pendingCall(cp.Msgs, "confirm")
		if pending.ID == "" {
			t.Fatal("checkpoint history carries no pending confirm call")
		}

		resumed, done := resumeRejected(t, ctx, cp.Msgs, pending, left, ask)
		_ = resumed
		if !done {
			t.Fatal("resume run did not complete")
		}
		if len(left.Calls()) != 1 {
			t.Fatalf("allowed call executions = %d, want exactly 1, no re-execution", len(left.Calls()))
		}
		if ask.calls != 1 {
			t.Fatalf("ask executions = %d, want only the parked attempt", ask.calls)
		}
	})

	t.Run("runtime.batch-one-result-per-call", func(t *testing.T) {
		ctx := context.Background()
		left := gohantest.NewFakeTool("left")
		ask := &parkTool{}
		first := gohantest.NewScriptedModel(conformanceProfile,
			gohantest.ToolUses(callUse("left"), callUse("confirm")),
		)
		rt, err := New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		st, err := rt.Start(ctx, suspendAgentRun(first, left, ask))
		if err != nil {
			t.Fatal(err)
		}
		st, _, _, err = rt.Step(ctx, st)
		var se *types.SuspendError
		if !errors.As(err, &se) {
			t.Fatalf("first step err = %v, want a park", err)
		}
		var cp backendCheckpoint
		if err := json.Unmarshal(st.Backend, &cp); err != nil {
			t.Fatalf("backend checkpoint: %v", err)
		}
		pending := pendingCall(cp.Msgs, "confirm")
		if pending.ID == "" {
			t.Fatal("checkpoint history carries no pending confirm call")
		}
		resumed, done := resumeRejected(t, ctx, cp.Msgs, pending, left, ask)
		if !done {
			t.Fatal("resume run did not complete")
		}
		results := map[string]int{}
		for _, m := range resumed.msgs {
			for _, b := range m.Blocks {
				if res, ok := b.(types.ToolResult); ok {
					results[res.ID]++
				}
			}
		}
		if results["call_left"] != 1 || results[pending.ID] != 1 {
			t.Fatalf("results per call = %v, want exactly one for each of call_left and %s", results, pending.ID)
		}
		if res, ok := resultFor(resumed.msgs, pending.ID); !ok ||
			res.Outcome != types.Failed || res.Error == nil ||
			res.Error.Kind != types.Permanent ||
			res.Error.Message != runtime.NotExecutedPrefix+"rejected by owner" {
			t.Fatalf("rejected result = %+v, want one permanent %srejected by owner", res, runtime.NotExecutedPrefix)
		}
	})
}

// pendingCall finds the pending tool call a checkpoint history carries.
func pendingCall(msgs []types.Message, name string) types.ToolUse {
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if u, ok := b.(types.ToolUse); ok && u.Name == name {
				return u
			}
		}
	}
	return types.ToolUse{}
}

// resultFor returns the tool result a message history carries for id.
func resultFor(msgs []types.Message, id string) (types.ToolResult, bool) {
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if res, ok := b.(types.ToolResult); ok && res.ID == id {
				return res, true
			}
		}
	}
	return types.ToolResult{}, false
}

// resumeRejected emulates the driver's resume on a rejected ask: the
// journaled allowed result and the permanent not_executed rejection ride
// the reloaded history, and the checkpointed position re-drives.
func resumeRejected(t *testing.T, ctx context.Context, msgs []types.Message, pending types.ToolUse, tools ...types.Tool) (*Runtime, bool) {
	t.Helper()
	hist := append(slices.Clone(msgs),
		types.Message{Role: types.RoleAssistant, Blocks: []types.Block{
			types.ToolResult{ID: "call_left", Outcome: types.Succeeded},
		}},
		types.Message{Role: types.RoleAssistant, Blocks: []types.Block{
			types.ToolResult{
				ID:      pending.ID,
				Outcome: types.Failed,
				Error: &types.ToolError{
					Kind:    types.Permanent,
					Message: runtime.NotExecutedPrefix + "rejected by owner",
				},
			},
		}},
	)
	final := gohantest.NewScriptedModel(conformanceProfile, gohantest.Text("done"))
	rt, err := New(ctx, &adk.ChatModelAgent{})
	if err != nil {
		t.Fatal(err)
	}
	ag := suspendAgentRun(final, tools...)
	ag.History = stores.History{Messages: hist}
	if _, err := rt.Start(ctx, ag); err != nil {
		t.Fatal(err)
	}
	st := runtime.State{}
	for range 4 {
		var status runtime.Status
		var done bool
		st, _, status, err = rt.Step(ctx, st)
		if err != nil {
			t.Fatalf("resume step: %v", err)
		}
		if status == runtime.DoneStatus {
			done = true
		}
		if done {
			return rt, true
		}
	}
	return rt, false
}
