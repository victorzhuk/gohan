// Package einoflow presents cloudwego/eino graphs as governed gohan flows.
package einoflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	gohan "github.com/victorzhuk/gohan/core"
	gohanruntime "github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// graphCheckpointVersion is the checkpoint envelope version the graph
// runtime writes into State.Backend. A stored checkpoint under a different
// version makes the harness resume plan refuse a graph flow.
const graphCheckpointVersion = "eino-graph-v1"

// ErrNotSuspendable reports a Resume on a flow the harness resume path does
// not own. Graph flows resume through the conversation that registered
// them, never through the standalone flow value.
var ErrNotSuspendable = errors.New("einoflow: graph flow is not suspendable")

// ModelNode bridges a governed gohan model into the eino chat model port.
// The bridge is non-mutating: WithTools records nothing, because the
// governed model owns its request assembly and the tool inventory binds at
// the tools node. The caller's ctx reaches the governed Generate unchanged,
// so the run-scoped sink and the limits ledger stay live.
func ModelNode(m types.Model) model.ToolCallingChatModel {
	return &governedChatModel{m: m}
}

// ToolsNode composes the governed tools into one eino tools node. Every
// call the node makes runs through the tool's governed Call — the canonical
// chain and the guards the component already carries — so the round trip
// traverses the same chains the native runtime runs. A nil tool fails
// construction.
func ToolsNode(ctx context.Context, tools ...types.Tool) (*compose.ToolsNode, error) {
	if len(tools) == 0 {
		return nil, errors.New("einoflow: tools node without tools")
	}
	bases := make([]tool.BaseTool, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			return nil, errors.New("einoflow: nil tool in tools node")
		}
		bases = append(bases, governedTool{t: t})
	}
	node, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{Tools: bases})
	if err != nil {
		return nil, fmt.Errorf("einoflow: tools node: %w", err)
	}
	return node, nil
}

// governedChatModel is the ModelNode bridge. It implements
// model.ToolCallingChatModel over the governed model.
type governedChatModel struct {
	m types.Model
}

// governed marks the component as builder-produced; Check reads it.
func (*governedChatModel) gohanGoverned() {}

func (g *governedChatModel) Generate(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	req, err := schemaToRequest(in)
	if err != nil {
		return nil, err
	}
	var res turnOutput
	for chunk, cerr := range g.m.Generate(ctx, req) {
		if cerr != nil {
			return nil, cerr
		}
		res.fold(chunk)
	}
	return res.asMessage(), nil
}

func (g *governedChatModel) Stream(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := g.Generate(ctx, in)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// WithTools is non-mutating: the governed model assembles its own requests
// and the tools bind at the tools node, so the inventory records nothing.
func (g *governedChatModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return g, nil
}

// governedTool is the ToolsNode bridge. The tool arrives already governed;
// the call carries ctx unchanged.
type governedTool struct {
	t types.Tool
}

func (g governedTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	spec := g.t.Spec()
	return &schema.ToolInfo{Name: spec.Name, Desc: spec.Description}, nil
}

func (g governedTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	res, err := g.t.Call(ctx, json.RawMessage(args))
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("einoflow: bridge tool result: %w", err)
	}
	return string(b), nil
}

// turnOutput folds one governed model pass into one eino message.
type turnOutput struct {
	text  string
	calls []types.ToolUse
}

func (o *turnOutput) fold(chunk types.ModelChunk) {
	// Identity-only streaming previews carry no executable args; the
	// complete call arrives later with its args.
	if chunk.Kind == types.DeltaToolArgs && chunk.ToolUse != nil && len(chunk.ToolUse.Args) == 0 {
		return
	}
	switch {
	case chunk.ToolUse != nil:
		o.calls = append(o.calls, *chunk.ToolUse)
	case chunk.Kind == types.DeltaText:
		o.text += chunk.Delta
	}
}

func (o *turnOutput) asMessage() *schema.Message {
	out := &schema.Message{Role: schema.Assistant, Content: o.text}
	for _, c := range o.calls {
		out.ToolCalls = append(out.ToolCalls, schema.ToolCall{
			ID: c.ID,
			Function: schema.FunctionCall{
				Name:      c.Name,
				Arguments: string(c.Args),
			},
		})
	}
	return out
}

