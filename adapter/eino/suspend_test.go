package eino

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

type parkTool struct {
	calls int
}

func (p *parkTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "confirm", Risk: types.RiskHigh, Effect: types.SideEffect}
}

func (p *parkTool) Call(_ context.Context, _ json.RawMessage) (types.ToolResult, error) {
	p.calls++
	if p.calls == 1 {
		return types.ToolResult{}, &types.SuspendError{Reason: types.HumanApproval}
	}
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

func suspendModel() types.Model {
	return gohantest.NewScriptedModel(conformanceProfile,
		gohantest.ToolCall("confirm", map[string]string{"n": "1"}),
		gohantest.Text("done"),
	)
}

func suspendAgentRun(m types.Model, tools ...types.Tool) runtime.AgentRun {
	return runtime.AgentRun{
		Model: m,
		Tools: tools,
		Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{Messages: in.History}, nil
		},
	}
}

func TestSuspend(t *testing.T) {
	t.Run("runtime.suspend-order", func(t *testing.T) {
		tool := &parkTool{}
		rt, err := New(context.Background(), &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		if rt.Granularity() != runtime.GranularityTurn {
			t.Fatalf("Granularity = %v, want turn", rt.Granularity())
		}
		st, err := rt.Start(context.Background(), suspendAgentRun(suspendModel(), tool))
		if err != nil {
			t.Fatal(err)
		}
		st, evs, status, err := rt.Step(context.Background(), st)
		var se *types.SuspendError
		if !errors.As(err, &se) || se.Reason != types.HumanApproval {
			t.Fatalf("Step err = %v, want a human approval park", err)
		}
		if status != runtime.Continue {
			t.Fatalf("status = %v, want continue", status)
		}
		if len(evs) != 0 {
			t.Fatalf("events = %v, want none before the driver suspends", evs)
		}
		var cp backendCheckpoint
		if err := json.Unmarshal(st.Backend, &cp); err != nil {
			t.Fatalf("backend checkpoint: %v", err)
		}
		var pending int
		for _, m := range cp.Msgs {
			for _, b := range m.Blocks {
				if u, ok := b.(types.ToolUse); ok && u.Name == "confirm" {
					pending++
				}
			}
		}
		if pending == 0 {
			t.Fatal("checkpoint history carries no pending confirm call")
		}
	})
}
