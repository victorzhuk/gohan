package std_test

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"slices"
	"strings"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

func TestExplanation(t *testing.T) {
	t.Run("chains.prompt-strings-accounted-for", func(t *testing.T) {
		preset := std.Interactive()
		set := preset.Prompts
		typ := reflect.TypeOf(set)
		v := reflect.ValueOf(set)
		prompts := make(map[string]string, typ.NumField())
		for i := range typ.NumField() {
			name := typ.Field(i).Name
			val := v.Field(i).String()
			if val == "" {
				t.Errorf("DefaultPrompts.%s is empty", name)
				continue
			}
			prompts[name] = val
		}
		ex := chains.Explanation{
			Steps:   explainSteps(preset.ToolChain),
			Prompts: prompts,
		}
		if len(ex.Prompts) != typ.NumField() {
			t.Fatalf("Prompts covers %d of %d PromptSet fields", len(ex.Prompts), typ.NumField())
		}
		if ex.Prompts["DataNotInstructions"] != set.DataNotInstructions {
			t.Fatal("assembled request prompt must map to its named PromptSet field")
		}
		for _, step := range ex.Steps {
			if step.Name == "" {
				t.Fatal("every step Explain shows must be named")
			}
		}
	})
}

func explainSteps(ch chains.ToolChain) []chains.StepInfo {
	out := make([]chains.StepInfo, 0, len(ch))
	for _, s := range ch {
		out = append(out, chains.StepInfo{Name: s.Name, Kind: s.Kind})
	}
	return out
}

func TestPreset(t *testing.T) {
	t.Run("chains.preset-is-copyable", func(t *testing.T) {
		preset := std.Interactive()
		if err := chains.ValidateToolChain(preset.ToolChain); err != nil {
			t.Fatalf("preset chain must validate as shipped: %v", err)
		}
		copied := make(chains.ToolChain, len(preset.ToolChain))
		copy(copied, preset.ToolChain)
		drop := 0
		copied = append(copied[:drop], copied[drop+1:]...)
		if len(copied) != len(preset.ToolChain)-1 {
			t.Fatalf("edited copy has %d steps, want %d", len(copied), len(preset.ToolChain)-1)
		}

		trace := func(ch chains.ToolChain) []string {
			var seen []string
			instrumented := make(chains.ToolChain, len(ch))
			copy(instrumented, ch)
			for i, step := range instrumented {
				name := step.Name
				use := step.Use
				instrumented[i].Use = func(next chains.ToolFunc) chains.ToolFunc {
					wrapped := use(next)
					return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
						seen = append(seen, name)
						return wrapped(ctx, call)
					}
				}
			}
			_, err := chains.RunToolChain(limitCtx(context.Background()), instrumented, func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
				return types.ToolResult{ID: call.ID}, nil
			})
			if err != nil {
				t.Fatalf("run of %d steps: %v", len(ch), err)
			}
			return seen
		}
		full := trace(preset.ToolChain)
		seen := trace(copied)
		want := append(append([]string{}, full[:drop]...), full[drop+1:]...)
		if !slices.Equal(seen, want) {
			t.Fatalf("edited copy ran %v, want %v", seen, want)
		}
	})
}

// limitCtx carries a fresh ledger, the way the invocation factory will in
// production once the driver lands its per-run creation.
func limitCtx(ctx context.Context) context.Context {
	return chains.WithLimitsState(ctx, chains.NewLimitsState())
}

func TestBatchAndAgenticPrompts(t *testing.T) {
	for name, build := range map[string]func() std.Preset{
		"agentic": std.Agentic,
		"batch":   std.Batch,
	} {
		p := build()
		if p.Prompts.Version == "" {
			t.Errorf("%s preset carries no prompt version", name)
		}
		if err := chains.ValidateToolChain(p.ToolChain); err != nil {
			t.Errorf("%s preset chain does not validate: %v", name, err)
		}
	}
}

