package types

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"

	"encoding/json/jsontext"
)

// ToolArgsError classifies why tool arguments were rejected. Reason is a
// metric label: keep the exact strings stable.
type ToolArgsError struct {
	Reason  string
	Message string
}

func (e *ToolArgsError) Error() string {
	return e.Message
}

// ValidateToolArgs rejects arguments that are not a strict JSON object:
// malformed syntax, duplicate member names, invalid UTF-8, or a non-object
// top level. The decoder's default options reject duplicate names, so no
// separate name-tracking pass is needed.
func ValidateToolArgs(args jsontext.Value) error {
	if !utf8.Valid(args) {
		return &ToolArgsError{
			Reason:  "invalid_utf8",
			Message: "tool arguments are not valid UTF-8",
		}
	}
	d := jsontext.NewDecoder(bytes.NewReader(args))
	if d.PeekKind() != '{' {
		return &ToolArgsError{
			Reason:  "syntax",
			Message: "tool arguments must be a JSON object",
		}
	}
	if _, err := d.ReadValue(); err != nil {
		if errors.Is(err, jsontext.ErrDuplicateName) {
			return &ToolArgsError{
				Reason:  "duplicate_key",
				Message: "tool arguments contain a duplicate key",
			}
		}
		return &ToolArgsError{
			Reason:  "syntax",
			Message: "tool arguments are not valid JSON: " + err.Error(),
		}
	}
	return nil
}

// ArgsErrorResult renders a validation failure as the failed tool result the
// model sees. A non-*ToolArgsError is treated as a syntax failure.
func ArgsErrorResult(err error) ToolResult {
	var argsErr *ToolArgsError
	if !errors.As(err, &argsErr) {
		argsErr = &ToolArgsError{Reason: "syntax", Message: err.Error()}
	}
	var b strings.Builder
	b.WriteString("invalid tool arguments (")
	b.WriteString(argsErr.Reason)
	b.WriteString("): ")
	b.WriteString(argsErr.Message)
	return ToolResult{
		Outcome: Failed,
		Error:   &ToolError{Kind: Permanent, Message: b.String()},
	}
}
