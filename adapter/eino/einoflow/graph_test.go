package einoflow

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	gohanruntime "github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

var testProfile = types.ModelProfile{Name: "p", Version: "v1"}

// scriptGraphModel answers every call with the scripted chunks.
type scriptGraphModel struct {
	chunks []types.ModelChunk
	calls  int
}

func (m *scriptGraphModel) Profile() types.ModelProfile { return testProfile }

func (m *scriptGraphModel) Generate(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.calls++
	chunks := m.chunks
	return func(yield func(types.ModelChunk, error) bool) {
		for _, c := range chunks {
			if !yield(c, nil) {
				return
			}
		}
	}
}

// rawTool is the ungoverned worker a governed decorator wraps.
type rawTool struct {
	steps *[]string
}

func (t rawTool) Spec() types.ToolSpec { return types.ToolSpec{Name: "ping"} }

func (t rawTool) Call(_ context.Context, _ json.RawMessage) (types.ToolResult, error) {
	if t.steps != nil {
		*t.steps = append(*t.steps, "tool")
	}
	return types.ToolResult{Content: []types.Block{types.Text{Text: "pong"}}}, nil
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

// rawChatModel is a chat model built outside the builders.
type rawChatModel struct{}

func (rawChatModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return &schema.Message{Role: schema.Assistant}, nil
}

func (rawChatModel) Stream(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, _ := rawChatModel{}.Generate(context.Background(), in)
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// compileGraph compiles the governed one-shot graph: model then tools.
// The chat model node consumes []*schema.Message and produces one message;
// the tools node consumes the batch back.
func compileGraph(t *testing.T, ctx context.Context, m types.Model, tools ...types.Tool) compose.Runnable[string, []*schema.Message] {
	t.Helper()
	tn, err := ToolsNode(ctx, tools...)
	if err != nil {
		t.Fatalf("ToolsNode: %v", err)
	}
	g := compose.NewGraph[string, []*schema.Message]()
	in := compose.InvokableLambda(func(_ context.Context, s string) ([]*schema.Message, error) {
		return []*schema.Message{{Role: schema.User, Content: s}}, nil
	})
	if err := g.AddLambdaNode("in", in); err != nil {
		t.Fatalf("AddLambdaNode: %v", err)
	}
	if err := g.AddChatModelNode("model", ModelNode(m)); err != nil {
		t.Fatalf("AddChatModelNode: %v", err)
	}
	if err := g.AddToolsNode("tools", tn); err != nil {
		t.Fatalf("AddToolsNode: %v", err)
	}
	if err := g.AddEdge(compose.START, "in"); err != nil {
		t.Fatalf("AddEdge start: %v", err)
	}
	if err := g.AddEdge("in", "model"); err != nil {
		t.Fatalf("AddEdge in: %v", err)
	}
	if err := g.AddEdge("model", "tools"); err != nil {
		t.Fatalf("AddEdge model: %v", err)
	}
	if err := g.AddEdge("tools", compose.END); err != nil {
		t.Fatalf("AddEdge end: %v", err)
	}
	r, err := g.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return r
}

func toolUseChunk() types.ModelChunk {
	return types.ModelChunk{ToolUse: &types.ToolUse{ID: "call_1", Name: "ping"}}
}

func TestGraphFlow(t *testing.T) {
	t.Run("runtime.governed-components-in-eino-graph", func(t *testing.T) {
		ctx := context.Background()
		steps := &[]string{}
		m := &scriptGraphModel{chunks: []types.ModelChunk{toolUseChunk()}}
		r := compileGraph(t, ctx, m, gatedTool{inner: rawTool{steps: steps}, steps: steps})
		f := FromRunnable[string, []*schema.Message](nil, r)
		out, err := f.Invoke(ctx, "hi")
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if len(out) != 1 || out[0].Role != schema.Tool || out[0].ToolCallID != "call_1" {
			t.Fatalf("out = %+v, want one tool result message for call_1", out)
		}
		if !strings.Contains(out[0].Content, "pong") {
			t.Errorf("tool result content = %q, want the ping result", out[0].Content)
		}
		if got := *steps; len(got) != 3 || got[0] != "gate" || got[1] != "guard" || got[2] != "tool" {
			t.Fatalf("chain steps = %v, want gate, guard, tool", got)
		}
		if m.calls != 1 {
			t.Errorf("model calls = %d, want one", m.calls)
		}
	})

	t.Run("ungoverned_component_reported", func(t *testing.T) {
		issues := Check(rawChatModel{}, (*compose.ToolsNode)(nil))
		if len(issues) != 1 {
			t.Fatalf("Check = %+v, want exactly the raw chat model reported", issues)
		}
		if issues[0].Reason != "chat model not built by einoflow.ModelNode" {
			t.Errorf("reason = %q, want the ModelNode reason", issues[0].Reason)
		}
		m := &scriptGraphModel{}
		if issues := Check(ModelNode(m)); len(issues) != 0 {
			t.Errorf("Check(ModelNode) = %+v, want no issues", issues)
		}
	})
}

func TestGraphFlowGoverning(t *testing.T) {
	t.Run("graph_flow_reports_done_from_the_harness", func(t *testing.T) {
		ctx := context.Background()
		steps := &[]string{}
		m := &scriptGraphModel{chunks: []types.ModelChunk{toolUseChunk()}}
		r := compileGraph(t, ctx, m, gatedTool{inner: rawTool{steps: steps}, steps: steps})
		fl, ok := FromRunnable[string, []*schema.Message](nil, r).(*graphFlow[string, []*schema.Message])
		if !ok {
			t.Fatal("FromRunnable returned an unexpected flow type")
		}
		run := &graphRun[string, []*schema.Message]{f: fl, in: "hi"}
		var done bool
		for ev, err := range gohan.Drive(ctx, graphRuntime[string, []*schema.Message]{run: run}, gohanruntime.AgentRun{}) {
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

	t.Run("granularity_reports_turn", func(t *testing.T) {
		m := &scriptGraphModel{}
		spec := gohan.ForeignSpec{
			Request:    gohan.FlowRequest{Name: "graph"},
			Profile:    "p",
			NewRuntime: func() gohanruntime.Runtime { return graphRuntime[string, string]{} },
		}
		s, err := gohan.Build(
			gohan.WithModels(m),
			gohan.WithLimits("graph", types.InteractiveLimits),
			gohan.WithForeignAgent(spec),
		)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		ex := s.Explain("graph")
		if ex.Granularity != "turn" {
			t.Errorf("granularity = %q, want turn", ex.Granularity)
		}
	})
}

func TestFromRunnableGuards(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("FromRunnable without a runnable: no panic")
		}
	}()
	FromRunnable[string, string](nil, nil)
}
