package eino

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

func TestResume(t *testing.T) {
	t.Run("completed_calls_answer_from_the_journal", func(t *testing.T) {
		tool := &parkTool{}
		model := gohantest.NewScriptedModel(conformanceProfile,
			gohantest.ToolCall("confirm", map[string]string{"n": "1"}),
			gohantest.ToolCall("confirm", map[string]string{"n": "2"}),
			gohantest.Text("done"),
		)
		ctx := context.Background()
		rt, err := New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		st, err := rt.Start(ctx, suspendAgentRun(model, tool))
		if err != nil {
			t.Fatal(err)
		}
		st, _, _, err = rt.Step(ctx, st)
		var se *types.SuspendError
		if !errors.As(err, &se) {
			t.Fatalf("first step err = %v, want a park", err)
		}
		hist := stores.History{Messages: restoredMsgs(t, st.Backend)}
		resumed, err := New(ctx, &adk.ChatModelAgent{})
		if err != nil {
			t.Fatal(err)
		}
		ag := suspendAgentRun(model, tool)
		ag.History = hist
		if _, err := resumed.Start(ctx, ag); err != nil {
			t.Fatal(err)
		}
		resumed.restore(st)
		toolCalls := 0
		var evs []types.Event
		var status runtime.Status
		var rerr error
		for {
			st, evs, status, rerr = resumed.Step(ctx, st)
			if rerr != nil {
				t.Fatalf("resume step: %v", rerr)
			}
			toolCalls++
			if status == runtime.DoneStatus {
				break
			}
			if toolCalls > 4 {
				t.Fatal("resume run did not complete")
			}
		}
		if tool.calls != 2 {
			t.Fatalf("tool calls = %d, want the approved retry only, no re-execution", tool.calls)
		}
		var done bool
		for _, ev := range evs {
			if _, ok := ev.(types.Done); ok {
				done = true
			}
		}
		if !done {
			t.Fatal("resume step returned no Done")
		}
	})
}

func restoredMsgs(t *testing.T, backend []byte) []types.Message {
	t.Helper()
	rt, err := New(context.Background(), &adk.ChatModelAgent{})
	if err != nil {
		t.Fatal(err)
	}
	rt.restore(runtime.State{Backend: backend})
	return rt.msgs
}
