package gohan

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"iter"
	"testing"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestTrifectaBuildRequiresTaintPolicy(t *testing.T) {
	var modelCalls int
	model := &funcModel{fn: func(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		modelCalls++
		return func(yield func(types.ModelChunk, error) bool) {}
	}}
	tools := []types.Tool{
		&fakeNativeTool{spec: types.ToolSpec{Name: "private", Capabilities: types.Capabilities{PrivateRead: true}}},
		&fakeNativeTool{spec: types.ToolSpec{Name: "untrusted", Trust: types.Untrusted}},
		&fakeNativeTool{spec: types.ToolSpec{Name: "exfil", Capabilities: types.Capabilities{Exfil: true}}},
	}
	spec := nativeSpec("trifecta", "native")
	spec.Tools = tools
	_, err := Build(WithModels(model), WithNativeAgent(spec))
	var trifecta types.ErrTaintTrifecta
	if !errors.As(err, &trifecta) || !trifecta.Exfil || !trifecta.PrivateRead || !trifecta.Untrusted {
		t.Fatalf("Build error = %v, want complete taint trifecta", err)
	}
	if modelCalls != 0 {
		t.Fatalf("model calls = %d, want 0", modelCalls)
	}

	spec.TaintPolicy = func(context.Context, types.ToolSpec, jsontext.Value) (types.TaintAction, []types.ArgTaint) {
		return types.TaintAllow, nil
	}
	if _, err := Build(WithModels(model), WithNativeAgent(spec)); err != nil {
		t.Fatalf("Build with policy: %v", err)
	}
}

func TestNativeWithoutExfilNeedsNoTaintPolicy(t *testing.T) {
	spec := nativeSpec("no-exfil", "native")
	spec.Tools = []types.Tool{&fakeNativeTool{spec: types.ToolSpec{Name: "private", Capabilities: types.Capabilities{PrivateRead: true}}}}
	if _, err := Build(WithModels(fakeModel{profile: profile("native", true)}), WithNativeAgent(spec)); err != nil {
		t.Fatalf("Build without exfil: %v", err)
	}
}

func TestNativeTaintHookReceivesWindow(t *testing.T) {
	var got []types.Block
	hook := func(ctx context.Context, _ types.ToolSpec, _ jsontext.Value) (types.TaintAction, []types.ArgTaint) {
		got, _ = types.TaintWindowFrom(ctx)
		return types.TaintAllow, nil
	}
	cfg := &resolvedNativeConfig{specs: []types.ToolSpec{{Name: "echo"}}, taintPolicy: hook}
	gate := nativeBatchGate(cfg, stores.History{})
	env := &turnEnv{msgs: []types.Message{{Blocks: []types.Block{types.Text{Text: "tool output"}}}}}
	ctx := env.effectCtx(context.Background())
	decision := gate(ctx, types.ToolUse{Name: "echo"})
	if decision.Outcome != runtime.BatchAllow || len(got) == 0 {
		t.Fatalf("decision=%v, hook window=%v", decision.Outcome, got)
	}
}

var _ types.TaintHook
var _ = permission.WithTaintHook
