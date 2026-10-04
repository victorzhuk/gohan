package std_test

import (
	"context"
	"reflect"
	"testing"

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
		if err := chains.ValidateToolChain(copied); err != nil {
			t.Fatalf("edited copy must still validate: %v", err)
		}
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
			_, err := chains.RunToolChain(context.Background(), instrumented, func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
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
		if !reflect.DeepEqual(seen, want) {
			t.Fatalf("edited copy ran %v, want %v", seen, want)
		}
	})
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
