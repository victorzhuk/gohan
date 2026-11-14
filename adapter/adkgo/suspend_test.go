package adkgo

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

type suspendProbeKey struct{}

// suspendBridgeTool parks on the first call and succeeds on the retry,
// recording the probe context value each call observed.
type suspendBridgeTool struct {
	calls    int
	probeGot []any
}

func (t *suspendBridgeTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "confirm", Risk: types.RiskHigh, Effect: types.SideEffect}
}

func (t *suspendBridgeTool) Call(ctx context.Context, _ json.RawMessage) (types.ToolResult, error) {
	t.calls++
	t.probeGot = append(t.probeGot, ctx.Value(suspendProbeKey{}))
	if t.calls == 1 {
		return types.ToolResult{}, &types.SuspendError{
			Reason: types.HumanApproval,
			Payload: []types.ToolUse{{
				ID:   "call_confirm",
				Name: "confirm",
				Args: jsontext.Value(`{"n":"1"}`),
			}},
		}
	}
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

func suspendBridgeRun(m types.Model, tools ...types.Tool) runtime.AgentRun {
	return runtime.AgentRun{
		Model: m,
		Tools: tools,
		Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{Messages: in.History}, nil
		},
	}
}

func TestSuspendBridge(t *testing.T) {
	t.Run("engines.adkgo-suspend-bridge", func(t *testing.T) {
		tool := &suspendBridgeTool{}
		m := gohantest.NewScriptedModel(conformanceProfile,
			gohantest.ToolCall("confirm", map[string]string{"n": "1"}),
			gohantest.Text("done"),
		)
		ctx := context.WithValue(context.Background(), suspendProbeKey{}, "probe")
		rt, err := New(ctx)
		if err != nil {
			t.Fatal(err)
		}
		st, err := rt.Start(ctx, suspendBridgeRun(m, tool))
		if err != nil {
			t.Fatal(err)
		}

		// Park
		st, evs, status, err := rt.Step(ctx, st)
		var se *types.SuspendError
		if !errors.As(err, &se) {
			t.Fatalf("first step err = %v, want a *types.SuspendError", err)
		}
		if se.Reason != types.HumanApproval {
			t.Fatalf("suspend reason = %q, want human_approval", se.Reason)
		}
		if status != runtime.Continue {
			t.Fatalf("status = %v, want continue", status)
		}
		if len(evs) != 0 {
			t.Fatalf("events = %v, want none before the driver suspends", evs)
		}
		if len(st.Pending) != 1 || st.Pending[0].ID != "call_confirm" || st.Pending[0].Name != "confirm" {
			t.Fatalf("pending = %+v, want the parked confirm call", st.Pending)
		}
		var cp backendCheckpoint
		if err := json.Unmarshal(st.Backend, &cp); err != nil {
			t.Fatalf("backend checkpoint: %v", err)
		}
		if cp.Version != "adkgo-v2" {
			t.Fatalf("checkpoint version = %q, want adkgo-v2", cp.Version)
		}
		if cp.Turn != st.Turn {
			t.Fatalf("checkpoint turn = %d, want %d", cp.Turn, st.Turn)
		}
		if len(cp.InterruptIDs) == 0 || cp.InterruptIDs[0] != "call_confirm" {
			t.Fatalf("interrupt ids = %v, want the parked call id", cp.InterruptIDs)
		}
		if tool.calls != 1 {
			t.Fatalf("tool calls = %d, want 1 before resume", tool.calls)
		}
		modelCallsAfterPark := len(m.Requests())
		if modelCallsAfterPark != 1 {
			t.Fatalf("model calls after park = %d, want 1", modelCallsAfterPark)
		}

		// Resume: adk routes the resumed interrupt input only to llmagent
		// roots, so this custom-agent backend serves the approved call by
		// replay; the follow-up turn settles in the same Step once the
		// batch drains.
		st, evs, status, err = rt.Step(ctx, st)
		if err != nil {
			t.Fatalf("resume step: %v", err)
		}
		if tool.calls != 2 {
			t.Fatalf("tool calls = %d, want the approved call re-executed once", tool.calls)
		}
		if len(st.Pending) != 0 {
			t.Fatalf("pending = %+v, want drained", st.Pending)
		}
		if got := len(m.Requests()); got != modelCallsAfterPark+1 {
			t.Fatalf("model calls after resume = %d, want %d (no regeneration of the tool turn)", got, modelCallsAfterPark+1)
		}
		if st.Turn != 1 {
			t.Fatalf("turn = %d, want 1 after the resumed turn", st.Turn)
		}

		// Turn boundary lands with the resume Step's follow-up turn.
		if status != runtime.DoneStatus {
			t.Fatalf("status = %v, want done", status)
		}
		var text types.AssistantMessage
		var done types.Done
		foundText := false
		for _, ev := range evs {
			switch e := ev.(type) {
			case types.AssistantMessage:
				text, foundText = e, true
			case types.Done:
				done = e
			}
		}
		if !foundText {
			t.Fatalf("events = %v, want the assistant text message as a value types.AssistantMessage", evs)
		}
		if text.Message.Role != types.RoleAssistant {
			t.Fatalf("assistant message = %+v, want the follow-up turn assistant message", text)
		}
		if done.Reason != types.StopCompleted {
			t.Fatalf("done = %+v, want completed", done)
		}
		if got := len(m.Requests()); got != 2 {
			t.Fatalf("model calls = %d, want 2, one per executed turn", got)
		}

		// Context propagation: the run ctx reached both calls unchanged.
		for i, got := range tool.probeGot {
			if got != "probe" {
				t.Fatalf("call %d probe value = %v, want the seeded run ctx value", i+1, got)
			}
		}
		if len(tool.probeGot) != 2 {
			t.Fatalf("probe values = %d, want one per tool call", len(tool.probeGot))
		}
	})
}
