// Package eino adapts the gohan runtime port to a cloudwego/eino ADK agent.
package eino

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Runtime drives one adk ChatModelAgent through the gohan runtime port.
// One Step is one full runner invocation; the agent's ReAct loop is the
// backend machinery inside that boundary. The runtime counts no turns and
// reads no limits: the driver owns both.
type Runtime struct {
	agent     *adk.ChatModelAgent
	streaming bool
	run       runtime.AgentRun
	msgs      []types.Message
	pending   *turnResult
	suspend   *types.SuspendError
	interrupt *adk.AgentEvent
}

const backendCheckpointVersion = "eino-v1"

type backendCheckpoint struct {
	Version string          `json:"version"`
	Msgs    []types.Message `json:"msgs"`
	Turn    int             `json:"turn"`
}

// Option configures a Runtime at construction.
type Option func(*config)

type config struct {
	streaming bool
}

// WithStreaming lets the eino runner stream agent events.
func WithStreaming(on bool) Option {
	return func(c *config) { c.streaming = on }
}

// New wires the agent into a runner. A nil agent fails construction.
func New(ctx context.Context, agent *adk.ChatModelAgent, opts ...Option) (*Runtime, error) {
	if agent == nil {
		return nil, errors.New("new eino runtime: nil agent")
	}
	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Runtime{agent: agent, streaming: cfg.streaming}, nil
}

func (r *Runtime) Name() string { return "eino" }

// BackendVersion reports the checkpoint envelope version this runtime
// writes into State.Backend, so the harness resume plan compares it
// against a stored checkpoint's BackendVersion.
func (r *Runtime) BackendVersion() string { return backendCheckpointVersion }

// FlowKind declares the agent-flow resume kind: an eino agent flow
// replays from the journal across an adapter upgrade.
func (r *Runtime) FlowKind() stores.FlowKind { return stores.FlowAgent }

// holdSuspend parks the governed signal a component returned until the
// event stream reports the backend's stateful interrupt for it.
func (r *Runtime) holdSuspend(ev *adk.AgentEvent, se *types.SuspendError) {
	r.suspend = se
	r.interrupt = ev
}

func (r *Runtime) takeSuspend() *types.SuspendError {
	se := r.suspend
	r.suspend = nil
	r.interrupt = nil
	return se
}

// withCheckpoint serializes the working history into State.Backend, the
// opaque carrier the checkpoint envelope persists and a resume restores.
func (r *Runtime) withCheckpoint(st runtime.State) runtime.State {
	msgs := r.msgs
	if r.pending != nil {
		msgs = append(append([]types.Message(nil), r.msgs...), r.pending.asMessage())
	}
	b, err := json.Marshal(backendCheckpoint{Version: backendCheckpointVersion, Msgs: msgs, Turn: st.Turn})
	if err != nil {
		return st
	}
	st.Backend = b
	return st
}

// restore rebuilds the working history from a checkpointed State.Backend
// ahead of a re-entry Step.
func (r *Runtime) restore(st runtime.State) {
	if len(st.Backend) == 0 {
		return
	}
	var cp backendCheckpoint
	if json.Unmarshal(st.Backend, &cp) != nil || cp.Version != backendCheckpointVersion {
		return
	}
	r.msgs = append([]types.Message(nil), cp.Msgs...)
}

func (r *Runtime) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

// Start binds the per-run wiring the subsequent Steps dispatch to. One
// instance drives one run, like the native stepper.
func (r *Runtime) Start(ctx context.Context, run runtime.AgentRun) (runtime.State, error) {
	if run.Model == nil {
		return runtime.State{}, errors.New("eino runtime: start without a governed model")
	}
	if len(run.Tools) == 0 {
		run.Tools = nil
	}
	r.run = run
	r.suspend = nil
	r.interrupt = nil
	// The resume path reloads the session history before Start: the
	// working history rebuilds from it, so re-entry never replays input
	// the checkpoint already carried.
	r.msgs = append([]types.Message(nil), run.History.Messages...)
	r.msgs = append(r.msgs, run.Input...)
	return runtime.State{}, nil
}