func fakeSpend(n int) types.ModelFunc {
	return func(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "x", Usage: &types.Usage{InputTokens: n}}, nil)
		}
	}
}

func collectModel(seq iter.Seq2[types.ModelChunk, error]) error {
	var err error
	for _, e := range seq {
		if e != nil {
			err = e
		}
	}
	return err
}

func okTool(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
	return types.ToolResult{ID: call.ID}, nil
}

func TestPresetLimitsEnforced(t *testing.T) {
	t.Run("maxcost-abort", func(t *testing.T) {
		p := std.Interactive()
		p.Limits.MaxCost = 0.5
		p.Pricing = types.Pricing{Input: 1}
		mw, ok := p.LimitsMiddleware()
		if !ok {
			t.Fatal("preset enforces no limits by default")
		}
		err := collectModel(mw(fakeSpend(1))(limitCtx(context.Background()), types.ModelRequest{}))
		var over *types.LimitExceededError
		if !errors.As(err, &over) || over.Limit != "MaxCost" {
			t.Fatalf("err = %v, want *LimitExceededError{Limit: MaxCost}", err)
		}
	})

	t.Run("no-ledger-forwards", func(t *testing.T) {
		p := std.Interactive()
		p.Limits.MaxCost = 0.5
		p.Pricing = types.Pricing{Input: 1}
		mw, _ := p.LimitsMiddleware()
		// No run ledger in the context: the step forwards and charges
		// nothing, whatever the configured budget says.
		err := collectModel(mw(fakeSpend(100))(context.Background(), types.ModelRequest{}))
		if err != nil {
			t.Fatalf("middleware without a ledger must forward: %v", err)
		}
	})

	t.Run("disabled-no-abort", func(t *testing.T) {
		p := std.Interactive().WithoutLimits()
		p.Limits.MaxToolCalls = 1
		// Two calls through the disabled chain must both pass, where
		// ToolLimits would record the second against MaxToolCalls=1.
		for i := range 2 {
			if _, err := chains.RunToolChain(limitCtx(context.Background()), p.ToolChain, okTool); err != nil {
				t.Fatalf("call %d: %v", i+1, err)
			}
		}
	})
}

