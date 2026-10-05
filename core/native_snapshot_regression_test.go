package gohan

import (
	"context"
	"encoding/json/jsontext"
	"iter"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type recordingTool struct {
	spec  types.ToolSpec
	mu    sync.Mutex
	calls int
}

func (t *recordingTool) Spec() types.ToolSpec { return t.spec }

func (t *recordingTool) Call(_ context.Context, _ jsontext.Value) (types.ToolResult, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return types.ToolResult{Content: []types.Block{types.Text{Text: "echoed"}}, Outcome: types.Succeeded}, nil
}

func (t *recordingTool) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

type recordingToolMW struct {
	name  string
	mu    sync.Mutex
	calls int
}

func (m *recordingToolMW) wrap(next types.ToolFunc) types.ToolFunc {
	return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
		m.mu.Lock()
		m.calls++
		m.mu.Unlock()
		return next(ctx, call)
	}
}

func (m *recordingToolMW) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func toolCallTurn() types.ModelFunc {
	return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			for _, m := range req.Messages {
				for _, b := range m.Blocks {
					if _, ok := b.(types.ToolResult); ok {
						yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done"}, nil)
						yield(types.ModelChunk{Finish: types.FinishStop}, nil)
						return
					}
				}
			}
			yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "c1", Name: "echo", Args: jsontext.Value(`{}`)}}, nil)
			yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
		}
	}
}

func TestResolveNativeOwnsDefinitionSlices(t *testing.T) {
	for _, tc := range []struct {
		name string
		mw   types.ModelMiddleware
	}{
		{name: "no-build-middleware"},
		{name: "with-build-middleware", mw: func(next types.ModelFunc) types.ModelFunc { return next }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := &recordingTool{spec: types.ToolSpec{Name: "search", Executor: types.ByHarness}}
			toolStep := chains.Step[types.ToolMiddleware]{Name: "tstep", Kind: chains.KindUser,
				Use: func(next types.ToolFunc) types.ToolFunc { return next }}
			modelStep := chains.Step[types.ModelMiddleware]{Name: "mstep", Kind: chains.KindUser,
				Use: func(next types.ModelFunc) types.ModelFunc { return next }}
			spec := nativeSpec("agent", "primary")
			spec.Tools = []types.Tool{tool}
			spec.ToolChain = chains.ToolChain{toolStep}
			spec.ModelChain = chains.ModelChain{modelStep}
			spec.Assemble = func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{}, nil
			}

			opts := []Option{
				WithModels(fakeModel{profile: profile("primary", true)}),
				WithLimits("agent", types.InteractiveLimits),
				WithNativeAgent(spec),
			}
			if tc.mw != nil {
				opts = append(opts, WithModelMiddleware(tc.mw))
			}
			s, err := Build(opts...)
			if err != nil {
				t.Fatalf("build: %v", err)
			}

			spec.Tools[0] = &recordingTool{spec: types.ToolSpec{Name: "decoy", Executor: types.ByHarness}}
			spec.ToolChain[0] = chains.Step[types.ToolMiddleware]{Name: "mutated", Kind: chains.KindUser}
			spec.ModelChain[0] = chains.Step[types.ModelMiddleware]{Name: "mutated", Kind: chains.KindUser}

			cfg, ok := s.resolvedNative("agent")
			if !ok {
				t.Fatal("flow not resolved")
			}
			if got := cfg.tools[0].Spec().Name; got != "search" {
				t.Errorf("resolved tool = %q, want search", got)
			}
			if len(cfg.toolChain) != 1 || cfg.toolChain[0].Name != "tstep" {
				t.Errorf("resolved tool chain = %+v, want tstep", cfg.toolChain)
			}
			wantModelSteps := 1
			if tc.mw != nil {
				wantModelSteps = 2
			}
			if len(cfg.modelChain) != wantModelSteps {
				t.Fatalf("resolved model chain = %d steps, want %d", len(cfg.modelChain), wantModelSteps)
			}
			if cfg.modelChain[0].Name != "mstep" {
				t.Errorf("first model step = %q, want mstep", cfg.modelChain[0].Name)
			}

			ex := s.Explain("agent")
			var stepNames []string
			for _, st := range ex.Steps {
				stepNames = append(stepNames, st.Name)
			}
			if len(stepNames) != wantModelSteps || stepNames[0] != "mstep" {
				t.Errorf("explain steps = %v, want mstep first", stepNames)
			}
			var toolStepNames []string
			for _, st := range ex.ToolSteps {
				toolStepNames = append(toolStepNames, st.Name)
			}
			if len(toolStepNames) != 1 || toolStepNames[0] != "tstep" {
				t.Errorf("explain tool steps = %v, want tstep", toolStepNames)
			}
		})
	}
}

func TestNativeConversationDefinitionMutationDoesNotChangeExecution(t *testing.T) {
	echo := &recordingTool{spec: types.ToolSpec{Name: "echo", Effect: types.ReadOnly}}
	decoyTool := &recordingTool{spec: types.ToolSpec{Name: "echo", Effect: types.ReadOnly}}
	toolMW := &recordingToolMW{name: "tool"}
	decoyToolMW := &recordingToolMW{name: "decoy-tool"}
	modelMW := &countingMW{}
	decoyModelMW := &countingMW{}

	spec := NativeSpec{
		Request:    FlowRequest{Name: "chat"},
		Profile:    "native",
		Tools:      []types.Tool{echo},
		ToolChain:  chains.ToolChain{{Name: "toolstep", Kind: chains.KindUser, Use: toolMW.wrap}},
		ModelChain: chains.ModelChain{{Name: "modelstep", Kind: chains.KindUser, Use: modelMW.wrap}},
		Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
		},
	}
	stack, err := Build(
		WithStores(stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}),
		WithModels(&funcModel{fn: toolCallTurn()}),
		WithLimits("chat", nativeConvLimits(4)),
		WithNativeAgent(spec),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	spec.Tools[0] = decoyTool
	spec.ToolChain[0].Use = decoyToolMW.wrap
	spec.ModelChain[0].Use = decoyModelMW.wrap

	conv := nativeConv(t, stack)
	res := collectStream(conv.Send(principalCtx(context.Background()), "s1", userMsg("go")))
	if res.err != nil {
		t.Fatalf("send: %v", res.err)
	}
	if echo.count() != 1 {
		t.Errorf("original tool calls = %d, want 1", echo.count())
	}
	if decoyTool.count() != 0 {
		t.Errorf("decoy tool calls = %d, want 0", decoyTool.count())
	}
	if toolMW.count() != 1 {
		t.Errorf("original tool middleware calls = %d, want 1", toolMW.count())
	}
	if decoyToolMW.count() != 0 {
		t.Errorf("decoy tool middleware calls = %d, want 0", decoyToolMW.count())
	}
	if modelMW.count() != 2 {
		t.Errorf("original model middleware calls = %d, want 2", modelMW.count())
	}
	if decoyModelMW.count() != 0 {
		t.Errorf("decoy model middleware calls = %d, want 0", decoyModelMW.count())
	}
	var finished, done int
	for _, ev := range res.evs {
		switch ev.(type) {
		case types.ToolFinished:
			finished++
		case types.Done:
			done++
		}
	}
	if finished != 1 || done == 0 {
		t.Errorf("tool results %d, done %d, want 1 result and a done", finished, done)
	}
}