// Step runs exactly one runner invocation and drains its event stream.
// Events are runtime-originated only: component events travel through the
// sink the driver installed in ctx, reached unchanged by every bridged
// component call.
func (r *Runtime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if err := ctx.Err(); err != nil {
		return st, nil, runtime.Continue, err
	}
	r.restore(st)
	r.pending = nil
	// A resumed run executes its checkpointed pending calls under their
	// recorded ids before another model invocation: the pending assistant
	// message is already in the restored backend history, so an approved
	// call never regenerates through the model. One pending call clears
	// only after its result settles, so a new suspension names the new
	// pending call, never the replayed ask.
	for len(st.Pending) > 0 {
		call := st.Pending[0]
		res, err := r.replayCall(ctx, call)
		if err != nil {
			var se *types.SuspendError
			if errors.As(err, &se) {
				return r.park(st, se), nil, runtime.Continue, se
			}
			return st, nil, runtime.Continue, err
		}
		r.msgs = append(r.msgs, res)
		st.Pending = st.Pending[1:]
	}
	ag, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Model:    turnModel{rt: r},
		Handlers: []adk.ChatModelAgentMiddleware{NewToolCallIDHandler()},
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: r.einoTools()},
			ReturnDirectly:  r.returnDirectly(),
		},
	})
	if err != nil {
		return st, nil, runtime.Continue, fmt.Errorf("eino runtime: build agent: %w", err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           ag,
		EnableStreaming: r.streaming,
	})
	it := runner.Run(ctx, toSchemaMessages(r.msgs))
	res, err := r.drain(ctx, it)
	if err != nil {
		var limited *types.LimitExceededError
		// Only the MaxTurns refusal is a stop the driver owns; a
		// MaxCost or MaxWallClock hard abort stays a limit error the
		// driver must see.
		if errors.As(err, &limited) && limited.Limit == "MaxTurns" {
			return st, []types.Event{types.Done{Reason: types.StopLimit}}, runtime.DoneStatus, nil
		}
		if se := r.takeSuspend(); se != nil {
			// A governed component parked the run; the driver owns the
			// checkpoint ordering, so the signal crosses as-is and the
			// parked calls land in the state's pending queue.
			return r.park(st, se), nil, runtime.Continue, se
		}
		if se, ok := errors.AsType[*types.SuspendError](err); ok {
			return r.park(st, se), nil, runtime.Continue, se
		}
		return st, nil, runtime.Continue, err
	}
	st.Turn++
	if len(res.calls) == 0 {
		r.msgs = append(r.msgs, res.asMessage())
		return st, []types.Event{
			types.AssistantMessage{Message: res.asMessage()},
			types.Done{Reason: types.StopCompleted},
		}, runtime.DoneStatus, nil
	}
	r.msgs = append(r.msgs, res.asMessage())
	r.msgs = append(r.msgs, res.results...)
	return st, []types.Event{types.AssistantMessage{Message: res.asMessage()}}, runtime.Continue, nil
}

// replayCall runs one checkpointed pending call through its governed tool
// under the recorded id and returns the result message the backend history
// continues from.
func (r *Runtime) replayCall(ctx context.Context, call types.ToolUse) (types.Message, error) {
	for _, t := range r.run.Tools {
		if t.Spec().Name != call.Name {
			continue
		}
		res, err := t.Call(types.WithToolUse(ctx, call), call.Args)
		if err != nil {
			return types.Message{}, err
		}
		res.ID = call.ID
		return types.Message{Role: types.RoleUser, Blocks: []types.Block{res}}, nil
	}
	return types.Message{}, fmt.Errorf("eino runtime: pending call %q names no declared tool", call.Name)
}

var _ runtime.Runtime = (*Runtime)(nil)

// park records the calls a governed component suspended on into the
// state's pending queue, so the driver's approval persistence sees the
// same head-of-queue shape a native batch park produces.
func (r *Runtime) park(st runtime.State, se *types.SuspendError) runtime.State {
	if calls, ok := se.Payload.([]types.ToolUse); ok && len(calls) > 0 && len(st.Pending) == 0 {
		st.Pending = calls
	}
	return r.withCheckpoint(st)
}

