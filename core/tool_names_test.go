package gohan

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestToolNames(t *testing.T) {
	t.Run("tools.name-grammar", func(t *testing.T) {
		for _, name := range []string{"orders.get", "Search", "", "-lead", "1lead", "UPPER", "has space", string(make([]byte, 65))} {
			_, err := NewTool(name, "d", func(context.Context, struct{}) (string, error) { return "", nil })
			if !errors.Is(err, types.ErrToolName) {
				t.Errorf("NewTool(%q) error = %v, want ErrToolName", name, err)
			}
		}
		for _, name := range []string{"a", "orders_get", "get_booking"} {
			tl, err := NewTool(name, "d", func(context.Context, struct{}) (string, error) { return "", nil })
			if err != nil {
				t.Errorf("NewTool(%q) unexpected error: %v", name, err)
			}
			if tl.Spec().Name != name {
				t.Errorf("Spec().Name = %q, want %q", tl.Spec().Name, name)
			}
		}
	})

	t.Run("tools.collision-fails-build", func(t *testing.T) {
		err := CheckToolCollisions([]RegisteredTool{
			{Spec: types.ToolSpec{Name: "search"}, Source: "local"},
			{Spec: types.ToolSpec{Name: "search", Deferred: true}, Source: "deferred"},
		})
		var coll types.ErrToolCollision
		if !errors.As(err, &coll) {
			t.Fatalf("error = %v, want ErrToolCollision", err)
		}
		if coll.Name != "search" {
			t.Errorf("Name = %q, want search", coll.Name)
		}
		if len(coll.Sources) != 2 {
			t.Errorf("Sources = %v, want both registration sources", coll.Sources)
		}
	})

	t.Run("tools.reserved-names", func(t *testing.T) {
		err := CheckToolCollisions([]RegisteredTool{
			{Spec: types.ToolSpec{Name: "read_output"}, Source: "user"},
		})
		var coll types.ErrToolCollision
		if !errors.As(err, &coll) {
			t.Fatalf("error = %v, want ErrToolCollision", err)
		}
		if coll.Name != "read_output" {
			t.Errorf("Name = %q, want read_output", coll.Name)
		}
		hasHarness := false
		for _, s := range coll.Sources {
			if s == HarnessToolSource {
				hasHarness = true
			}
		}
		if len(coll.Sources) != 2 || !hasHarness {
			t.Errorf("Sources = %v, want the harness named as the other source", coll.Sources)
		}
	})

	t.Run("tools.rename-is-new-tool", func(t *testing.T) {
		err := CheckToolSet([]string{"search"}, []string{"find"})
		var drift types.ErrToolSetDrift
		if !errors.As(err, &drift) {
			t.Fatalf("error = %v, want ErrToolSetDrift", err)
		}
		if len(drift.Missing) != 1 || drift.Missing[0] != "search" {
			t.Errorf("Missing = %v, want [search]", drift.Missing)
		}
		if len(drift.Extra) != 1 || drift.Extra[0] != "find" {
			t.Errorf("Extra = %v, want [find]", drift.Extra)
		}
		if err := CheckToolSet([]string{"search"}, []string{"search"}); err != nil {
			t.Errorf("unchanged set error = %v, want nil", err)
		}
	})
}
