package state

import (
	"reflect"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type demoState struct {
	Query   string            `json:"query"`
	Filters map[string]string `json:"filters"`
	Count   int               `json:"count"`
	When    time.Time         `json:"when"`
	Hidden  string            `json:"-"`
}

func runSharedStateVersioned(t *testing.T) {
	t.Helper()
	// SetSharedState is called twice against the same session state; the
	// caller (core) owns the counter, this package versions each computed
	// patch, and every update carries exactly one StateChanged.
	var (
		prev    any
		version int64
		events  []types.StateChanged
	)
	first := demoState{Query: "invoices", Count: 0, When: time.Unix(0, 0).UTC()}
	for _, next := range []demoState{
		first,
		{Query: "invoices", Count: 3, When: time.Unix(0, 0).UTC()},
	} {
		version++
		sc, err := Update(version, prev, next)
		if err != nil {
			t.Fatalf("update %d: %v", version, err)
		}
		events = append(events, sc)
		prev = next
	}
	if len(events) != 2 {
		t.Fatalf("got %d StateChanged events, want 2", len(events))
	}
	if events[0].Version != 1 || events[1].Version != 2 {
		t.Fatalf("versions %d then %d, want monotonically increasing 1, 2", events[0].Version, events[1].Version)
	}
	if len(events[0].Patch) != 4 {
		t.Fatalf("first update patch has %d ops, want one add per member: %+v", len(events[0].Patch), events[0].Patch)
	}
	patch := events[1].Patch
	if len(patch) != 1 || patch[0].Op != "replace" || patch[0].Path != "/count" || patch[0].Value != float64(3) {
		t.Fatalf("second update patch %+v, want one replace of /count", patch)
	}
}

func TestStatePatch(t *testing.T) {
	t.Run("working-state.shared-state-versioned", runSharedStateVersioned)

	t.Run("key-wise-add-remove-replace", func(t *testing.T) {
		prev := map[string]any{"a": 1, "b": 2, "c": 3}
		next := map[string]any{"b": 2, "c": 30, "d": 4}
		ops, err := Patch(prev, next)
		if err != nil {
			t.Fatalf("Patch: %v", err)
		}
		want := []types.PatchOp{
			{Op: "remove", Path: "/a"},
			{Op: "add", Path: "/d", Value: float64(4)},
			{Op: "replace", Path: "/c", Value: float64(30)},
		}
		if !reflect.DeepEqual(ops, want) {
			t.Fatalf("ops %+v, want %+v", ops, want)
		}
	})

	t.Run("pointer-escaping", func(t *testing.T) {
		ops, err := Patch(map[string]any{}, map[string]any{"a/b~c": 1})
		if err != nil {
			t.Fatalf("Patch: %v", err)
		}
		if len(ops) != 1 || ops[0].Path != "/a~1b~0c" {
			t.Fatalf("path %q, want /a~1b~0c", ops[0].Path)
		}
	})

	t.Run("nested-and-array-whole", func(t *testing.T) {
		prev := map[string]any{"tags": []any{"x", "y"}, "meta": map[string]any{"n": 1}}
		next := map[string]any{"tags": []any{"x", "z"}, "meta": map[string]any{"n": 1}}
		ops, err := Patch(prev, next)
		if err != nil {
			t.Fatalf("Patch: %v", err)
		}
		want := []types.PatchOp{{Op: "replace", Path: "/tags", Value: []any{"x", "z"}}}
		if !reflect.DeepEqual(ops, want) {
			t.Fatalf("ops %+v, want %+v", ops, want)
		}
	})

	t.Run("equal-is-empty-patch", func(t *testing.T) {
		ops, err := Patch(map[string]any{"a": 1}, map[string]any{"a": 1})
		if err != nil {
			t.Fatalf("Patch: %v", err)
		}
		if len(ops) != 0 {
			t.Fatalf("ops %+v, want none", ops)
		}
	})

	t.Run("unserialisable-is-error", func(t *testing.T) {
		if _, err := Patch(map[string]any{}, map[string]any{"a": make(chan int)}); err == nil {
			t.Fatal("want error for unserialisable value")
		}
	})

	t.Run("unserialisable-previous-is-error", func(t *testing.T) {
		if _, err := Patch(make(chan int), map[string]any{}); err == nil {
			t.Fatal("want error for unserialisable previous value")
		}
	})
}

// TestSharedStateVersioned aliases the scenario test under the name the
// chunk's verify command selects.
func TestSharedStateVersioned(t *testing.T) {
	t.Run("working-state.shared-state-versioned", runSharedStateVersioned)
}