func (r *Runtime) einoTools() []tool.BaseTool {
	if len(r.run.Tools) == 0 {
		return nil
	}
	out := make([]tool.BaseTool, 0, len(r.run.Tools))
	for _, t := range r.run.Tools {
		out = append(out, turnTool{t: t, rt: r})
	}
	return out
}

// returnDirectly ends the runner invocation after one governed tool batch,
// so one Step stays one model call plus its batch and the driver re-drives
// the turn boundary itself.
func (r *Runtime) returnDirectly() map[string]bool {
	if len(r.run.Tools) == 0 {
		return nil
	}
	out := make(map[string]bool, len(r.run.Tools))
	for _, t := range r.run.Tools {
		out[t.Spec().Name] = true
	}
	return out
}

// turnResult is one model turn the governed bridge observed.
type turnResult struct {
	text    string
	calls   []types.ToolUse
	results []types.Message
}

func (res turnResult) asMessage() types.Message {
	msg := types.Message{Role: types.RoleAssistant}
	if res.text != "" {
		msg.Blocks = append(msg.Blocks, types.Text{Text: res.text})
	}
	for _, c := range res.calls {
		msg.Blocks = append(msg.Blocks, c)
	}
	return msg
}

// drain consumes one runner invocation to completion and folds the model
// outputs it carries into one turn result. ctx flows into the iterator, so
// a cancelled turn stops here.
func (r *Runtime) drain(ctx context.Context, it *adk.AsyncIterator[*adk.AgentEvent]) (turnResult, error) {
	var res turnResult
	for {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		ev, ok := it.Next()
		if !ok {
			return res, nil
		}
		if ev.Err != nil {
			return res, ev.Err
		}
		if ev.Action != nil && ev.Action.Interrupted != nil {
			if se := r.takeSuspend(); se != nil {
				return res, se
			}
			return res, fmt.Errorf("eino runtime: interrupt without a governed park: %v", ev.Action.Interrupted.Data)
		}
		out := ev.Output
		if out == nil {
			continue
		}
		variant := out.MessageOutput
		if variant == nil || variant.IsStreaming {
			continue
		}
		m := variant.Message
		switch {
		case variant.Role == schema.Tool || m.ToolCallID != "":
			res.results = append(res.results, types.Message{
				Role: types.RoleAssistant,
				Blocks: []types.Block{types.ToolResult{
					ID:      m.ToolCallID,
					Content: []types.Block{types.Text{Text: m.Content}},
				}},
			})
		default:
			res.text = m.Content
			res.calls = nil
			for _, c := range m.ToolCalls {
				res.calls = append(res.calls, types.ToolUse{
					ID:   c.ID,
					Name: c.Function.Name,
					Args: json.RawMessage(c.Function.Arguments),
				})
			}
		}
	}
}

// limitIdentity hands a hard limit abort back unwrapped, so the driver
// sees the same *LimitExceededError identity the native flow receives.
func limitIdentity(err error) error {
	var limited *types.LimitExceededError
	if errors.As(err, &limited) && limited.Limit != "MaxTurns" {
		return limited
	}
	return err
}

// turnModel bridges the governed gohan model into the eino chat model
// port. ctx reaches the governed call unchanged, so the run-scoped sink,
// the cancel shield and the limits ledger all stay live.
type turnModel struct {
	rt *Runtime
}

func (t turnModel) Generate(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	res, err := t.call(ctx, in)
	if err != nil {
		return nil, limitIdentity(err)
	}
	out := &schema.Message{Role: schema.Assistant, Content: res.text}
	for _, c := range res.calls {
		out.ToolCalls = append(out.ToolCalls, schema.ToolCall{
			ID: c.ID,
			Function: schema.FunctionCall{
				Name:      c.Name,
				Arguments: string(c.Args),
			},
		})
	}
	return out, nil
}

