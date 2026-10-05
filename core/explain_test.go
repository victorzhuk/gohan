package gohan

import (
	"bytes"
	"context"
	"iter"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

type explainModel struct {
	profile types.ModelProfile
	calls   *atomic.Int64
}

func (m *explainModel) Profile() types.ModelProfile { return m.profile }

func (m *explainModel) Generate(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	if m.calls != nil {
		m.calls.Add(1)
	}
	return func(yield func(types.ModelChunk, error) bool) {}
}

func passThroughMW(next types.ModelFunc) types.ModelFunc {
	return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return next(ctx, req)
	}
}

func explainStack(t *testing.T, prompts chains.PromptSet, calls *atomic.Int64) *Stack {
	t.Helper()
	tool := &fakeNativeTool{spec: types.ToolSpec{Name: "search", Executor: types.ByHarness}}
	spec := nativeSpec("agent", "primary")
	spec.Tools = []types.Tool{tool}
	spec.Assemble = func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
		return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
	}
	s, err := Build(
		WithModels(&explainModel{profile: profile("primary", true), calls: calls}),
		WithModelMiddleware(passThroughMW),
		WithPrompts(prompts),
		WithLimits("agent", types.InteractiveLimits),
		WithNativeAgent(spec),
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return s
}

func TestExplain(t *testing.T) {
	prompts := chains.PromptSet{
		Version:             "v1",
		RepairInstruction:   "repair: retry with valid JSON",
		DataNotInstructions: "data, not instructions",
	}

	t.Run("projects the resolved configuration", func(t *testing.T) {
		s := explainStack(t, prompts, nil)
		ex := s.Explain("agent")
		if ex.Flow != "agent" || ex.Profile != "primary" {
			t.Fatalf("flow/profile = %q/%q, want agent/primary", ex.Flow, ex.Profile)
		}
		if ex.Strategies.Structured != "constrained" {
			t.Errorf("strategy = %q, want constrained", ex.Strategies.Structured)
		}
		if len(ex.Steps) != 1 || ex.Steps[0].Name != "model-middleware-1" || ex.Steps[0].Kind != chains.KindUser {
			t.Fatalf("model steps = %+v, want one model-middleware-1 KindUser", ex.Steps)
		}
		if !reflect.DeepEqual(ex.Steps[0].Applies, []string{"search"}) {
			t.Errorf("step applies = %v, want [search]", ex.Steps[0].Applies)
		}
		if len(ex.ToolSteps) != 0 {
			t.Errorf("tool steps = %+v, want none", ex.ToolSteps)
		}
		if ex.Limits.MaxTurns <= 0 {
			t.Errorf("limits = %+v, want resolved preset", ex.Limits)
		}
		if ex.Granularity != "effect" {
			t.Errorf("granularity = %q, want effect", ex.Granularity)
		}
		if ex.Release != s.Manifest().ID() {
			t.Errorf("release = %q, want manifest ID", ex.Release)
		}
		if want := chains.PromptFields(prompts); !reflect.DeepEqual(ex.Prompts, want) {
			t.Errorf("prompts = %v, want the resolved set %v", ex.Prompts, want)
		}
	})

	t.Run("sample request matches execution preparation", func(t *testing.T) {
		s := explainStack(t, prompts, nil)
		ex := s.Explain("agent")
		cfg, ok := s.resolvedNative("agent")
		if !ok {
			t.Fatal("flow not resolved")
		}
		exec, err := s.nativeTurnConfig(cfg).assemble(context.Background(), types.AssembleInput{})
		if err != nil {
			t.Fatalf("execution assemble: %v", err)
		}
		if !reflect.DeepEqual(ex.Sample, exec) {
			t.Errorf("sample = %+v, want the execution request %+v", ex.Sample, exec)
		}
		if len(ex.Sample.System) == 0 {
			t.Error("sample must carry the resolved prompt block")
		}
	})

	t.Run("zero provider calls", func(t *testing.T) {
		var calls atomic.Int64
		s := explainStack(t, prompts, &calls)
		_ = s.Explain("agent")
		if calls.Load() != 0 {
			t.Fatalf("Explain made %d provider calls, want 0", calls.Load())
		}
	})

	t.Run("handle identity resolves the same configuration", func(t *testing.T) {
		s := explainStack(t, prompts, nil)
		c := &conversation{spec: "agent"}
		if ex := s.Explain(c); ex.Flow != "agent" || ex.Profile != "primary" {
			t.Fatalf("handle explanation = %q/%q, want agent/primary", ex.Flow, ex.Profile)
		}
	})

	t.Run("unknown flow yields an empty explanation", func(t *testing.T) {
		s := explainStack(t, prompts, nil)
		if ex := s.Explain("nope"); !reflect.DeepEqual(ex, chains.Explanation{}) {
			t.Errorf("unknown flow explanation = %+v, want zero value", ex)
		}
	})

	t.Run("release follows the resolved prompt set", func(t *testing.T) {
		sA := explainStack(t, prompts, nil)
		changed := prompts
		changed.RepairInstruction = "repair: emit one JSON object"
		sB := explainStack(t, changed, nil)
		if sA.Manifest().ID() == sB.Manifest().ID() {
			t.Error("changing RepairInstruction must change the release identity")
		}
		if sA.Explain("agent").Release == sB.Explain("agent").Release {
			t.Error("Explain.Release must follow the changed prompt set")
		}
	})
}

