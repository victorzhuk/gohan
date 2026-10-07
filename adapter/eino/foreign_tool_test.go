package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/conformance"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// einoEcho is an imported eino InvokableTool: it satisfies eino's tool
// port directly and counts its invocations.
type einoEcho struct{ called int }

func (*einoEcho) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "echo", Desc: "echoes its input"}, nil
}

func (e *einoEcho) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	e.called++
	var in struct {
		In string `json:"in"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &in); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"text":%q}`, in.In), nil
}

// nativeForeignTool bridges the imported eino tool into the types.Tool
// port the native runtime's governed batch consumes.
type nativeForeignTool struct{ inner *einoEcho }

func (t nativeForeignTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "echo"}
}

func (t nativeForeignTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	out, err := t.inner.InvokableRun(ctx, string(args))
	if err != nil {
		return types.ToolResult{}, err
	}
	var res types.ToolResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return types.ToolResult{}, err
	}
	return res, nil
}

// nativeMixHarness drives the shipped native runtime over a scripted model
// with the imported eino tool in its batch, so the mixing direction runs
// the governed turn path with a foreign component inside it.
type nativeMixHarness struct {
	profile types.ModelProfile
	model   types.Model
	tool    nativeForeignTool

	msgs []types.Message
}

func (h *nativeMixHarness) Run(ctx context.Context, input []types.Message) ([]types.Event, error) {
	h.msgs = append([]types.Message(nil), input...)
	ag := runtime.AgentRun{
		Model: h.model,
		Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{Messages: in.History}, nil
		},
		ModelEffect: h.modelEffect,
		BatchEffect: h.batchEffect,
		Tools:       []types.Tool{h.tool},
	}
	rt := runtime.NewNative()
	st, err := rt.Start(ctx, ag)
	if err != nil {
		return nil, err
	}
	var evs []types.Event
	for {
		var status runtime.Status
		st, evs, status, err = rt.Step(ctx, st)
		if err != nil {
			return evs, err
		}
		if status == runtime.DoneStatus {
			return evs, nil
		}
	}
}

func (h *nativeMixHarness) modelEffect(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	req, err := types.ModelRequest{Messages: h.msgs}, error(nil)
	if err != nil {
		return st, nil, runtime.Continue, err
	}
	var evs []types.Event
	var text string
	var pending []types.ToolUse
	for chunk, cerr := range h.model.Generate(ctx, req) {
		if cerr != nil {
			return st, evs, runtime.Continue, cerr
		}
		if err := ctx.Err(); err != nil {
			return st, evs, runtime.Continue, err
		}
		switch {
		case chunk.ToolUse != nil:
			pending = append(pending, *chunk.ToolUse)
		case chunk.Kind == types.DeltaText:
			text += chunk.Delta
		}
	}
	if len(pending) == 0 {
		msg := types.Message{ID: "assistant-1", Role: types.RoleAssistant, Blocks: []types.Block{types.Text{Text: text}}}
		h.msgs = append(h.msgs, msg)
		evs = append(evs, types.AssistantMessage{Message: msg}, types.Done{Reason: types.StopCompleted})
		return st, evs, runtime.DoneStatus, nil
	}
	asst := types.Message{Role: types.RoleAssistant}
	for i := range pending {
		asst.Blocks = append(asst.Blocks, pending[i])
	}
	h.msgs = append(h.msgs, asst)
	evs = append(evs, types.AssistantMessage{Message: asst})
	return st, evs, runtime.Continue, nil
}

func (h *nativeMixHarness) batchEffect(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	asst := h.msgs[len(h.msgs)-1]
	var uses []types.ToolUse
	for _, b := range asst.Blocks {
		if u, ok := b.(types.ToolUse); ok {
			uses = append(uses, u)
		}
	}
	if len(uses) == 0 {
		return st, nil, runtime.Continue, nil
	}
	if err := ctx.Err(); err != nil {
		return st, nil, runtime.Continue, err
	}
	var evs []types.Event
	for _, call := range uses {
		res, err := h.tool.Call(ctx, call.Args)
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		msg := types.Message{
			Role:   types.RoleAssistant,
			Blocks: []types.Block{types.ToolResult{ID: call.ID, Content: res.Content}},
		}
		h.msgs = append(h.msgs, msg)
		evs = append(evs, types.AssistantMessage{Message: msg})
	}
	return st, evs, runtime.Continue, nil
}

func (h *nativeMixHarness) Observed() conformance.Observations {
	return conformance.Observations{}
}

func TestForeignToolUnderNative(t *testing.T) {
	profile := types.ModelProfile{Name: "conformance", Version: "v1"}

	t.Run("runtime.plain-answer", func(t *testing.T) {
		echo := &einoEcho{}
		h := &nativeMixHarness{
			profile: profile,
			model:   gohantest.NewScriptedModel(profile, gohantest.Text("done")),
			tool:    nativeForeignTool{inner: echo},
		}
		evs, err := h.Run(context.Background(), harnessInput())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		assertMixDone(t, evs, "done")
		if echo.called != 0 {
			t.Fatalf("foreign tool calls = %d, want 0 on the plain answer", echo.called)
		}
	})

	t.Run("runtime.tool-round-trip", func(t *testing.T) {
		echo := &einoEcho{}
		h := &nativeMixHarness{
			profile: profile,
			model: gohantest.NewScriptedModel(profile,
				gohantest.ToolCall("echo", map[string]string{"in": "1"}),
				gohantest.Text("done"),
			),
			tool: nativeForeignTool{inner: echo},
		}
		evs, err := h.Run(context.Background(), harnessInput())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		assertMixDone(t, evs, "done")
		if echo.called != 1 {
			t.Fatalf("foreign tool calls = %d, want 1", echo.called)
		}
		var sawUse, sawResult bool
		for _, m := range h.msgs {
			for _, b := range m.Blocks {
				switch b.(type) {
				case types.ToolUse:
					sawUse = true
				case types.ToolResult:
					sawResult = true
				}
			}
		}
		if !sawUse || !sawResult {
			t.Fatalf("round trip history incomplete: use=%v result=%v", sawUse, sawResult)
		}
	})
}

func assertMixDone(t *testing.T, evs []types.Event, want string) {
	t.Helper()
	var done types.Done
	var last types.AssistantMessage
	for _, ev := range evs {
		switch v := ev.(type) {
		case types.Done:
			done = v
		case types.AssistantMessage:
			last = v
		}
	}
	if done.Reason != types.StopCompleted {
		t.Fatalf("done = %+v, want Done{StopCompleted}", done)
	}
	if textOf(last.Message.Blocks) != want {
		t.Fatalf("assistant text = %q, want %q", textOf(last.Message.Blocks), want)
	}
}
