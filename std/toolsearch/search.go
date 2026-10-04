// Package toolsearch provides the deferred-tool discovery loop: the
// search_tools meta-tool, the per-turn filter that keeps undiscovered
// deferred tools out of the assembled set, and the activation state that
// rides in the run's checkpoint.
package toolsearch

import (
	"context"
	"strings"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
)

// SearchToolsName is the harness-reserved name of the discovery meta-tool.
const SearchToolsName = "search_tools"

type searchArgs struct {
	Query string `json:"query" desc:"substring matched against deferred tool names and descriptions"`
}

type searchMatch struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// NewSearchTools builds the search_tools meta-tool: ReadOnly, matching the
// query against the deferred specs the registry reports. A discovered
// definition carries name and description only; the tool's schema stays
// with its spec until activation.
func NewSearchTools(registry func() []types.ToolSpec) (types.Tool, error) {
	return gohan.NewTool(SearchToolsName,
		"Search deferred tools by query and return their names and descriptions",
		func(ctx context.Context, args searchArgs) ([]searchMatch, error) {
			var out []searchMatch
			for _, spec := range registry() {
				if spec.Deferred && matches(spec, args.Query) {
					out = append(out, searchMatch{Name: spec.Name, Description: spec.Description})
				}
			}
			return out, nil
		})
}

func matches(spec types.ToolSpec, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}
	haystack := strings.ToLower(spec.Name + " " + spec.Description)
	return strings.Contains(haystack, q)
}
