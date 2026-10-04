package structured

import (
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// RepairDecision is what one failed validation yields to the runtime. The
// Drive loop executes the repair turn this decides; this package only
// classifies the failure and bounds the allowance.
type RepairDecision struct {
	// Retry reports whether a repair turn is still allowed.
	Retry bool
	// Turn is the number of failed attempts so far, 1-based.
	Turn int
	// ToolResult is the validation failure handed back to the model for the
	// repair turn. The ID is empty: no tool call occurred, the result is
	// synthetic.
	ToolResult types.ToolResult
	// Class is non-zero when the failure was classified, currently as a
	// refusal phrased as JSON.
	Class types.ErrorClass
	// Err is non-nil when the call fails: the allowance is exhausted or the
	// output was refused.
	Err error
}

// Decide classifies a validation failure under the strategy's bounded
// allowance and yields the decision for the failed attempt (turn counts
// attempts, starting at 1). A refusal phrased as schema-valid JSON is
// classified ClassContentPolicy and never repaired. Otherwise the failure
// repairs while turns remain; the instruction text comes from the prompt
// set's RepairInstruction. Once the allowance is spent, Err wraps
// ErrStructuredOutput and the value never reaches business code.
func (vr ValidateRepair) Decide(out any, vErr error, turn int, instruction string) RepairDecision {
	d := RepairDecision{Turn: turn}
	if class, refused := Classify(out); refused {
		d.Class = class
		d.Err = fmt.Errorf("structured output refused (%v): %w: %s", class, types.ErrStructuredOutput, vErr)
		return d
	}
	if turn >= vr.Max {
		d.Err = fmt.Errorf("repair allowance exhausted after %d turn(s): %w: %s", turn, types.ErrStructuredOutput, vErr)
		return d
	}
	d.Retry = true
	d.ToolResult = types.ToolResult{
		Content: []types.Block{types.Text{Text: instruction + "\n" + vErr.Error()}},
		Outcome: types.Failed,
		Error:   &types.ToolError{Kind: types.Permanent, Message: vErr.Error()},
	}
	return d
}
