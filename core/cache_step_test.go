package gohan

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

func cacheSpecTool(name string, effect types.Effect) types.Tool {
	return cacheTool{spec: types.ToolSpec{Name: name, Effect: effect}}
}

type cacheTool struct{ spec types.ToolSpec }

func (t cacheTool) Spec() types.ToolSpec { return t.spec }
func (t cacheTool) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	return types.ToolResult{}, nil
}

func cacheFlowStack(t *testing.T, opts ...Option) (*Stack, error) {
	t.Helper()
	base := []Option{
		WithModels(&funcModel{}),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "native",
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{}, nil
			},
			ModelChain: chains.ModelChain{{Name: "prefix", Kind: chains.KindCache}},
		}),
	}
	return Build(append(base, opts...)...)
}

func TestCacheStepRefused(t *testing.T) {
	t.Run("cache.refused-on-conversation", func(t *testing.T) {
		_, err := cacheFlowStack(t)
		if err == nil {
			t.Fatal("cache step on a conversation flow must refuse")
		}
		if !strings.Contains(err.Error(), `cache step "prefix"`) || !strings.Contains(err.Error(), "conversation flow") {
			t.Fatalf("error = %v, want step name and the conversation rule", err)
		}
	})

	t.Run("refused-on-non-readonly-tool", func(t *testing.T) {
		_, err := Build(
			WithModels(&funcModel{}),
			WithNativeAgent(NativeSpec{
				Request: FlowRequest{Name: "agent"},
				Profile: "native",
				Tools:   []types.Tool{cacheSpecTool("writer", types.SideEffect)},
				Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					return types.ModelRequest{}, nil
				},
				ModelChain: chains.ModelChain{{Name: "prefix", Kind: chains.KindCache}},
			}),
		)
		if err == nil {
			t.Fatal("cache step with a side-effect tool must refuse")
		}
		if !strings.Contains(err.Error(), `"writer"`) || !strings.Contains(err.Error(), "non-read-only") {
			t.Fatalf("error = %v, want the tool and the effect rule", err)
		}
	})

	t.Run("allow-cache-opts-out-with-warning", func(t *testing.T) {
		s, err := cacheFlowStack(t, AllowCache())
		if err != nil {
			t.Fatalf("AllowCache must admit the cache step: %v", err)
		}
		warns := s.Explain("chat").Warnings
		if len(warns) != 1 || !strings.Contains(warns[0], `"prefix"`) {
			t.Fatalf("warnings = %v, want one naming the cache step", warns)
		}
	})

	t.Run("no-cache-step-no-warning", func(t *testing.T) {
		s, err := Build(
			WithModels(&funcModel{}),
			AllowCache(),
			WithNativeAgent(NativeSpec{
				Request: FlowRequest{Name: "chat"},
				Profile: "native",
				Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					return types.ModelRequest{}, nil
				},
			}),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if warns := s.Explain("chat").Warnings; len(warns) != 0 {
			t.Fatalf("warnings = %v, want none without a cache step", warns)
		}
	})
}

type gateRuntime struct {
	finish types.FinishReason
	events []types.Event
}

func (rt *gateRuntime) Name() string                         { return "gate" }
func (rt *gateRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }
func (rt *gateRuntime) Start(_ context.Context, _ runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}
func (rt *gateRuntime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if rt.finish != "" {
		recordFinish(ctx, rt.finish)
	}
	return st, rt.events, runtime.DoneStatus, nil
}

func TestCacheWriteGate(t *testing.T) {
	drive := func(t *testing.T, rt runtime.Runtime, uncertain []types.CallKey, opts ...LifecycleOption) int {
		t.Helper()
		var mu sync.Mutex
		writes := 0
		opts = append([]LifecycleOption{
			WithLifecycleCacheWrite(func(context.Context, runtime.State) {
				mu.Lock()
				defer mu.Unlock()
				writes++
			}),
			WithLifecycleUncertain(func(runtime.State) []types.CallKey { return uncertain }),
		}, opts...)
		for _, err := range DriveLifecycle(context.Background(), NewLifecycle(opts...), rt, runtime.AgentRun{}) {
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		return writes
	}

	t.Run("cache.never-cache-failures", func(t *testing.T) {
		uncertain := []types.CallKey{{SessionID: "s1", CallID: "c1"}}
		if writes := drive(t, &gateRuntime{}, uncertain); writes != 0 {
			t.Fatalf("cache writes = %d, want 0 for an Uncertain run", writes)
		}
	})

	t.Run("clean-completion-writes", func(t *testing.T) {
		if writes := drive(t, &gateRuntime{}, nil); writes != 1 {
			t.Fatalf("cache writes = %d, want 1 for a clean completion", writes)
		}
	})

	t.Run("max-tokens-finish-writes-nothing", func(t *testing.T) {
		if writes := drive(t, &gateRuntime{finish: types.FinishMaxTokens}, nil); writes != 0 {
			t.Fatalf("cache writes = %d, want 0 for a max_tokens finish", writes)
		}
	})

	t.Run("refusal-finish-writes-nothing", func(t *testing.T) {
		if writes := drive(t, &gateRuntime{finish: types.FinishRefusal}, nil); writes != 0 {
			t.Fatalf("cache writes = %d, want 0 for a refusal finish", writes)
		}
	})

	t.Run("guard-block-writes-nothing", func(t *testing.T) {
		rt := &gateRuntime{events: []types.Event{
			types.GuardBlocked{Stage: types.StageOutput, Reason: "no"},
			types.Done{Reason: types.StopGuardBlocked},
		}}
		if writes := drive(t, rt, nil); writes != 0 {
			t.Fatalf("cache writes = %d, want 0 for a guard block", writes)
		}
	})
}
