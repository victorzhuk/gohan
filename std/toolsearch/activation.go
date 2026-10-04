package toolsearch

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

// Activation is the run's active deferred tool set: the tools discovery
// has joined to the assembled set for the rest of the run. It rides in
// Checkpoint.Data as JSON, so no store shape changes.
type Activation struct {
	Active []string `json:"active"`
}

// Activate adds the discovered tools to the active set, deduplicated and
// sorted, so the recorded set is byte-stable across resume.
func Activate(active []string, discovered ...string) []string {
	set := make(map[string]struct{}, len(active)+len(discovered))
	for _, n := range active {
		set[n] = struct{}{}
	}
	for _, n := range discovered {
		set[n] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Save records the activation in the checkpoint's Data payload.
func Save(cp *stores.Checkpoint, active []string) error {
	data, err := json.Marshal(Activation{Active: active})
	if err != nil {
		return fmt.Errorf("encode activation: %w", err)
	}
	cp.Data = data
	return nil
}

// Load reads the activation back from a checkpoint. The boolean is false
// when the checkpoint carries no activation, which is a run that never
// discovered a deferred tool.
func Load(cp stores.Checkpoint) (Activation, bool, error) {
	if len(cp.Data) == 0 {
		return Activation{}, false, nil
	}
	var a Activation
	if err := json.Unmarshal(cp.Data, &a); err != nil {
		return Activation{}, false, fmt.Errorf("decode activation: %w", err)
	}
	return a, true, nil
}

// DeferredFilter returns the per-turn filter that keeps every deferred
// tool out of the assembled set until discovery put it in active. The
// meta-tool itself is never deferred.
func DeferredFilter(active []string) std.ToolFilter {
	set := make(map[string]struct{}, len(active))
	for _, n := range active {
		set[n] = struct{}{}
	}
	return func(specs []types.ToolSpec, turn int) []types.ToolSpec {
		var out []types.ToolSpec
		for _, s := range specs {
			if !s.Deferred || s.Name == SearchToolsName {
				out = append(out, s)
				continue
			}
			if _, ok := set[s.Name]; ok {
				out = append(out, s)
			}
		}
		return out
	}
}
