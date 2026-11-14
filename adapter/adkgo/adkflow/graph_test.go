package adkflow

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	workflow "google.golang.org/adk/v2/workflow"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	gohanruntime "github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

var testProfile = types.ModelProfile{Name: "p", Version: "v1"}

// guardedModel records the model step and passes the call to the governed
// model unchanged, mirroring the governed decoration the run composes.
type guardedModel struct {
	inner    types.Model
	steps    *[]string
	sinkSeen *bool
}

func (m guardedModel) Profile() types.ModelProfile { return m.inner.Profile() }

func (m guardedModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	*m.steps = append(*m.steps, "model-guard")
	if _, ok := types.SinkFrom(ctx); ok {
		*m.sinkSeen = true
	}
	return m.inner.Generate(ctx, req)
}

// gatedTool records the gate step and composes the raw tool through the
// canonical chain, mirroring the governed decoration the run composes.
type gatedTool struct {
	inner types.Tool
	steps *[]string
}

func (t gatedTool) Spec() types.ToolSpec { return t.inner.Spec() }

func (t gatedTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	*t.steps = append(*t.steps, "gate")
	inner := t.inner
	steps := t.steps
	fn := chains.ComposeToolChain(chains.ToolChain{{
		Name: "guard",
		Kind: chains.KindGuard,
		Use: func(next types.ToolFunc) types.ToolFunc {
			return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
				*steps = append(*steps, "guard")
				return next(ctx, call)
			}
		},
	}}, func(_ context.Context, _ types.ToolUse) (types.ToolResult, error) {
		return inner.Call(ctx, args)
	})
	return fn(ctx, types.ToolUse{Name: t.inner.Spec().Name})
}

// buildGraph wires the governed one-shot graph: model then tools.
func buildGraph(t *testing.T, m types.Model, tool types.Tool) *workflow.Workflow {
	t.Helper()
	mn, err := ModelNode(m)
	if err != nil {
		t.Fatalf("ModelNode: %v", err)
	}
	spec := tool.Spec()
	tn, err := ToolsNode(spec, tool)
	if err != nil {
		t.Fatalf("ToolsNode: %v", err)
	}
	wf, err := workflow.New("swap", []workflow.Edge{
		{From: workflow.Start, To: mn},
		{From: mn, To: tn},
	})
	if err != nil {
		t.Fatalf("workflow.New: %v", err)
	}
	return wf
}

func TestGraphFlow(t *testing.T) {
	t.Run("graph_runs_through_a_real_invocation", func(t *testing.T) {
		ctx := context.Background()
		steps := &[]string{}
		sinkSeen := false
		m := gohantest.NewScriptedModel(testProfile, gohantest.ToolCall("ping", nil))
		fake := gohantest.NewFakeTool("ping")
		tool := gatedTool{inner: fake, steps: steps}
		wf := buildGraph(t, guardedModel{inner: m, steps: steps, sinkSeen: &sinkSeen}, tool)
		f := FromWorkflow[types.ModelRequest, []types.ToolResult](nil, wf)
		out, err := f.Invoke(ctx, types.ModelRequest{})
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("out = %+v, want one tool result", out)
		}
		if out[0].ID != "ping-1" || out[0].Outcome != types.Succeeded {
			t.Errorf("tool result = %+v, want the ping success result", out[0])
		}
		if got := *steps; len(got) != 3 || got[0] != "model-guard" || got[1] != "gate" || got[2] != "guard" {
			t.Fatalf("chain steps = %v, want model-guard, gate, guard", got)
		}
		if len(m.Requests()) != 1 {
			t.Errorf("model calls = %d, want one", len(m.Requests()))
		}
		if calls := fake.Calls(); len(calls) != 1 {
			t.Errorf("tool calls = %d, want one", len(calls))
		}
		if !sinkSeen {
			t.Error("the governed model never saw the run-scoped sink the runner propagated")
		}
	})

	t.Run("graph_flow_reports_done_from_the_harness", func(t *testing.T) {
		ctx := context.Background()
		steps := &[]string{}
		m := gohantest.NewScriptedModel(testProfile, gohantest.ToolCall("ping", nil))
		tool := gatedTool{inner: gohantest.NewFakeTool("ping"), steps: steps}
		wf := buildGraph(t, guardedModel{inner: m, steps: steps, sinkSeen: new(bool)}, tool)
		fl, ok := FromWorkflow[types.ModelRequest, []types.ToolResult](nil, wf).(*workflowFlow[types.ModelRequest, []types.ToolResult])
		if !ok {
			t.Fatal("FromWorkflow returned an unexpected flow type")
		}
		run := &graphRun[types.ModelRequest, []types.ToolResult]{f: fl}
		var done bool
		for ev, err := range gohan.Drive(ctx, graphRuntime[types.ModelRequest, []types.ToolResult]{run: run}, gohanruntime.AgentRun{}) {
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
			if d, ok := ev.(types.Done); ok && d.Reason == types.StopCompleted {
				done = true
			}
		}
		if !done {
			t.Fatal("drive reported no Done")
		}
		if !run.ran {
			t.Error("the graph run never executed")
		}
	})

	t.Run("ungoverned_component_reported", func(t *testing.T) {
		raw := workflow.NewFunctionNode("raw", func(_ agent.Context, in string) (string, error) {
			return in, nil
		}, workflow.NodeConfig{})
		wf, err := workflow.New("", []workflow.Edge{{From: workflow.Start, To: raw}})
		if err != nil {
			t.Fatalf("workflow.New: %v", err)
		}
		msgs := Check(wf)
		if len(msgs) != 1 {
			t.Fatalf("Check = %+v, want exactly the raw node reported", msgs)
		}
		if !strings.Contains(msgs[0], `"raw"`) {
			t.Errorf("message = %q, want the raw node named", msgs[0])
		}
		m := gohantest.NewScriptedModel(testProfile, gohantest.Text("ok"))
		gn, err := ModelNode(m)
		if err != nil {
			t.Fatalf("ModelNode: %v", err)
		}
		gw, err := workflow.New("", []workflow.Edge{{From: workflow.Start, To: gn}})
		if err != nil {
			t.Fatalf("workflow.New: %v", err)
		}
		if msgs := Check(gw); len(msgs) != 0 {
			t.Errorf("Check(governed) = %+v, want no messages", msgs)
		}
	})

	t.Run("construction_refusals", func(t *testing.T) {
		if _, err := ModelNode(nil); err == nil {
			t.Error("ModelNode(nil): no refusal")
		}
		if _, err := ToolsNode(types.ToolSpec{Name: "ping"}); err == nil {
			t.Error("ToolsNode without tools: no refusal")
		}
		if _, err := ToolsNode(types.ToolSpec{Name: "ping"}, gohantest.NewFakeTool("pong")); err == nil {
			t.Error("ToolsNode with a mismatched name: no refusal")
		}
		if _, err := ToolsNode(types.ToolSpec{Name: "ping"}, nil); err == nil {
			t.Error("ToolsNode with a nil tool: no refusal")
		}
	})
}
