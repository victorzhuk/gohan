package gohan

import (
	"context"
	"encoding/json/jsontext"
	"iter"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestNativeDefinitionScenarios(t *testing.T) {
	t.Run("flow.native-definition-resolved-once", func(t *testing.T) {
		model := &countingModel{profile: profile("primary", true)}
		search := &fakeNativeTool{spec: types.ToolSpec{Name: "search", Effect: types.ReadOnly}}
		spec := nativeSpec("agent", "primary")
		spec.Tools = []types.Tool{search}
		mw := func(next types.ModelFunc) types.ModelFunc {
			return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
				return next(ctx, req)
			}
		}
		s, err := Build(
			WithModels(model),
			WithModelMiddleware(mw),
			WithLimits("agent", types.InteractiveLimits),
			WithNativeAgent(spec),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if got := model.calls.Load(); got != 0 {
			t.Fatalf("provider calls after build = %d, want 0", got)
		}
		cfg, ok := s.resolvedNative("agent")
		if !ok {
			t.Fatal("definition not resolved")
		}
		if cfg.profile.Name != "primary" {
			t.Errorf("resolved profile = %q, want primary", cfg.profile.Name)
		}
		if !reflect.DeepEqual(cfg.request.Tools, []types.ToolSpec{search.Spec()}) {
			t.Errorf("resolved request tools = %v, want derived from spec", cfg.request.Tools)
		}
		if len(cfg.modelChain) == 0 {
			t.Fatal("resolved model chain is empty")
		}
		if cfg.limits.MaxTurns <= 0 {
			t.Errorf("resolved limits = %+v, want a preset", cfg.limits)
		}
		runs := stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		)
		conv, err := NewNativeConversation(s, "agent",
			WithConversationRuns(runs),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
			WithConversationApprovalPolicy(nrrAllowPolicy{}),
		)
		if err != nil {
			t.Fatalf("new native conversation: %v", err)
		}
		if got := model.calls.Load(); got != 0 {
			t.Fatalf("provider calls after constructor = %d, want 0", got)
		}
		ex := s.Explain(conv)
		if ex.Profile != "primary" {
			t.Errorf("explain profile = %q, want primary", ex.Profile)
		}
		if len(ex.Tools) != 1 || ex.Tools[0].Name != "search" {
			t.Errorf("explain tools = %v, want [search]", ex.Tools)
		}
		if !reflect.DeepEqual(ex.Limits, cfg.limits) {
			t.Errorf("explain limits = %+v, want resolved %+v", ex.Limits, cfg.limits)
		}
		if len(ex.Steps) == 0 {
			t.Error("explain steps = 0, want the resolved chain")
		}
		if got := model.calls.Load(); got != 0 {
			t.Errorf("provider calls after explain = %d, want 0", got)
		}
	})

	t.Run("flow.native-tools-single-source", func(t *testing.T) {
		model := &doubleCallModel{}
		search := &countingExecTool{spec: types.ToolSpec{Name: "search", Effect: types.ReadOnly}}
		spec := nativeSpec("chat", "primary")
		spec.Tools = []types.Tool{search}
		spec.Request.Tools = []types.ToolSpec{{Name: "injected"}}
		spec.Assemble = func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
			return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
		}
		runs := stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		)
		s, err := Build(
			WithStores(stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)), Runs: runs}),
			WithModels(model),
			WithNativeAgent(spec),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		cfg, ok := s.resolvedNative("chat")
		if !ok {
			t.Fatal("definition not resolved")
		}
		var got []string
		for _, ts := range cfg.request.Tools {
			got = append(got, ts.Name)
		}
		if len(got) != 1 || got[0] != "search" {
			t.Fatalf("resolved request tools = %v, want [search]", got)
		}
		conv, err := NewNativeConversation(s, "chat",
			WithConversationRuns(runs),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
			WithConversationApprovalPolicy(nrrAllowPolicy{}),
		)
		if err != nil {
			t.Fatalf("new native conversation: %v", err)
		}
		model.reset(
			types.ToolUse{ID: "c1", Name: "search", Args: jsontext.Value(`{}`)},
			types.ToolUse{ID: "c2", Name: "injected", Args: jsontext.Value(`{}`)},
		)
		var dones []types.Done
		for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("go")) {
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			if d, ok := ev.(types.Done); ok {
				dones = append(dones, d)
			}
		}
		if len(dones) == 0 {
			t.Fatal("send produced no done event")
		}
		if ran := search.ran(); len(ran) != 1 {
			t.Fatalf("search ran %d times (%v), want exactly once", len(ran), ran)
		}
		hist, err := s.stores.SessionLog.Load(nrrCtx(), "s1")
		if err != nil {
			t.Fatalf("load session: %v", err)
		}
		var denied *types.ToolError
		for _, msg := range hist.Messages {
			for _, b := range msg.Blocks {
				res, ok := b.(types.ToolResult)
				if !ok || res.ID != "c2" {
					continue
				}
				if res.Outcome != types.Failed {
					t.Fatalf("injected result outcome = %v, want Failed", res.Outcome)
				}
				denied = res.Error
			}
		}
		if denied == nil || !strings.Contains(denied.Message, "unknown tool") || !strings.Contains(denied.Message, "injected") {
			t.Fatalf("injected denial = %+v, want an unknown-tool failure naming injected", denied)
		}
	})
}

type doubleCallModel struct {
	mu    sync.Mutex
	calls int
	steps []types.ToolUse
}

func (m *doubleCallModel) Profile() types.ModelProfile {
	return profile("primary", true)
}

func (m *doubleCallModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.mu.Lock()
		m.calls++
		answered := 0
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				if _, ok := b.(types.ToolResult); ok {
					answered++
				}
			}
		}
		m.mu.Unlock()
		if answered >= len(m.steps) {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done"}, nil)
			yield(types.ModelChunk{Finish: types.FinishStop}, nil)
			return
		}
		for _, call := range m.steps[answered:] {
			yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: call.ID, Name: call.Name, Args: call.Args}}, nil)
		}
		yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
	}
}

func (m *doubleCallModel) reset(steps ...types.ToolUse) {
	m.mu.Lock()
	m.calls = 0
	m.steps = steps
	m.mu.Unlock()
}

type countingExecTool struct {
	spec  types.ToolSpec
	mu    sync.Mutex
	calls []string
}

func (t *countingExecTool) Spec() types.ToolSpec { return t.spec }

func (t *countingExecTool) Call(_ context.Context, args jsontext.Value) (types.ToolResult, error) {
	t.mu.Lock()
	t.calls = append(t.calls, string(args))
	t.mu.Unlock()
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

func (t *countingExecTool) ran() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.calls...)
}
