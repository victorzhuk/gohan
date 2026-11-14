// Package adkflow presents google.golang.org/adk/v2 workflow graphs as
// governed gohan flows.
package adkflow

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	workflow "google.golang.org/adk/v2/workflow"
	"google.golang.org/genai"

	gohan "github.com/victorzhuk/gohan/core"
	gohanruntime "github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// graphCheckpointVersion is the checkpoint envelope version the graph
// runtime writes into State.Backend. A stored checkpoint under a different
// version makes the harness resume plan refuse a graph flow.
const graphCheckpointVersion = "adk-graph-v1"

// ErrNotSuspendable reports a Resume on a flow the harness resume path does
// not own. Graph flows resume through the conversation that registered
// them, never through the standalone flow value.
var ErrNotSuspendable = errors.New("adkflow: graph flow is not suspendable")

var (
	governedMu    sync.Mutex
	governedNames = map[string]struct{}{}
)

// register records a builder-produced node name so Check can tell governed
// nodes from foreign ones. The adk engine rejects duplicate names inside
// one workflow, so the name identifies the builder product.
func register(n workflow.Node) {
	governedMu.Lock()
	defer governedMu.Unlock()
	governedNames[n.Name()] = struct{}{}
}

func isGoverned(name string) bool {
	governedMu.Lock()
	defer governedMu.Unlock()
	_, ok := governedNames[name]
	return ok
}

// ModelNode builds a workflow node whose work is one governed model call.
// The governed model owns its request assembly and its canonical chain, so
// the guard stays inside the node and the caller's ctx reaches the governed
// Generate unchanged, keeping the run-scoped sink and the limits ledger
// live. The node consumes a types.ModelRequest and produces a types.Message
// carrying every block the model pass emitted. A nil model fails
// construction.
func ModelNode(m types.Model) (workflow.Node, error) {
	if m == nil {
		return nil, errors.New("adkflow: model node without a model")
	}
	n := &modelNode{
		BaseNode: workflow.NewBaseNode("gohan-model", "one governed model call", workflow.NodeConfig{}),
		m:        m,
	}
	register(n)
	return n, nil
}

type modelNode struct {
	workflow.BaseNode
	m types.Model
}

func (n *modelNode) Run(ctx agent.Context, input any) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		req, ok := input.(types.ModelRequest)
		if !ok {
			yield(nil, fmt.Errorf("adkflow: model node %q wants a types.ModelRequest, got %T", n.Name(), input))
			return
		}
		var msg types.Message
		for chunk, err := range n.m.Generate(ctx, req) {
			if err != nil {
				yield(nil, err)
				return
			}
			foldChunk(&msg, chunk)
		}
		ev := session.NewEvent(ctx, ctx.InvocationID())
		ev.Output = msg
		yield(ev, nil)
	}
}

// foldChunk folds one governed model chunk into the message under
// construction. Identity-only streaming previews carry no executable args;
// the complete call arrives later with its args.
func foldChunk(msg *types.Message, chunk types.ModelChunk) {
	if chunk.ToolUse != nil {
		msg.Blocks = append(msg.Blocks, *chunk.ToolUse)
		return
	}
	if chunk.Delta != "" {
		msg.Blocks = append(msg.Blocks, types.Text{Text: chunk.Delta})
	}
}

// ToolsNode builds a workflow node that runs the named governed tool.
// Every call the node makes runs through the tool's governed Call, so the
// round trip traverses the same chains the native runtime runs. The node
// consumes a types.ToolUse or a types.Message (every tool use block runs)
// and produces the tool results in call order. An empty tool list, a nil
// tool, or a spec naming no tool in the governed set fails construction.
func ToolsNode(spec types.ToolSpec, tools ...types.Tool) (workflow.Node, error) {
	if len(tools) == 0 {
		return nil, errors.New("adkflow: tools node without tools")
	}
	var match types.Tool
	for _, t := range tools {
		if t == nil {
			return nil, errors.New("adkflow: nil tool in tools node")
		}
		if t.Spec().Name == spec.Name {
			match = t
		}
	}
	if match == nil {
		return nil, fmt.Errorf("adkflow: tools node %q: no tool named %q in the governed set", spec.Name, spec.Name)
	}
	n := &toolsNode{
		BaseNode: workflow.NewBaseNode("gohan-tools", "one governed tool call", workflow.NodeConfig{}),
		t:        match,
	}
	register(n)
	return n, nil
}

