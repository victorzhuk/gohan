package guard

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/types"
)

// descriptionRules reject instruction-like text in tool descriptions:
// directives addressed to the model, disclosure demands and tool-call
// steering. Imported tools default to Untrusted and must pass this check
// at build time.
var descriptionRules = []Rule{
	{
		Name:     "instruction-override",
		Contains: []string{"ignore previous instructions", "ignore all previous", "disregard previous instructions"},
		Action:   guards.Block,
	},
	{
		Name:     "directive",
		Contains: []string{"you must", "never reveal", "do not tell the user", "system prompt"},
		Action:   guards.Block,
	},
	{
		Name:     "tool-steering",
		Contains: []string{"always call", "instead of calling", "before answering"},
		Action:   guards.Block,
	},
}

// CheckDescription rejects a tool description carrying an instruction
// directive. The returned error wraps types.ErrToolDescription and names
// the tool, which is what Build fails with.
func CheckDescription(tool, description string) error {
	g := NewRules("tool-description", descriptionRules...)
	in := guards.GuardInput{
		Stage:  types.StageToolResult,
		Origin: types.Origin{Kind: types.OriginTool, Name: tool},
		Blocks: []types.Block{types.Text{Text: description}},
	}
	d, err := g.Decide(context.Background(), in)
	if err != nil {
		return err
	}
	if d.Value.Action != guards.Block {
		return nil
	}
	return fmt.Errorf("%w: tool %q: %s", types.ErrToolDescription, tool, d.Value.Reason)
}
