package toolsearch_test

import (
	"context"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/toolsearch"
)

func TestDeferredToolsGoverned(t *testing.T) {
	t.Run("tools.governed-while-deferred", func(t *testing.T) {
		catalog := []types.ToolSpec{
			{Name: "search", Effect: types.ReadOnly, Deferred: true},
			{Name: "render_voucher", Description: "Render a voucher", Effect: types.ReadOnly, RequiredScopes: []string{"voucher:read"}, Deferred: true},
			{Name: toolsearch.SearchToolsName, Effect: types.ReadOnly},
		}

		undiscovered := map[string]types.ToolSpec{}
		for _, s := range assembledNames(t, catalog, nil, 1) {
			for _, spec := range catalog {
				if spec.Name == s {
					undiscovered[s] = spec
				}
			}
		}
		principal := func(context.Context) types.RunInfo {
			return types.RunInfo{Principal: types.Principal{Scopes: []string{"other:scope"}}}
		}
		gate := func(lookup func(string) (types.ToolSpec, bool), next types.ToolFunc) types.ToolFunc {
			return permission.Gate(nil,
				permission.WithSpecLookup(lookup),
				permission.WithRunInfo(principal),
			)(next)
		}
		next := func(context.Context, types.ToolUse) (types.ToolResult, error) {
			t.Fatal("call must never reach the tool")
			return types.ToolResult{}, nil
		}

		res, err := gate(func(name string) (types.ToolSpec, bool) {
			s, ok := undiscovered[name]
			return s, ok
		}, next)(context.Background(), types.ToolUse{Name: "render_voucher"})
		if err != nil {
			t.Fatalf("undiscovered call: %v", err)
		}
		if res.Error == nil || !strings.Contains(res.Error.Message, "unknown tool") {
			t.Fatalf("result error = %v, want unknown-tool denial", res.Error)
		}

		discovered := toolsearch.Activate(nil, "render_voucher")
		active := map[string]types.ToolSpec{}
		for _, s := range assembledNames(t, catalog, discovered, 2) {
			for _, spec := range catalog {
				if spec.Name == s {
					active[s] = spec
				}
			}
		}
		if _, ok := active["render_voucher"]; !ok {
			t.Fatal("discovered tool missing from the active registry")
		}

		res, err = gate(func(name string) (types.ToolSpec, bool) {
			s, ok := active[name]
			return s, ok
		}, next)(context.Background(), types.ToolUse{Name: "render_voucher"})
		if err != nil {
			t.Fatalf("discovered call: %v", err)
		}
		if res.Error == nil || !strings.Contains(res.Error.Message, "missing scope voucher:read") {
			t.Fatalf("result error = %v, want scope denial", res.Error)
		}
	})
}
