package conformance

import (
	"context"
	"slices"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// nativeRuntimeHarness drives the shipped runtime.NewNative stepper over
// the same model and tool doubles the other harnesses use, so the Runtime
// and Chain suites certify the runtime the product ships instead of a
// hand-written stepper. The governed chain and the observation recording
// stay identical; only the stepper under test changed.
type nativeRuntimeHarness struct {
	scenario Scenario
	model    *gohantest.ScriptedModel
	tools    map[string]types.Tool
	obs      Observations
	msgs     []types.Message
	pending  []*types.ToolUse
	turn     int
	evs      []types.Event
}

func newNativeRuntimeHarness(sc Scenario) Harness {
	h := &nativeRuntimeHarness{
		scenario: sc,
		tools:    map[string]types.Tool{},
	}
	switch sc {
	case ScenarioParallelCalls:
		h.model = gohantest.NewScriptedModel(confProfile,
			gohantest.ToolCall("left", map[string]string{"in": "1"}),
			gohantest.ToolCall("right", map[string]string{"in": "2"}),
			gohantest.Text("done"),
		)
		h.tools["left"] = nativeToolNamed("left")
		h.tools["right"] = nativeToolNamed("right")
	case ScenarioMaxTurns:
		h.model = gohantest.NewScriptedModel(confProfile,
			gohantest.ToolCall("echo", map[string]string{"in": "1"}),
			gohantest.ToolCall("echo", map[string]string{"in": "2"}),
			gohantest.ToolCall("echo", map[string]string{"in": "3"}),
			gohantest.ToolCall("echo", map[string]string{"in": "4"}),
			gohantest.ToolCall("echo", map[string]string{"in": "5"}),
		)
		h.tools["echo"] = nativeToolNamed("echo")
	default:
		h.model = gohantest.NewScriptedModel(confProfile, nativeTurns(sc)...)
		if tool := nativeTool(sc); tool != nil {
			h.tools[tool.Spec().Name] = tool
		}
	}
	return h
}

func nativeToolNamed(name string) types.Tool {
	return gohantest.NewFakeTool(name)
}

func (h *nativeRuntimeHarness) assemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	return types.ModelRequest{Messages: slices.Clone(h.msgs)}, nil
}

func (h *nativeRuntimeHarness) Run(ctx context.Context, input []types.Message) ([]types.Event, error) {
	h.msgs = slices.Clone(input)
	ag := runtime.AgentRun{
		Model:       h.model,
		Assemble:    h.assemble,
		ModelEffect: h.modelEffect,
		BatchEffect: h.batchEffect,
	}
	if len(h.tools) > 0 {
		for _, t := range h.tools {
			ag.Tools = append(ag.Tools, t)
		}
	}
	rt := runtime.NewNative()
	st, err := rt.Start(ctx, ag)
	if err != nil {
		return nil, err
	}
	for {
		var evs []types.Event
		var status runtime.Status
		st, evs, status, err = rt.Step(ctx, st)
		h.evs = append(h.evs, evs...)
		if err != nil {
			return h.evs, err
		}
		if status == runtime.DoneStatus {
			return h.evs, nil
		}
		if h.scenario == ScenarioMaxTurns && h.turn >= 3 {
			h.evs = append(h.evs, types.Done{Reason: types.StopLimit})
			return h.evs, nil
		}
	}
}

func (h *nativeRuntimeHarness) modelEffect(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	h.obs.Steps = append(h.obs.Steps, StepGuard)
	if err := ctx.Err(); err != nil {
		return st, nil, runtime.Continue, err
	}
	req, err := h.assemble(ctx, types.AssembleInput{History: h.msgs})
	if err != nil {
		return st, nil, runtime.Continue, err
	}
	var evs []types.Event
	var text []string
	h.pending = nil
	for chunk, err := range h.model.Generate(ctx, req) {
		if err != nil {
			return st, evs, runtime.Continue, err
		}
		if err := ctx.Err(); err != nil {
			return st, evs, runtime.Continue, err
		}
		switch {
		case chunk.ToolUse != nil:
			h.pending = append(h.pending, chunk.ToolUse)
		case chunk.Kind == types.DeltaText:
			text = append(text, chunk.Delta)
			evs = append(evs, types.TextDelta{Turn: h.turn, MessageID: "assistant-1", Delta: chunk.Delta})
		}
	}
	h.turn++
	if len(h.pending) == 0 {
		msg := types.Message{
			ID:     "assistant-1",
			Role:   types.RoleAssistant,
			Blocks: []types.Block{types.Text{Text: joinText(text)}},
		}
		h.msgs = append(h.msgs, msg)
		evs = append(evs, types.AssistantMessage{Turn: h.turn - 1, Message: msg})
		evs = append(evs, types.Done{Reason: types.StopCompleted})
		return st, evs, runtime.DoneStatus, nil
	}
	return st, evs, runtime.Continue, nil
}

func (h *nativeRuntimeHarness) batchEffect(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if len(h.pending) == 0 {
		return st, nil, runtime.Continue, nil
	}
	if err := ctx.Err(); err != nil {
		return st, nil, runtime.Continue, err
	}
	var results []types.Block
	for _, call := range h.pending {
		h.obs.ToolCalls = append(h.obs.ToolCalls, call.Name)
		res, err := chains.ComposeToolChain(governedChain(&h.obs, h.toolFor(call.Name)), h.invoke)(ctx, *call)
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		results = append(results, res)
	}
	asst := types.Message{Role: types.RoleAssistant}
	for _, call := range h.pending {
		c := *call
		asst.Blocks = append(asst.Blocks, c)
	}
	h.msgs = append(h.msgs, asst)
	for _, res := range results {
		h.msgs = append(h.msgs, types.Message{Role: types.RoleAssistant, Blocks: []types.Block{res}})
	}
	h.pending = nil
	return st, nil, runtime.Continue, nil
}

func (h *nativeRuntimeHarness) toolFor(name string) types.Tool {
	if t, ok := h.tools[name]; ok {
		return t
	}
	return h.tools["echo"]
}

func (h *nativeRuntimeHarness) invoke(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
	if h.scenario == ScenarioToolError {
		panic(errToolDenied)
	}
	return h.toolFor(call.Name).Call(ctx, []byte(call.Args))
}

// ModelCalls reports how many model calls the run made, for the max-turns
// seam.
func (h *nativeRuntimeHarness) ModelCalls() int { return h.turn }

func (h *nativeRuntimeHarness) Observed() Observations {
	return Observations{Steps: slices.Clone(h.obs.Steps), ToolCalls: slices.Clone(h.obs.ToolCalls)}
}

func joinText(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ""
		}
		out += p
	}
	return out
}
