package adkgo

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// replayTool parks on its first call and approves afterwards, recording
// every observed call id through the ToolUse context value.
type replayTool struct {
	calls int
	ids   []string
}

func (t *replayTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "approve", Risk: types.RiskHigh, Effect: types.SideEffect}
}

func (t *replayTool) Call(ctx context.Context, _ json.RawMessage) (types.ToolResult, error) {
	t.calls++
	use, _ := types.ToolUseFrom(ctx)
	t.ids = append(t.ids, use.ID)
	if t.calls == 1 {
		return types.ToolResult{}, &types.SuspendError{
			Reason: types.HumanApproval,
			Payload: []types.ToolUse{{
				ID:   "call_approve",
				Name: "approve",
				Args: json.RawMessage(`{"n":"1"}`),
			}},
		}
	}
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

// countModel counts Generate invocations over its scripted inner model.
type countModel struct {
	inner types.Model
	calls int
}

func (m *countModel) Profile() types.ModelProfile { return m.inner.Profile() }

func (m *countModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.calls++
	return m.inner.Generate(ctx, req)
}

func assembleIdentity(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	return types.ModelRequest{Messages: in.History}, nil
}

func TestReplayFallback(t *testing.T) {
	t.Run("engines.adkgo-replay-fallback", func(t *testing.T) {
		tool := &replayTool{}
		scripted := gohantest.NewScriptedModel(conformanceProfile,
			gohantest.ToolCall("approve", map[string]string{"n": "1"}),
			gohantest.Text("done"),
		)
		model := &countModel{inner: scripted}
		rt, err := New(context.Background())
		if err != nil {
			t.Fatalf("New error = %v", err)
		}
		run := runtime.AgentRun{Model: model, Tools: []types.Tool{tool}, Assemble: assembleIdentity}
		st, err := rt.Start(context.Background(), run)
		if err != nil {
			t.Fatalf("Start error = %v", err)
		}

		// Park records the step result.
		var (
			se       *types.SuspendError
			evs      []types.Event
			done     types.Done
			doneSeen bool
		)
		st, _, status, err := rt.Step(context.Background(), st)
		if !errors.As(err, &se) {
			t.Fatalf("Step err = %v, want a typed SuspendError", err)
		}
		if se.Reason != types.HumanApproval {
			// Shipped defect: drain reconstructs the SuspendError from
			// the recorded payload and drops the governed Reason.
			t.Errorf("park reason = %q, want %q", se.Reason, types.HumanApproval)
		}
		if status != runtime.Continue {
			t.Fatalf("status = %v, want continue", status)
		}
		if len(st.Pending) != 1 || st.Pending[0].ID != "call_approve" || st.Pending[0].Name != "approve" {
			t.Fatalf("pending = %+v, want the parked call_approve in order", st.Pending)
		}
		var cp backendCheckpoint
		if err := json.Unmarshal(st.Backend, &cp); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		if cp.Version != "adkgo-v2" {
			t.Fatalf("envelope version = %q, want adkgo-v2", cp.Version)
		}
		if cp.Turn != st.Turn {
			t.Fatalf("envelope turn = %d, want %d", cp.Turn, st.Turn)
		}
		last := cp.Msgs[len(cp.Msgs)-1]
		if last.Role != types.RoleAssistant {
			t.Fatalf("last checkpoint message role = %q, want assistant", last.Role)
		}
		var carries bool
		for _, b := range last.Blocks {
			if u, ok := b.(types.ToolUse); ok && u.ID == "call_approve" {
				carries = true
			}
		}
		if !carries {
			t.Fatalf("last checkpoint message %+v carries no pending call_approve", last)
		}

		// The table names the capabilities it covers.
		for _, f := range DeclaredFallbacks() {
			if err := requireFallback(f.Capability); err != nil {
				t.Errorf("requireFallback(%q) error = %v, want declared", f.Capability, err)
			}
		}
		if err := requireFallback("undeclared-x"); !errors.Is(err, ErrUndeclaredFallback) {
			t.Errorf("requireFallback(undeclared-x) error = %v, want ErrUndeclaredFallback wrap", err)
		}

		parked := st
		// Re-execution reproduces the State sequence: the pending calls
		// replay under their recorded ids before any model call.
		fresh, err := New(context.Background())
		if err != nil {
			t.Fatalf("fresh New error = %v", err)
		}
		if _, err := fresh.Start(context.Background(), run); err != nil {
			t.Fatalf("fresh Start error = %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		steps := 0
		for {
			before := model.calls
			st, evs, status, err = fresh.Step(ctx, st)
			if err != nil {
				if steps == 0 && errors.As(err, new(*types.SuspendError)) {
					t.Fatalf("replay parked again: %v", err)
				}
				t.Fatalf("replay Step error = %v", err)
			}
			for _, ev := range evs {
				if d, ok := ev.(types.Done); ok {
					done, doneSeen = d, true
				}
			}
			steps++
			if len(st.Pending) > 0 {
				if model.calls != before {
					t.Fatalf("model calls %d during pending replay, want none", model.calls-before)
				}
				continue
			}
			if status != runtime.DoneStatus {
				continue
			}
			break
		}
		if tool.calls != 2 || len(tool.ids) != 2 || tool.ids[1] != "call_approve" {
			t.Fatalf("tool calls %d ids %v, want the replayed call under the recorded id call_approve", tool.calls, tool.ids)
		}
		if model.calls != 2 {
			t.Fatalf("model calls = %d, want one per run across park and replay", model.calls)
		}
		if !doneSeen {
			t.Fatal("final Step events carry no Done")
		}
		if done.Reason != types.StopCompleted {
			t.Fatalf("done reason = %q, want %q", done.Reason, types.StopCompleted)
		}

		// A v1 envelope predates native resume: the declared fingerprint
		// fallback keeps the Start history and the run still completes.
		v1, err := json.Marshal(map[string]any{
			"version": "adkgo-v1",
			"msgs":    cp.Msgs[:1],
			"turn":    0,
		})
		if err != nil {
			t.Fatalf("encode v1 envelope: %v", err)
		}
		parked.Backend = v1
		legacy, err := New(context.Background())
		if err != nil {
			t.Fatalf("legacy New error = %v", err)
		}
		legacyRun := runtime.AgentRun{
			Model:    gohantest.NewScriptedModel(conformanceProfile, gohantest.Text("done")),
			Tools:    []types.Tool{tool},
			Assemble: assembleIdentity,
		}
		if _, err := legacy.Start(context.Background(), legacyRun); err != nil {
			t.Fatalf("legacy Start error = %v", err)
		}
		for {
			_, _, status, err = legacy.Step(context.Background(), parked)
			if err != nil {
				t.Fatalf("v1 replay Step error = %v", err)
			}
			if status == runtime.DoneStatus {
				break
			}
		}
		if !slicesPrefix(legacy.msgs, append(append([]types.Message(nil), run.History.Messages...), run.Input...)) {
			t.Fatalf("v1 restore rebuilt history %+v, want the Start history first", legacy.msgs)
		}
		if tool.calls != 3 {
			t.Fatalf("tool calls = %d, want the v1 replay to execute the pending call once more", tool.calls)
		}

		// A cancelled context stops the parked replay before any call.
		cancelled, stop := context.WithCancel(context.Background())
		stop()
		callsBefore := tool.calls
		_, _, _, err = fresh.Step(cancelled, st)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled Step error = %v, want context.Canceled", err)
		}
		if tool.calls != callsBefore {
			t.Fatalf("tool calls moved to %d under a cancelled ctx, want none", tool.calls)
		}
	})
}

func slicesPrefix(msgs, prefix []types.Message) bool {
	if len(msgs) < len(prefix) {
		return false
	}
	for i, m := range prefix {
		if msgs[i].ID != m.ID {
			return false
		}
	}
	return true
}