type toolsNode struct {
	workflow.BaseNode
	t types.Tool
}

func (n *toolsNode) Run(ctx agent.Context, input any) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		uses, err := toolUses(input)
		if err != nil {
			yield(nil, err)
			return
		}
		res := make([]types.ToolResult, 0, len(uses))
		for _, use := range uses {
			r, err := n.t.Call(ctx, use.Args)
			if err != nil {
				yield(nil, err)
				return
			}
			res = append(res, r)
		}
		ev := session.NewEvent(ctx, ctx.InvocationID())
		ev.Output = res
		yield(ev, nil)
	}
}

// toolUses extracts the calls one node activation runs. A types.ToolUse is
// one call; a types.Message contributes every tool use block it carries.
func toolUses(input any) ([]types.ToolUse, error) {
	switch in := input.(type) {
	case types.ToolUse:
		return []types.ToolUse{in}, nil
	case *types.ToolUse:
		if in == nil {
			return nil, errors.New("adkflow: tools node got a nil tool use")
		}
		return []types.ToolUse{*in}, nil
	case types.Message:
		var uses []types.ToolUse
		for _, b := range in.Blocks {
			if u, ok := b.(types.ToolUse); ok {
				uses = append(uses, u)
			}
		}
		return uses, nil
	case nil:
		return nil, errors.New("adkflow: tools node without a tool use input")
	default:
		return nil, fmt.Errorf("adkflow: tools node wants a types.ToolUse or types.Message, got %T", input)
	}
}

// FromWorkflow presents a constructed adk-go workflow as a gohan Flow. One
// Step is one whole graph run, so Drive owns the limits, the events, the
// journal, checkpoint publication, preemption and Done. The stack parameter
// is kept for registration symmetry; the flow drives through gohan.Drive
// and needs no stack state, and a nil stack yields a standalone flow.
func FromWorkflow[In, Out any](stack *gohan.Stack, wf *workflow.Workflow) gohan.Flow[In, Out] {
	if wf == nil {
		panic("adkflow: FromWorkflow without a workflow")
	}
	return &workflowFlow[In, Out]{wf: wf}
}

// workflowFlow is the FromWorkflow flow value. Per-run state lives on the
// graphRun the invocation creates, so one flow value serves concurrent
// invocations.
type workflowFlow[In, Out any] struct {
	wf *workflow.Workflow
}

func (f *workflowFlow[In, Out]) Invoke(ctx context.Context, in In) (Out, error) {
	var out Out
	run := &graphRun[In, Out]{f: f, in: in}
	for _, err := range f.drive(ctx, run) {
		if err != nil {
			return out, err
		}
	}
	return run.out, nil
}

func (f *workflowFlow[In, Out]) Resume(context.Context, types.ResumeToken, stores.ResumeInput) (Out, error) {
	var out Out
	return out, ErrNotSuspendable
}

func (f *workflowFlow[In, Out]) drive(ctx context.Context, run *graphRun[In, Out]) iter.Seq2[types.Event, error] {
	return func(yield func(types.Event, error) bool) {
		for ev, err := range gohan.Drive(ctx, graphRuntime[In, Out]{run: run}, gohanruntime.AgentRun{}) {
			if !yield(ev, err) {
				return
			}
		}
	}
}

// graphRun is one invocation's per-run state.
type graphRun[In, Out any] struct {
	f   *workflowFlow[In, Out]
	in  In
	out Out
	ran bool
}

// graphRuntime adapts one graph run to the Runtime vocabulary Drive
// consumes. One Step is one whole graph run, so the granularity is Turn,
// and the runtime declares the graph flow kind and checkpoint version the
// harness resume plan reads.
type graphRuntime[In, Out any] struct {
	run *graphRun[In, Out]
}

func (graphRuntime[In, Out]) Name() string { return "adk-graph" }

func (graphRuntime[In, Out]) Granularity() gohanruntime.StepGranularity {
	return gohanruntime.GranularityTurn
}

// FlowKind declares the graph-flow resume kind: a graph flow refuses a
// foreign BackendVersion instead of replaying it.
func (graphRuntime[In, Out]) FlowKind() stores.FlowKind { return stores.FlowGraph }

