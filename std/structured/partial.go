// Package structured turns model text output into typed values and
// partial views over streamed deltas.
package structured

import (
	"encoding/json"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// PartialValue is the deep-partial view: containers closed, values unvalidated.
type PartialValue map[string]any

// Partial parses the accumulated text into the declared type and validates it.
// A document that does not parse as Out fails with ErrStructuredOutput; the
// value never reaches the caller.
func Partial[Out any](acc string) (Out, error) {
	var out Out
	if err := json.Unmarshal([]byte(acc), &out); err != nil {
		return out, fmt.Errorf("parse structured output: %w: %s", types.ErrStructuredOutput, err)
	}
	return out, nil
}

// PartialView parses the accumulated text into an unvalidated deep-partial
// view. Text cut off mid-document is closed (unterminated strings and
// containers are terminated) so clients can render the object as it fills.
// A view is never validated, returned or stored as a complete value.
func PartialView(acc string) (PartialValue, error) {
	closed, ok := closePartial(acc)
	if !ok {
		return nil, fmt.Errorf("parse partial view: %w", types.ErrStructuredOutput)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(closed), &v); err != nil {
		return nil, fmt.Errorf("parse partial view: %w: %s", types.ErrStructuredOutput, err)
	}
	return PartialValue(v), nil
}

// closePartial terminates an unterminated string and closes open containers,
// dropping a dangling separator left by the cut.
func closePartial(s string) (string, bool) {
	var stack []byte
	inString, escaped := false, false
	for i := range len(s) {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case inString && c == '"':
			inString = false
		case c == '"':
			inString = true
		case c == '{' || c == '[':
			stack = append(stack, c)
		case c == '}' || c == ']':
			if len(stack) == 0 || (c == '}' && stack[len(stack)-1] != '{') || (c == ']' && stack[len(stack)-1] != '[') {
				return "", false
			}
			stack = stack[:len(stack)-1]
		}
	}

	end := len(s)
	for end > 0 {
		c := s[end-1]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' || c == ':' {
			end--
			continue
		}
		break
	}
	out := s[:end]
	if inString {
		out += `"`
	}
	if escaped {
		// A trailing backslash escapes the quote just appended.
		out = out[:len(out)-2] + `\\` + `"`
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			out += "}"
		} else {
			out += "]"
		}
	}
	return out, true
}
