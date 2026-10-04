package types

import (
	"testing"

	"encoding/json/jsontext"
)

func TestToolArgs(t *testing.T) {
	t.Run("clean object passes", func(t *testing.T) {
		if err := ValidateToolArgs(jsontext.Value(`{"a":1,"b":[true,null]}`)); err != nil {
			t.Fatalf("ValidateToolArgs() = %v, want nil", err)
		}
	})

	t.Run("messages.duplicate-keys-in-tool-args", func(t *testing.T) {
		args := jsontext.Value(`{"a":1,"a":2}`)
		err := ValidateToolArgs(args)
		var argsErr *ToolArgsError
		if err == nil || !asToolArgsError(err, &argsErr) {
			t.Fatalf("ValidateToolArgs(%s) = %v, want *ToolArgsError", args, err)
		}
		if argsErr.Reason != "duplicate_key" {
			t.Fatalf("Reason = %q, want %q", argsErr.Reason, "duplicate_key")
		}
		res := ArgsErrorResult(err)
		if res.Outcome != Failed {
			t.Fatalf("Outcome = %v, want %v", res.Outcome, Failed)
		}
		if res.Error == nil || res.Error.Kind != Permanent {
			t.Fatalf("Error.Kind = %v, want %v", res.Error, Permanent)
		}
	})

	t.Run("invalid utf8", func(t *testing.T) {
		args := jsontext.Value("{\"a\":\"\xff\xfe\"}")
		err := ValidateToolArgs(args)
		var argsErr *ToolArgsError
		if err == nil || !asToolArgsError(err, &argsErr) {
			t.Fatalf("ValidateToolArgs() = %v, want *ToolArgsError", err)
		}
		if argsErr.Reason != "invalid_utf8" {
			t.Fatalf("Reason = %q, want %q", argsErr.Reason, "invalid_utf8")
		}
		res := ArgsErrorResult(err)
		if res.Outcome != Failed {
			t.Fatalf("Outcome = %v, want %v", res.Outcome, Failed)
		}
		if res.Error == nil || res.Error.Kind != Permanent {
			t.Fatalf("Error.Kind = %v, want %v", res.Error, Permanent)
		}
	})

	t.Run("syntax error", func(t *testing.T) {
		args := jsontext.Value(`{"a":}`)
		err := ValidateToolArgs(args)
		var argsErr *ToolArgsError
		if err == nil || !asToolArgsError(err, &argsErr) {
			t.Fatalf("ValidateToolArgs() = %v, want *ToolArgsError", err)
		}
		if argsErr.Reason != "syntax" {
			t.Fatalf("Reason = %q, want %q", argsErr.Reason, "syntax")
		}
		res := ArgsErrorResult(err)
		if res.Outcome != Failed {
			t.Fatalf("Outcome = %v, want %v", res.Outcome, Failed)
		}
		if res.Error == nil || res.Error.Kind != Permanent {
			t.Fatalf("Error.Kind = %v, want %v", res.Error, Permanent)
		}
	})

	t.Run("non-object is syntax", func(t *testing.T) {
		for _, args := range []jsontext.Value{
			jsontext.Value(`[1,2]`),
			jsontext.Value(`42`),
			jsontext.Value(`"x"`),
			jsontext.Value(`null`),
		} {
			err := ValidateToolArgs(args)
			var argsErr *ToolArgsError
			if err == nil || !asToolArgsError(err, &argsErr) {
				t.Fatalf("ValidateToolArgs(%s) = %v, want *ToolArgsError", args, err)
			}
			if argsErr.Reason != "syntax" {
				t.Fatalf("Reason = %q, want %q", argsErr.Reason, "syntax")
			}
			res := ArgsErrorResult(err)
			if res.Outcome != Failed {
				t.Fatalf("Outcome = %v, want %v", res.Outcome, Failed)
			}
			if res.Error == nil || res.Error.Kind != Permanent {
				t.Fatalf("Error.Kind = %v, want %v", res.Error, Permanent)
			}
		}
	})
}

func asToolArgsError(err error, target **ToolArgsError) bool {
	e, ok := err.(*ToolArgsError)
	if ok {
		*target = e
	}
	return ok
}