// schemaToRequest converts one eino message batch into the governed
// request the model port consumes.
func schemaToRequest(in []*schema.Message) (types.ModelRequest, error) {
	msgs := make([]types.Message, 0, len(in))
	for _, m := range in {
		if m == nil {
			continue
		}
		msgs = append(msgs, schemaToMessage(m))
	}
	return types.ModelRequest{Messages: msgs}, nil
}

func schemaToMessage(m *schema.Message) types.Message {
	var msg types.Message
	switch m.Role {
	case schema.User:
		msg.Role = types.RoleUser
	default:
		msg.Role = types.RoleAssistant
	}
	if m.Content != "" {
		msg.Blocks = append(msg.Blocks, types.Text{Text: m.Content})
	}
	for _, c := range m.ToolCalls {
		msg.Blocks = append(msg.Blocks, types.ToolUse{
			ID:   c.ID,
			Name: c.Function.Name,
			Args: json.RawMessage(c.Function.Arguments),
		})
	}
	if m.ToolCallID != "" {
		msg.Blocks = append(msg.Blocks, types.ToolResult{
			ID:      m.ToolCallID,
			Content: []types.Block{types.Text{Text: m.Content}},
		})
	}
	return msg
}

// FromRunnable presents a compiled eino runnable as a gohan Flow. One Step
// is one whole graph run, so Drive owns the limits, the events, the
// journal, checkpoint publication, preemption and Done. The stack parameter
// is kept for registration symmetry; the flow drives through gohan.Drive
// and needs no stack state, and a nil stack yields a standalone flow.
func FromRunnable[In, Out any](stack *gohan.Stack, r compose.Runnable[In, Out]) gohan.Flow[In, Out] {
	if r == nil {
		panic("einoflow: FromRunnable without a runnable")
	}
	return &graphFlow[In, Out]{run: r}
}

// graphFlow is the FromRunnable flow value. Per-run state lives on the
// graphRun the invocation creates, so one flow value serves concurrent
// invocations.
type graphFlow[In, Out any] struct {
	run compose.Runnable[In, Out]
}

func (f *graphFlow[In, Out]) Invoke(ctx context.Context, in In) (Out, error) {
	var out Out
	run := &graphRun[In, Out]{f: f, in: in}
	for _, err := range f.drive(ctx, run) {
		if err != nil {
			return out, err
		}
	}
	return run.out, nil
}

func (f *graphFlow[In, Out]) Resume(context.Context, types.ResumeToken, stores.ResumeInput) (Out, error) {
	var out Out
	return out, ErrNotSuspendable
}

func (f *graphFlow[In, Out]) drive(ctx context.Context, run *graphRun[In, Out]) iter.Seq2[types.Event, error] {
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
	f   *graphFlow[In, Out]
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

func (graphRuntime[In, Out]) Name() string { return "eino-graph" }

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
	out, err := run.f.run.Invoke(ctx, run.in)
	if err != nil {
		return st, nil, gohanruntime.DoneStatus, err
	}
	run.out = out
	return st, nil, gohanruntime.DoneStatus, nil
}

// Issue is one component Check reports.
type Issue struct {
	// Component names the kind of the reported node value.
	Component string
	// Reason says why the component is ungoverned.
	Reason string
}

// governed marks a component a builder produced.
type governed interface{ gohanGoverned() }

// Check reports the components a graph carries outside the builders. Pass
// the node values the graph was built with. A chat model that did not come
// from ModelNode is reported; a nil value is skipped. eino v0.9 exposes no
// introspection over a *compose.ToolsNode instance, so tools nodes are
// skipped where introspection is unavailable.
func Check(nodes ...any) []Issue {
	var issues []Issue
	for _, n := range nodes {
		switch {
		case n == nil:
			continue
		case isGoverned(n):
			continue
		case isChatModel(n):
			issues = append(issues, Issue{
				Component: fmt.Sprintf("%T", n),
				Reason:    "chat model not built by einoflow.ModelNode",
			})
		}
	}
	return issues
}

func isGoverned(n any) bool {
	_, ok := n.(governed)
	return ok
}

func isChatModel(n any) bool {
	_, ok := n.(model.BaseChatModel)
	return ok
}