func (t turnModel) Stream(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := t.Generate(ctx, in)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (t turnModel) call(ctx context.Context, in []*schema.Message) (turnResult, error) {
	req, err := t.assemble(ctx)
	if err != nil {
		return turnResult{}, err
	}
	var res turnResult
	for chunk, cerr := range t.rt.run.Model.Generate(ctx, req) {
		if cerr != nil {
			return res, cerr
		}
		// Identity-only streaming previews carry no executable args; the
		// complete call arrives later with its args.
		if chunk.Kind == types.DeltaToolArgs && chunk.ToolUse != nil && len(chunk.ToolUse.Args) == 0 {
			continue
		}
		switch {
		case chunk.ToolUse != nil:
			res.calls = append(res.calls, *chunk.ToolUse)
		case chunk.Kind == types.DeltaText:
			res.text += chunk.Delta
		}
	}
	t.rt.pending = &res
	return res, nil
}

func (t turnModel) assemble(ctx context.Context) (types.ModelRequest, error) {
	if t.rt.run.Assemble == nil {
		return types.ModelRequest{Messages: t.rt.msgs}, nil
	}
	return t.rt.run.Assemble(ctx, types.AssembleInput{History: t.rt.msgs})
}

// turnTool bridges one governed gohan tool into the eino tool port. The
// tool arrives already governed; the call carries ctx unchanged.
type turnTool struct {
	t  types.Tool
	rt *Runtime
}

func (t turnTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	spec := t.t.Spec()
	return &schema.ToolInfo{Name: spec.Name, Desc: spec.Description}, nil
}

func (t turnTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	res, err := t.t.Call(ctx, json.RawMessage(args))
	if err != nil {
		var se *types.SuspendError
		if errors.As(err, &se) {
			// The governed component parks the call: the park crosses the
			// backend as a stateful interrupt and maps back to the same
			// signal on the event stream.
			t.rt.holdSuspend(adk.StatefulInterrupt(ctx, se, nil), se)
		}
		return "", limitIdentity(err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("eino runtime: bridge tool result: %w", err)
	}
	return string(b), nil
}

// toolCallIDHandler binds the backend call id each tool wrapper receives
// onto the caller context, so the governed core can read the identity the
// backend generated for the call.
type toolCallIDHandler struct {
	*adk.BaseChatModelAgentMiddleware
}

// NewToolCallIDHandler returns the middleware that carries the backend
// tool call id through the caller context. Install it in
// ChatModelAgentConfig.Handlers; a call without it fails closed.
func NewToolCallIDHandler() adk.ChatModelAgentMiddleware {
	return &toolCallIDHandler{}
}

func (h *toolCallIDHandler) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	call := types.ToolUse{}
	if tCtx != nil {
		call.ID = tCtx.CallID
	}
	return WithToolCallID(endpoint, call), nil
}

// WithToolCallID wraps a tool call endpoint so it binds call onto the
// caller context before the endpoint runs.
func WithToolCallID(endpoint adk.InvokableToolCallEndpoint, call types.ToolUse) adk.InvokableToolCallEndpoint {
	return func(ctx context.Context, argsInJSON string, opts ...tool.Option) (string, error) {
		return endpoint(types.WithToolUse(ctx, call), argsInJSON, opts...)
	}
}

func toSchemaMessages(in []types.Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(in))
	for _, m := range in {
		var role schema.RoleType
		if m.Role == types.RoleUser {
			role = schema.User
		} else {
			role = schema.Assistant
		}
		sm := &schema.Message{Role: role}
		for _, b := range m.Blocks {
			switch v := b.(type) {
			case types.Text:
				sm.Content += v.Text
			case types.ToolUse:
				sm.ToolCalls = append(sm.ToolCalls, schema.ToolCall{
					ID: v.ID,
					Function: schema.FunctionCall{
						Name:      v.Name,
						Arguments: string(v.Args),
					},
				})
			case types.ToolResult:
				sm = &schema.Message{
					Role:       schema.Tool,
					Content:    textOf(v.Content),
					ToolCallID: v.ID,
				}
			}
		}
		out = append(out, sm)
	}
	return out
}

func textOf(blocks []types.Block) string {
	var s string
	for _, b := range blocks {
		if t, ok := b.(types.Text); ok {
			s += t.Text
		}
	}
	return s
}
