package structured

import (
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// baseToolOutputTokens is the output allowance the first turn grants a tool
// call; the single retry doubles it.
const baseToolOutputTokens = 4096

// TruncationDecision is what one truncated tool-call turn yields to the
// runtime. The Drive loop executes the retry turn this decides; this package
// only classifies the truncation and bounds the allowance.
type TruncationDecision struct {
	// Retry reports whether a retry turn with a larger output allowance is
	// still allowed. Exactly one retry is granted.
	Retry bool
	// Attempt is the number of truncated attempts so far, 1-based.
	Attempt int
	// MaxTokens is the output allowance for the retry turn, larger than the
	// previous one. Zero when Retry is false.
	MaxTokens int
	// ToolResult is the truncation result handed back to the model. The ID
	// pairs with the truncated ToolUse, so the turn can be sent back as is.
	ToolResult types.ToolResult
	// Err is non-nil when the retry allowance is spent.
	Err error
}

// Truncated reports whether a turn's tool-call arguments were cut off by the
// output limit. ToolUse carries no truncation flag, so the rule reads the
// finish reason alone: a max_tokens finish means the arguments may be
// incomplete and the tool must not run on them.
func Truncated(finish types.FinishReason) bool {
	return finish == types.FinishMaxTokens
}

// DecideTruncated applies the truncated-arguments rule to a completed turn.
// attempt counts truncated attempts, starting at 1. The first truncation
// yields a truncation result naming the tool and one retry with a doubled
// output allowance; a second truncation exhausts the allowance and the error
// wraps ErrStructuredOutput.
func DecideTruncated(tu types.ToolUse, finish types.FinishReason, attempt int) TruncationDecision {
	d := TruncationDecision{Attempt: attempt}
	if !Truncated(finish) {
		return d
	}
	msg := fmt.Sprintf("tool %q arguments truncated by the output limit", tu.Name)
	if attempt > 1 {
		d.Err = fmt.Errorf("truncated tool arguments after %d attempt(s): %w: %s", attempt, types.ErrStructuredOutput, msg)
		return d
	}
	d.Retry = true
	d.MaxTokens = baseToolOutputTokens * 2
	d.ToolResult = types.ToolResult{
		ID:      tu.ID,
		Content: []types.Block{types.Text{Text: msg}},
		Outcome: types.Failed,
		Error:   &types.ToolError{Kind: types.Permanent, Message: msg},
	}
	return d
}
