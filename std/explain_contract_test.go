package std_test

import (
	"context"
	"encoding/json"
	"iter"
	"maps"
	"strings"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

const explainInstruction = "answer briefly"

type explainContractModel struct{}

func (explainContractModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "interactive", Caps: types.Caps{Tools: true}}
}

func (explainContractModel) Generate(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {}
}

type echoContractTool struct{}

func (echoContractTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "echo", Description: "echoes its input", Executor: types.ByHarness}
}

func (echoContractTool) Call(_ context.Context, _ json.RawMessage) (types.ToolResult, error) {
	return types.ToolResult{ID: "echoed"}, nil
}

func explainInteractive(t *testing.T, prompts chains.PromptSet) (chains.Explanation, string) {
	t.Helper()
	preset := std.Interactive()
	opts := append(preset.Options(),
		gohan.WithModels(explainContractModel{}),
		gohan.WithPrompts(prompts),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:     gohan.FlowRequest{Name: "interactive"},
			Profile:     "interactive",
			Instruction: []types.Block{types.Text{Text: explainInstruction}},
			Tools:       []types.Tool{echoContractTool{}},
			ToolChain:   preset.ToolChain,
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{System: in.System, Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	)
	stack, err := gohan.Build(opts...)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return stack.Explain("interactive"), stack.Manifest().ID()
}

func TestExplainPromptAccounting(t *testing.T) {
	t.Run("chains.prompt-strings-accounted-for", func(t *testing.T) {
		preset := std.Interactive()
		ex, release := explainInteractive(t, preset.Prompts)
		fields := chains.PromptFields(preset.Prompts)
		if len(fields) == 0 {
			t.Fatal("the preset carries no PromptSet")
		}
		if !maps.Equal(ex.Prompts, fields) {
			t.Fatalf("Explain.Prompts = %v, want the full PromptSet accounting %v", ex.Prompts, fields)
		}
		for name, val := range fields {
			if val == "" {
				t.Errorf("PromptSet.%s is empty", name)
				continue
			}
		}
		// A request string may carry several prompt fields joined
		// together, so accounting removes every named field value; what
		// remains must be padding alone. Any authored string would leave
		// unaccounted characters.
		account := func(where string, role types.Role, txt types.Text) {
			if txt.Text == "" || role == types.RoleUser {
				return
			}
			rest := strings.ReplaceAll(txt.Text, explainInstruction, "")
			for _, val := range fields {
				if val != "" {
					rest = strings.ReplaceAll(rest, val, "")
				}
			}
			if strings.TrimSpace(rest) != "" {
				t.Errorf("%s carries unaccounted text %q", where, strings.TrimSpace(rest))
			}
		}
		for _, b := range ex.Sample.System {
			if txt, ok := b.(types.Text); ok {
				account("system", types.RoleSystem, txt)
			}
		}
		for _, m := range ex.Sample.Messages {
			for _, b := range m.Blocks {
				if txt, ok := b.(types.Text); ok {
					account("message", m.Role, txt)
				}
			}
		}
		if ex.Release == "" || ex.Release != release {
			t.Fatalf("Release = %q, want the manifest identity %q", ex.Release, release)
		}
		edited := preset.Prompts
		edited.Version += "-edited"
		ex2, _ := explainInteractive(t, edited)
		if ex2.Release == release {
			t.Fatal("the manifest release identity must change when a prompt field changes")
		}
	})
}
