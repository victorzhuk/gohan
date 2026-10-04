package gohan

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"encoding/json/jsontext"

	"github.com/victorzhuk/gohan/core/types"
)

func TestToolCallContract(t *testing.T) {
	t.Run("tools.unknown-tool", func(t *testing.T) {
		called := false
		set := ToolSetFunc(func(name string) (types.Tool, bool) {
			return stubTool{name: name, call: func() { called = true }}, false
		})
		res, err := CallTool(context.Background(), set, "create_priority_ticket", jsontext.Value(`{}`))
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if res.Outcome != types.Failed {
			t.Fatalf("Outcome = %v, want %v", res.Outcome, types.Failed)
		}
		if res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("Error = %v, want Permanent", res.Error)
		}
		if !strings.Contains(res.Error.Message, "create_priority_ticket") {
			t.Fatalf("Message = %q, want it to name the unknown tool", res.Error.Message)
		}
		if called {
			t.Fatal("tool executed for an unknown name")
		}
	})

	t.Run("tools.invalid-args-on-raw-tool", func(t *testing.T) {
		called := false
		tool := stubTool{
			name: "raw_notes",
			spec: types.ToolSpec{Name: "raw_notes", Schema: jsontext.Value(`{"type":"object"}`)},
			call: func() { called = true },
		}
		set := ToolSetFunc(func(string) (types.Tool, bool) { return tool, true })
		res, err := CallTool(context.Background(), set, "raw_notes", jsontext.Value(`{"a":1,"a":2}`))
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("result = %+v, want Failed(Permanent)", res)
		}
		if res.Error.Message == "" {
			t.Fatal("Message empty, want the validation error")
		}
		if called {
			t.Fatal("tool ran despite invalid arguments")
		}
	})

	t.Run("tools.classified-error", func(t *testing.T) {
		tool := stubTool{
			name: "search",
			spec: types.ToolSpec{Name: "search", Effect: types.ReadOnly},
			err:  Retryable(errors.New("backend busy")),
		}
		set := ToolSetFunc(func(string) (types.Tool, bool) { return tool, true })
		res, err := CallTool(context.Background(), set, "search", jsontext.Value(`{}`))
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if res.Outcome != types.Failed {
			t.Fatalf("Outcome = %v, want %v", res.Outcome, types.Failed)
		}
		if res.Error == nil || res.Error.Kind != types.Retryable {
			t.Fatalf("Error.Kind = %v, want Retryable", res.Error)
		}
	})

	t.Run("panic recovered by effect", func(t *testing.T) {
		panicking := func(name string, effect types.Effect) stubTool {
			return stubTool{
				name: name,
				spec: types.ToolSpec{Name: name, Effect: effect},
				call: func() { panic("boom") },
			}
		}
		set := ToolSetFunc(func(name string) (types.Tool, bool) { return panicking(name, effectFor(name)), true })

		res, err := CallTool(context.Background(), set, "side", jsontext.Value(`{}`))
		if err != nil {
			t.Fatalf("SideEffect: err = %v, want nil", err)
		}
		if res.Outcome != types.Unknown || res.Error == nil || res.Error.Kind != types.OutcomeUnknown {
			t.Fatalf("SideEffect: result = %+v, want Unknown(OutcomeUnknown)", res)
		}

		res, err = CallTool(context.Background(), set, "ro", jsontext.Value(`{}`))
		if err != nil {
			t.Fatalf("ReadOnly: err = %v, want nil", err)
		}
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("ReadOnly: result = %+v, want Failed(Permanent)", res)
		}
	})

	t.Run("deadline by effect", func(t *testing.T) {
		tool := func(effect types.Effect) stubTool {
			return stubTool{
				name: "t",
				spec: types.ToolSpec{Name: "t", Effect: effect},
				call: func() { time.Sleep(time.Millisecond) },
				err:  context.DeadlineExceeded,
			}
		}
		set := func() ToolSet {
			return ToolSetFunc(func(string) (types.Tool, bool) { return tool(types.SideEffect), true })
		}
		res, err := CallTool(context.Background(), set(), "t", jsontext.Value(`{}`))
		if err != nil {
			t.Fatalf("SideEffect: err = %v, want nil", err)
		}
		if res.Outcome != types.Unknown || res.Error == nil || res.Error.Kind != types.OutcomeUnknown {
			t.Fatalf("SideEffect deadline: result = %+v, want Unknown(OutcomeUnknown)", res)
		}

		set = func() ToolSet {
			return ToolSetFunc(func(string) (types.Tool, bool) { return tool(types.ReadOnly), true })
		}
		res, err = CallTool(context.Background(), set(), "t", jsontext.Value(`{}`))
		if err != nil {
			t.Fatalf("ReadOnly: err = %v, want nil", err)
		}
		if res.Error == nil || res.Error.Kind != types.Retryable {
			t.Fatalf("ReadOnly deadline: result = %+v, want Retryable", res)
		}
	})

	t.Run("cancellation passes through", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		tool := stubTool{name: "t", spec: types.ToolSpec{Name: "t"}, err: context.Canceled}
		set := ToolSetFunc(func(string) (types.Tool, bool) { return tool, true })
		res, err := CallTool(ctx, set, "t", jsontext.Value(`{}`))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if res.Error != nil {
			t.Fatalf("Error = %v, want no rendered failure", res.Error)
		}
	})
}

func effectFor(name string) types.Effect {
	if name == "side" {
		return types.SideEffect
	}
	return types.ReadOnly
}

type stubTool struct {
	name string
	spec types.ToolSpec
	call func()
	err  error
}

func (t stubTool) Spec() types.ToolSpec { return t.spec }

func (t stubTool) Call(ctx context.Context, args jsontext.Value) (types.ToolResult, error) {
	if t.call != nil {
		t.call()
	}
	if t.err != nil {
		return types.ToolResult{}, t.err
	}
	return types.ToolResult{Outcome: types.Succeeded}, nil
}