func TestPresetOptions(t *testing.T) {
	for name, build := range map[string]func() std.Preset{
		"interactive": std.Interactive,
		"agentic":     std.Agentic,
		"batch":       std.Batch,
	} {
		t.Run(name, func(t *testing.T) {
			p := build()
			opts := p.Options()
			if len(opts) == 0 {
				t.Fatal("preset yields no options")
			}
			if _, err := gohan.Build(opts...); err != nil {
				t.Fatalf("Build rejected preset options: %v", err)
			}

			// The named chain describes only what the preset runs.
			if len(p.ToolChain) != 1 || p.ToolChain[0].Kind != chains.KindLimit || p.ToolChain[0].Name != "limits" {
				t.Fatalf("chain = %v, want a single limits step", explainSteps(p.ToolChain))
			}
		})
	}

	t.Run("runs-spend-full-budget-independently", func(t *testing.T) {
		p := std.Agentic()
		p.Limits.MaxToolCalls = 2
		p.Limits.MaxTurns = 2
		spendRun := func() *chains.LimitsState {
			st := chains.NewLimitsState()
			ctx := chains.WithLimitsState(context.Background(), st)
			mw, ok := p.LimitsMiddleware()
			if !ok {
				t.Fatal("preset yields no limit middleware")
			}
			if err := collectModel(mw(fakeSpend(1))(ctx, types.ModelRequest{})); err != nil {
				t.Fatalf("model call: %v", err)
			}
			if _, err := chains.RunToolChain(ctx, p.ToolChain, okTool); err != nil {
				t.Fatalf("tool call: %v", err)
			}
			return st
		}
		first := spendRun()
		second := spendRun()
		// Each run spent its whole budget: model turn and tool call
		// landed on its own ledger.
		for i, st := range []*chains.LimitsState{first, second} {
			snap := st.Snapshot()
			if snap.Turns != 1 || snap.ToolUses != 1 {
				t.Fatalf("run %d spent %+v, want 1 turn and 1 tool use", i+1, snap)
			}
		}
		// The tool total is shared on one ledger: the batch reservation
		// refuses what the direct call already spent.
		st := chains.NewLimitsState()
		ctx := chains.WithLimitsState(context.Background(), st)
		if _, err := chains.RunToolChain(ctx, p.ToolChain, okTool); err != nil {
			t.Fatalf("tool call: %v", err)
		}
		if _, _, err := st.ReserveBatch(ctx, p.Limits, 2); !errors.Is(err, types.ErrBatchOverrun) {
			t.Fatalf("batch reserve err = %v, want ErrBatchOverrun", err)
		}
	})

	t.Run("ledger-created-after-preset-governs", func(t *testing.T) {
		p := std.Interactive()
		p.Limits.MaxCost = 0.5
		p.Pricing = types.Pricing{Input: 1}
		mw, ok := p.LimitsMiddleware()
		if !ok {
			t.Fatal("preset yields no limit middleware")
		}
		// The preset was built first; the ledger only enters at call
		// time and still governs the spend.
		err := collectModel(mw(fakeSpend(1))(limitCtx(context.Background()), types.ModelRequest{}))
		var over *types.LimitExceededError
		if !errors.As(err, &over) || over.Limit != "MaxCost" {
			t.Fatalf("err = %v, want *LimitExceededError{Limit: MaxCost}", err)
		}
	})

	t.Run("later-withprompts-replaces-set", func(t *testing.T) {
		p := std.Interactive()
		manifestID := func(opts []gohan.Option) string {
			s, err := gohan.Build(opts...)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			return s.Manifest().ID()
		}
		presetOpts := p.Options()
		first := manifestID(presetOpts)
		second := manifestID(append([]gohan.Option{}, presetOpts...))
		if first != second {
			t.Fatal("a preset-alone build must be deterministic")
		}
		custom := p.Prompts
		custom.RepairInstruction = "Repair the answer against the reported problems."
		if manifestID(append(append([]gohan.Option{}, presetOpts...), gohan.WithPrompts(custom))) == manifestID(presetOpts) {
			t.Fatal("a later WithPrompts must change the manifest identity")
		}
	})

	t.Run("without-limits-drops-middleware", func(t *testing.T) {
		p := std.Interactive().WithoutLimits()
		if mw, ok := p.LimitsMiddleware(); ok {
			t.Fatalf("WithoutLimits still returned a middleware %v", mw)
		}
		opts := p.Options()
		if len(opts) != 1 {
			t.Fatalf("WithoutLimits yields %d options, want only WithPrompts", len(opts))
		}
	})
}

func TestPromptMiddleware(t *testing.T) {
	p := std.Interactive()
	mw := std.PromptMiddleware(p.Prompts)
	var got types.ModelRequest
	call := mw(func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		got = req
		return func(yield func(types.ModelChunk, error) bool) {}
	})
	for range call(context.Background(), types.ModelRequest{}) {
	}
	if len(got.System) != 1 {
		t.Fatalf("request carries %d system blocks, want 1", len(got.System))
	}
	ps := p.Prompts
	want := strings.Join([]string{
		ps.FenceOpen, ps.DataNotInstructions, ps.FenceClose,
		ps.OutcomeUnknown, ps.ReadBackHint, ps.OutputRefHint,
		ps.RepairInstruction, ps.NotesPreamble, ps.OperatorTurn,
	}, "\n")
	block, ok := got.System[0].(types.Text)
	if !ok {
		t.Fatalf("system block is %T, want types.Text", got.System[0])
	}
	if block.Text != want {
		t.Fatalf("prompt round trip lost strings:\ngot  %q\nwant %q", block.Text, want)
	}
}
