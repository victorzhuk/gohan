package gohan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
)

func TestToolPolicy(t *testing.T) {
	t.Run("tools.untrusted-effect-cap", func(t *testing.T) {
		declared := types.ToolSpec{Name: "imported_search", Effect: types.SideEffect}

		capped, applied := ApplyToolPolicy(declared, ToolPolicy{Trust: types.Untrusted})
		if !applied {
			t.Fatal("effect cap not applied to an Untrusted tool")
		}
		if capped.Effect != types.ReadOnly {
			t.Fatalf("Effect = %d, want ReadOnly", capped.Effect)
		}

		kept, applied := ApplyToolPolicy(declared, ToolPolicy{Trust: types.Untrusted, MaxEffect: types.SideEffect})
		if applied || kept.Effect != types.SideEffect {
			t.Fatalf("explicit MaxEffect raise must not cap: applied=%v effect=%d", applied, kept.Effect)
		}

		kept, applied = ApplyToolPolicy(declared, ToolPolicy{Trust: types.Trusted})
		if applied || kept.Effect != types.SideEffect {
			t.Fatalf("Trusted tool must not be capped: applied=%v effect=%d", applied, kept.Effect)
		}

		lookup := func(name string) (types.ToolSpec, bool) {
			if name == capped.Name {
				return capped, true
			}
			return types.ToolSpec{}, false
		}
		ran := false
		gate := permission.Gate(nil,
			permission.WithSpecLookup(lookup),
			permission.WithRunInfo(func(context.Context) types.RunInfo { return types.RunInfo{} }),
		)
		res, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			ran = true
			return types.ToolResult{}, nil
		})(context.Background(), types.ToolUse{Name: capped.Name})
		if err != nil {
			t.Fatalf("gate error = %v, want nil", err)
		}
		if !ran {
			t.Fatal("capped ReadOnly tool must pass the gate defaults")
		}
		if res.Error != nil {
			t.Fatalf("result error = %v, want nil", res.Error)
		}

		uncappedGate := permission.Gate(nil,
			permission.WithSpecLookup(func(string) (types.ToolSpec, bool) { return declared, true }),
			permission.WithRunInfo(func(context.Context) types.RunInfo { return types.RunInfo{} }),
		)
		_, err = uncappedGate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			t.Fatal("uncapped SideEffect must not pass the gate")
			return types.ToolResult{}, nil
		})(context.Background(), types.ToolUse{Name: declared.Name})
		var ask *permission.AskError
		if !errors.As(err, &ask) {
			t.Fatalf("error = %v, want AskError", err)
		}
		if !strings.Contains(ask.Error(), "imported_search") {
			t.Fatalf("AskError = %q, want it to name the tool", ask.Error())
		}
	})
}
