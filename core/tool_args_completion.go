package gohan

import (
	"github.com/victorzhuk/gohan/core/types"
)

// validateCompletion checks one call's complete arguments once, at the
// completion boundary. The fragments streamed as ToolArgsDelta previews are
// never inputs and never form an argument set, so only the complete ToolUse
// that arrives with the finished model turn is validated. ok is false when
// the set is truncated or invalid; the returned failed ToolResult goes back
// to the model, the tool never runs and no ToolStarted is emitted, because
// ToolStarted means the gate passed and the call is starting.
func validateCompletion(cu types.ToolUse) (res types.ToolResult, ok bool) {
	err := types.ValidateToolArgs(cu.Args)
	if err == nil {
		return types.ToolResult{}, true
	}
	res = types.ArgsErrorResult(err)
	res.ID = cu.ID
	return res, false
}
