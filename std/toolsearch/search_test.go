package toolsearch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
	"github.com/victorzhuk/gohan/std/toolsearch"
)

func deferredCatalog() []types.ToolSpec {
	specs := make([]types.ToolSpec, 0, 46)
	for i := range 40 {
		specs = append(specs, types.ToolSpec{
			Name:        fmt.Sprintf("deferred_tool_%02d", i),
			Description: "A deferred tool",
			Effect:      types.ReadOnly,
			Deferred:    true,
		})
	}
	for i := range 4 {
		specs = append(specs, types.ToolSpec{
			Name:        fmt.Sprintf("loaded_tool_%d", i),
			Description: "Always loaded",
			Effect:      types.ReadOnly,
		})
	}
	specs = append(specs,
		types.ToolSpec{Name: "render_voucher", Description: "Render a voucher PDF", Effect: types.ReadOnly, Deferred: true},
		types.ToolSpec{Name: toolsearch.SearchToolsName, Description: "Search deferred tools", Effect: types.ReadOnly},
	)
	return specs
}

func assembledNames(t *testing.T, catalog []types.ToolSpec, active []string, turn int) []string {
	t.Helper()
	set, err := std.NarrowTools(catalog, toolsearch.DeferredFilter(active), turn)
	if err != nil {
		t.Fatalf("filter turn %d: %v", turn, err)
	}
	return names(set)
}

func contains(set []string, want string) bool {
	for _, n := range set {
		if n == want {
			return true
		}
	}
	return false
}

func names(specs []types.ToolSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Name)
	}
	return out
}

func TestDeferredTools(t *testing.T) {
	t.Run("tools.not-assembled-until-discovered", func(t *testing.T) {
		catalog := deferredCatalog()
		got := assembledNames(t, catalog, nil, 1)
		if len(got) != 5 {
			t.Fatalf("assembled = %d tools, want 5 (4 loaded + search_tools)", len(got))
		}
		for _, name := range got {
			if name != toolsearch.SearchToolsName && !strings.HasPrefix(name, "loaded_tool_") {
				t.Fatalf("deferred tool %q leaked into the assembled set", name)
			}
		}
		if !contains(got, toolsearch.SearchToolsName) {
			t.Fatal("search_tools missing from the assembled set")
		}

		registry := func() []types.ToolSpec { return catalog }
		search, err := toolsearch.NewSearchTools(registry)
		if err != nil {
			t.Fatalf("build search_tools: %v", err)
		}
		res, err := search.Call(context.Background(), json.RawMessage(`{"query":"voucher"}`))
		if err != nil {
			t.Fatalf("search_tools call: %v", err)
		}
		text, ok := res.Content[0].(types.Text)
		if !ok {
			t.Fatalf("content block %T, want Text", res.Content[0])
		}
		if !strings.Contains(text.Text, "render_voucher") {
			t.Fatalf("search result %q, want it to name render_voucher", text.Text)
		}

		active := toolsearch.Activate(nil, "render_voucher")
		after := assembledNames(t, catalog, active, 2)
		if !contains(after, "render_voucher") {
			t.Fatal("discovered tool absent from the assembled set after activation")
		}
	})

	t.Run("tools.activation-persists-across-resume", func(t *testing.T) {
		catalog := deferredCatalog()
		active := toolsearch.Activate(nil, "render_voucher")

		var cp stores.Checkpoint
		if err := toolsearch.Save(&cp, active); err != nil {
			t.Fatalf("save activation: %v", err)
		}
		loaded, ok, err := toolsearch.Load(cp)
		if err != nil || !ok {
			t.Fatalf("load activation: ok=%v err=%v, want loaded", ok, err)
		}
		if !contains(loaded.Active, "render_voucher") {
			t.Fatalf("active set after resume = %v, want render_voucher", loaded.Active)
		}

		recomputed := assembledNames(t, catalog, loaded.Active, 1)
		if !contains(recomputed, "render_voucher") {
			t.Fatalf("recomputed set after resume = %v, want render_voucher", recomputed)
		}

		var drift types.ErrToolSetDrift
		err = std.ToolSetDrift(loaded.Active, recomputedWithout(recomputed, "render_voucher"))
		if !errors.As(err, &drift) {
			t.Fatalf("error = %v, want ErrToolSetDrift", err)
		}
		if len(drift.Missing) != 1 || drift.Missing[0] != "render_voucher" {
			t.Fatalf("drift = %+v, want Missing [render_voucher]", drift)
		}
	})
}

func recomputedWithout(set []string, drop string) []string {
	out := make([]string, 0, len(set))
	for _, n := range set {
		if n != drop {
			out = append(out, n)
		}
	}
	return out
}
