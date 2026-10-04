package structured

import (
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestTruncatedToolArgs(t *testing.T) {
	truncatedUse := types.ToolUse{ID: "call-1", Name: "lookup", Args: jsontext.Value(`{"q": "gol`)}
	t.Run("structured-output.truncated-tool-args", func(t *testing.T) {
		if !Truncated(types.FinishMaxTokens) {
			t.Fatal("max_tokens finish not recognised as truncation")
		}
		executed := false
		result := runGuarded(truncatedUse, types.FinishMaxTokens, func(types.ToolUse) []types.Block {
			executed = true
			return nil
		})
		if executed {
			t.Fatal("tool executed on truncated arguments")
		}
		if result.Error == nil || result.Outcome != types.Failed {
			t.Fatalf("truncation result not failed: %+v", result)
		}
		d := DecideTruncated(truncatedUse, types.FinishMaxTokens, 1)
		if !d.Retry || d.MaxTokens <= baseToolOutputTokens {
			t.Fatalf("no single larger-allowance retry decided: %+v", d)
		}
	})
	t.Run("complete args pass through", func(t *testing.T) {
		for _, finish := range []types.FinishReason{types.FinishStop, types.FinishToolUse} {
			if Truncated(finish) {
				t.Fatalf("%s finish treated as truncation", finish)
			}
			d := DecideTruncated(truncatedUse, finish, 1)
			if d.Retry || d.ToolResult.ID != "" || d.Err != nil {
				t.Fatalf("%s finish produced a truncation decision: %+v", finish, d)
			}
		}
	})
	t.Run("truncated args rejected before execution", func(t *testing.T) {
		executed := false
		runGuarded(truncatedUse, types.FinishMaxTokens, func(types.ToolUse) []types.Block {
			executed = true
			return nil
		})
		if executed {
			t.Fatal("tool executed on truncated arguments")
		}
	})
	t.Run("truncation result names the tool", func(t *testing.T) {
		d := DecideTruncated(truncatedUse, types.FinishMaxTokens, 1)
		got := d.ToolResult
		if got.ID != truncatedUse.ID {
			t.Fatalf("result ID %q does not pair with the tool call", got.ID)
		}
		if !contains(got.Content, truncatedUse.Name) {
			t.Fatalf("truncation result does not name tool %q: %+v", truncatedUse.Name, got.Content)
		}
	})
	t.Run("second truncation not retried again", func(t *testing.T) {
		d := DecideTruncated(truncatedUse, types.FinishMaxTokens, 2)
		if d.Retry || d.MaxTokens != 0 || d.ToolResult.ID != "" {
			t.Fatalf("second truncation still retried: %+v", d)
		}
		if !errors.Is(d.Err, types.ErrStructuredOutput) {
			t.Fatalf("exhausted allowance error not wrapped: %v", d.Err)
		}
	})
}

// runGuarded is the execution guard a Drive loop wraps a tool call in: the
// tool runs only when the finish reason clears the truncation rule. The
// retry-execution half belongs to row 22.4.
func runGuarded(tu types.ToolUse, finish types.FinishReason, exec func(types.ToolUse) []types.Block) types.ToolResult {
	if Truncated(finish) {
		return DecideTruncated(tu, finish, 1).ToolResult
	}
	return types.ToolResult{ID: tu.ID, Content: exec(tu), Outcome: types.Succeeded}
}

func contains(blocks []types.Block, substr string) bool {
	for _, b := range blocks {
		if txt, ok := b.(types.Text); ok && strings.Contains(txt.Text, substr) {
			return true
		}
	}
	return false
}