func TestBuildResolvedMatrixNativeEntries(t *testing.T) {
	var buf bytes.Buffer
	tool := &fakeNativeTool{spec: types.ToolSpec{Name: "search", Executor: types.ByHarness}}
	specA := nativeSpec("agent", "primary")
	specA.Tools = []types.Tool{tool}
	specB := nativeSpec("zflow", "primary")
	_, err := Build(
		WithLogger(slog.New(slog.NewTextHandler(&buf, nil))),
		WithModels(&explainModel{profile: profile("primary", true)}),
		WithNativeAgent(specA),
		WithNativeAgent(specB),
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out := buf.String()
	if got := strings.Count(out, "resolved strategy matrix"); got != 1 {
		t.Fatalf("want exactly one resolved-matrix record, got %d", got)
	}
	for _, want := range []string{"agent", "zflow", "primary", "constrained"} {
		if !strings.Contains(out, want) {
			t.Errorf("resolved-matrix record lacks %q:\n%s", want, out)
		}
	}
}

func TestExplainEmptyChains(t *testing.T) {
	t.Run("chains.empty-chains", func(t *testing.T) {
		var got *types.ModelRequest
		gen := func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			got = &req
			return func(yield func(types.ModelChunk, error) bool) {
				yield(types.ModelChunk{Kind: types.DeltaText, Delta: "hi"}, nil)
				yield(types.ModelChunk{Finish: types.FinishStop}, nil)
			}
		}
		stack := nativeConvStack(t, gen, nil, types.RunLimits{})
		ex := stack.Explain("chat")
		if len(ex.Steps) != 0 {
			t.Fatalf("model steps = %d, want zero for an empty chain", len(ex.Steps))
		}
		if len(ex.ToolSteps) != 0 {
			t.Fatalf("tool steps = %d, want zero for an empty chain", len(ex.ToolSteps))
		}
		for _, b := range ex.Sample.System {
			if txt, ok := b.(types.Text); ok && txt.Text != "" {
				t.Errorf("empty-chain sample system block %q adds prompt text", txt.Text)
			}
		}
		for _, m := range ex.Sample.Messages {
			for _, b := range m.Blocks {
				if txt, ok := b.(types.Text); ok && txt.Text != "" && m.Role != types.RoleUser {
					t.Errorf("empty-chain sample message %s adds %q", m.Role, txt.Text)
				}
			}
		}
		conv := nativeConv(t, stack)
		res := collectStream(conv.Send(types.WithPrincipal(context.Background(), types.Principal{Tenant: "t", Subject: "u1"}), "s-empty", userMsg("hello")))
		if res.err != nil {
			t.Fatalf("send: %v", res.err)
		}
		if got == nil {
			t.Fatal("the model received no assembled request")
		}
		if len(got.System) != 0 {
			t.Fatalf("system blocks = %v, want none: the flow declares no instruction", got.System)
		}
		if len(got.Messages) != 1 || got.Messages[0].Role != types.RoleUser {
			t.Fatalf("messages = %v, want the user input alone", got.Messages)
		}
	})
}
