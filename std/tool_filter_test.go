package std_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
	std "github.com/victorzhuk/gohan/std"
)

func TestToolManifestAndFilterNarrowOnly(t *testing.T) {
	t.Run("tools.tool-filter-per-turn", func(t *testing.T) {
		base := []types.ToolSpec{
			{Name: "search_catalog", Effect: types.ReadOnly},
			{Name: "create_booking", Effect: types.SideEffect},
		}
		hideBooking := func(specs []types.ToolSpec, turn int) []types.ToolSpec {
			var out []types.ToolSpec
			for _, s := range specs {
				if turn == 1 && s.Name == "create_booking" {
					continue
				}
				out = append(out, s)
			}
			return out
		}

		turn1, err := std.NarrowTools(base, hideBooking, 1)
		if err != nil {
			t.Fatalf("turn 1: %v", err)
		}
		if len(turn1) != 1 || turn1[0].Name != "search_catalog" {
			t.Fatalf("turn 1 set = %v, want only search_catalog", names(turn1))
		}
		turn2, err := std.NarrowTools(base, hideBooking, 2)
		if err != nil {
			t.Fatalf("turn 2: %v", err)
		}
		if len(turn2) != 2 {
			t.Fatalf("turn 2 set = %v, want both tools", names(turn2))
		}

		visible := map[string]types.ToolSpec{}
		for _, s := range turn1 {
			visible[s.Name] = s
		}
		gate := permission.Gate(nil, permission.WithSpecLookup(func(name string) (types.ToolSpec, bool) {
			s, ok := visible[name]
			return s, ok
		}))
		res, err := gate(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			t.Fatal("hidden tool must never run")
			return types.ToolResult{}, nil
		})(context.Background(), types.ToolUse{Name: "create_booking"})
		if err != nil {
			t.Fatalf("gate error = %v, want nil", err)
		}
		if res.Error == nil || !strings.Contains(res.Error.Message, "unknown tool") {
			t.Fatalf("result error = %v, want unknown-tool denial", res.Error)
		}

		widen := func(specs []types.ToolSpec, turn int) []types.ToolSpec {
			return append(specs, types.ToolSpec{Name: "smuggled"})
		}
		if _, err := std.NarrowTools(base, widen, 1); !errors.Is(err, std.ErrToolFilterWidened) {
			t.Fatalf("error = %v, want ErrToolFilterWidened", err)
		}
	})
}
