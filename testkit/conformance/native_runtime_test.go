package conformance

import (
	"context"
	"slices"
	"strings"

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
	tool     types.Tool
	obs      Observations
	msgs     []types.Message
	call     *types.ToolUse
	turn     int
	evs      []types.Event
}

func newNativeRuntimeHarness(sc Scenario) Harness {
	return &nativeRuntimeHarness{
		scenario: sc,
		model:    gohantest.NewScriptedModel(confProfile, nativeTurns(sc)...),
		tool:     nativeTool(sc),
	}
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
	if h.tool != nil {
		ag.Tools = []types.Tool{h.tool}
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
	var call *types.ToolUse
	for chunk, err := range h.model.Generate(ctx, req) {
		if err != nil {
			return st, evs, runtime.Continue, err
		}
		if err := ctx.Err(); err != nil {
			return st, evs, runtime.Continue, err
		}
		switch {
		case chunk.ToolUse != nil:
			call = chunk.ToolUse
		case chunk.Kind == types.DeltaText:
			text = append(text, chunk.Delta)
			evs = append(evs, types.TextDelta{Turn: h.turn, MessageID: "assistant-1", Delta: chunk.Delta})
		}
	}
	h.turn++
	if call == nil {
		msg := types.Message{
			ID:     "assistant-1",
			Role:   types.RoleAssistant,
			Blocks: []types.Block{types.Text{Text: strings.Join(text, "")}},
		}
		h.msgs = append(h.msgs, msg)
		evs = append(evs, types.AssistantMessage{Turn: h.turn - 1, Message: msg})
		evs = append(evs, types.Done{Reason: types.StopCompleted})
		return st, evs, runtime.DoneStatus, nil
	}
	h.call = call
	return st, evs, runtime.Continue, nil
}

func (h *nativeRuntimeHarness) batchEffect(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if h.call == nil {
		return st, nil, runtime.Continue, nil
	}
	if err := ctx.Err(); err != nil {
		return st, nil, runtime.Continue, err
	}
	h.obs.ToolCalls = append(h.obs.ToolCalls, h.call.Name)
	res, err := chains.RunToolChain(ctx, governedChain(&h.obs, h.tool), h.invoke)
	if err != nil {
		return st, nil, runtime.Continue, err
	}
	h.msgs = append(h.msgs,
		types.Message{Role: types.RoleAssistant, Blocks: []types.Block{*h.call}},
		types.Message{Role: types.RoleAssistant, Blocks: []types.Block{res}},
	)
	h.call = nil
	return st, nil, runtime.Continue, nil
}

func (h *nativeRuntimeHarness) invoke(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
	if h.scenario == ScenarioToolError {
		panic(errToolDenied)
	}
	return h.tool.Call(ctx, []byte(call.Args))
}

func (h *nativeRuntimeHarness) Observed() Observations {
	return Observations{Steps: slices.Clone(h.obs.Steps), ToolCalls: slices.Clone(h.obs.ToolCalls)}
}
