package std

import (
	"errors"
	"fmt"
	"sort"

	"github.com/victorzhuk/gohan/core/types"
)

// ErrToolFilterWidened reports a filter that returned a tool outside the
// base set: a per-turn filter narrows, it never adds.
var ErrToolFilterWidened = errors.New("gohan.std: tool filter widened the tool set")

// ToolFilter selects the tool subset visible on one turn. It must be
// pure: the same base and turn yield the same subset, which is what makes
// the recomputed set on replay deterministic.
type ToolFilter func(specs []types.ToolSpec, turn int) []types.ToolSpec

// NarrowTools applies filter to base for one turn. The result is a
// deterministic subset of base, emitted in base order regardless of the
// order the filter returned. A filter naming a tool outside base fails
// with ErrToolFilterWidened.
func NarrowTools(base []types.ToolSpec, filter ToolFilter, turn int) ([]types.ToolSpec, error) {
	if filter == nil {
		return base, nil
	}
	chosen := make(map[string]struct{}, len(base))
	for _, s := range filter(base, turn) {
		chosen[s.Name] = struct{}{}
	}
	for name := range chosen {
		if !registered(base, name) {
			return nil, fmt.Errorf("%w: %q", ErrToolFilterWidened, name)
		}
	}
	out := make([]types.ToolSpec, 0, len(chosen))
	for _, s := range base {
		if _, ok := chosen[s.Name]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

func registered(specs []types.ToolSpec, name string) bool {
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}

// ToolSetDrift compares a recorded tool set against the recomputed one.
// A mismatch returns ErrToolSetDrift with the recorded names missing from
// the recomputed set and the recomputed names absent from the recording.
func ToolSetDrift(recorded, recomputed []string) error {
	rec := make(map[string]struct{}, len(recorded))
	for _, n := range recorded {
		rec[n] = struct{}{}
	}
	got := make(map[string]struct{}, len(recomputed))
	for _, n := range recomputed {
		got[n] = struct{}{}
	}
	var missing, extra []string
	for n := range rec {
		if _, ok := got[n]; !ok {
			missing = append(missing, n)
		}
	}
	for n := range got {
		if _, ok := rec[n]; !ok {
			extra = append(extra, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		return types.ErrToolSetDrift{Missing: missing, Extra: extra}
	}
	return nil
}