// BackendVersion reports the checkpoint envelope version this runtime
// writes into State.Backend.
func (graphRuntime[In, Out]) BackendVersion() string { return graphCheckpointVersion }

func (graphRuntime[In, Out]) Start(_ context.Context, _ gohanruntime.AgentRun) (gohanruntime.State, error) {
	return gohanruntime.State{}, nil
}

func (r graphRuntime[In, Out]) Step(ctx context.Context, st gohanruntime.State) (gohanruntime.State, []types.Event, gohanruntime.Status, error) {
	run := r.run
	if run.ran {
		return st, nil, gohanruntime.DoneStatus, nil
	}
	run.ran = true
	last, err := run.f.run(ctx, any(run.in))
	if err != nil {
		return st, nil, gohanruntime.DoneStatus, err
	}
	out, ok := last.(Out)
	if !ok {
		return st, nil, gohanruntime.DoneStatus, fmt.Errorf("adkflow: graph output is %T, want %T", last, out)
	}
	run.out = out
	return st, nil, gohanruntime.DoneStatus, nil
}

// probeMessage is the user message every internal runner invocation
// carries. The graph input travels through the Run closure, not through
// the message, so the text is only a placeholder the runner requires.
var probeMessage = &genai.Content{Parts: []*genai.Part{{Text: "adkflow"}}}

// run drives one graph run through a real adk runner. The runner builds
// the genuine InvocationContext the workflow engine requires; the graph
// input reaches the root node through the agent Run closure.
func (f *workflowFlow[In, Out]) run(ctx context.Context, in any) (any, error) {
	ag, err := agent.New(agent.Config{
		Name: "adkflow",
		Run: func(ic agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return f.wf.RunNode(agent.Promote(ic), in)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("adkflow: graph agent: %w", err)
	}
	rn, err := runner.NewInMemory("adkflow", ag)
	if err != nil {
		return nil, fmt.Errorf("adkflow: graph runner: %w", err)
	}
	var last any
	for ev, err := range rn.Run(ctx, "adkflow", "run", probeMessage, agent.RunConfig{}) {
		if err != nil {
			return last, err
		}
		if ev != nil && ev.Output != nil {
			last = ev.Output
		}
	}
	return last, nil
}

// Check reports the nodes a workflow runs outside the builders, one message
// per node. The adk workflow engine exposes no introspection over a
// constructed *workflow.Workflow, so the audit runs the graph once through
// a real adk runner and reads the node path every event carries. A node the
// package did not build is reported by name; the probe observes only nodes
// that actually run, so a routed branch the probe does not take stays
// unreported.
func Check(wf *workflow.Workflow) []string {
	if wf == nil {
		return []string{"adkflow: workflow is nil"}
	}
	ag, err := agent.New(agent.Config{
		Name: "adkflow",
		Run: func(ic agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return wf.RunNode(agent.Promote(ic), nil)
		},
	})
	if err != nil {
		return []string{fmt.Sprintf("adkflow: graph agent: %v", err)}
	}
	rn, err := runner.NewInMemory("adkflow", ag)
	if err != nil {
		return []string{fmt.Sprintf("adkflow: graph runner: %v", err)}
	}
	observed := map[string]bool{}
	var order []string
	for ev, err := range rn.Run(context.Background(), "adkflow", "check", probeMessage, agent.RunConfig{}) {
		if err != nil {
			break
		}
		if ev == nil || ev.NodeInfo == nil || ev.NodeInfo.Path == "" {
			continue
		}
		name := nodeName(ev.NodeInfo.Path)
		if name == "" || name == "START" || observed[name] {
			continue
		}
		observed[name] = true
		order = append(order, name)
	}
	var msgs []string
	for _, name := range order {
		if !isGoverned(name) {
			msgs = append(msgs, fmt.Sprintf("adkflow: node %q is not built by adkflow.ModelNode or adkflow.ToolsNode", name))
		}
	}
	return msgs
}

// nodeName reduces one scheduler node path to the node name: the path is
// "<prefix>/<name>@<run>" and a standalone RunNode run carries no prefix.
func nodeName(path string) string {
	path = path[strings.LastIndex(path, "/")+1:]
	if i := strings.LastIndex(path, "@"); i >= 0 {
		path = path[:i]
	}
	return path
}
