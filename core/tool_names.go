package gohan

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/victorzhuk/gohan/core/types"
)

// toolNamePattern is the tools spec's name grammar: lowercase word
// characters only, one leading letter, at most 64 characters.
var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// reservedToolNames are reserved for the harness meta-tools. A user
// registration under one of them collides with the harness itself.
var reservedToolNames = map[string]struct{}{
	"search_tools":        {},
	"read_output":         {},
	"notes_read":          {},
	"notes_write":         {},
	"memory_read":         {},
	"memory_write":        {},
	"load_skill":          {},
	"read_skill_resource": {},
}

// HarnessToolSource is the source name the build wiring uses when it
// registers the harness's own tools. Reserved names collide with every
// other source.
const HarnessToolSource = "harness"

// ValidToolName reports whether name satisfies the tool name grammar.
func ValidToolName(name string) bool {
	return toolNamePattern.MatchString(name)
}

// CheckToolName enforces the grammar. NewTool and Build both call it: no
// adapter ever rewrites a name.
func CheckToolName(name string) error {
	if !ValidToolName(name) {
		return fmt.Errorf("%w: %q", types.ErrToolName, name)
	}
	return nil
}

// RegisteredTool pairs a tool's spec with the wiring source that
// registered it. Sources are what a collision error names.
type RegisteredTool struct {
	Spec   types.ToolSpec
	Source string
}

// CheckToolCollisions enforces name uniqueness across every registration
// path in one flow, deferred and imported sets included. A harness
// reserved name is treated as registered by HarnessToolSource, so a user
// registration under it fails with the harness named as the other source.
// The first collision found is returned.
func CheckToolCollisions(tools []RegisteredTool) error {
	owners := make(map[string][]string, len(tools)+len(reservedToolNames))
	add := func(name, source string) {
		owners[name] = append(owners[name], source)
	}
	for name := range reservedToolNames {
		add(name, HarnessToolSource)
	}
	for _, t := range tools {
		add(t.Spec.Name, t.Source)
	}
	// Deterministic reporting: iterate names in sorted order.
	names := make([]string, 0, len(owners))
	for name := range owners {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if sources := owners[name]; len(sources) > 1 {
			return types.ErrToolCollision{Name: name, Sources: sources}
		}
	}
	return nil
}

// CheckToolSet enforces the rename rule against the pinned manifest:
// a renamed tool is a new tool, so the old name is reported missing and
// the new one unpinned. Session grants and every other identity keyed on
// ToolSpec.Name never match across the rename because nothing migrates.
func CheckToolSet(pinned, current []string) error {
	pinnedSet := make(map[string]struct{}, len(pinned))
	for _, n := range pinned {
		pinnedSet[n] = struct{}{}
	}
	currentSet := make(map[string]struct{}, len(current))
	for _, n := range current {
		currentSet[n] = struct{}{}
	}
	var missing, extra []string
	for _, n := range pinned {
		if _, ok := currentSet[n]; !ok {
			missing = append(missing, n)
		}
	}
	for _, n := range current {
		if _, ok := pinnedSet[n]; !ok {
			extra = append(extra, n)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return nil
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return types.ErrToolSetDrift{Missing: missing, Extra: extra}
}
