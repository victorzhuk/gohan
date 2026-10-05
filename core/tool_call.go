package gohan

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/victorzhuk/gohan/core/stores"

	"encoding/json/jsontext"

	"github.com/victorzhuk/gohan/core/types"
) // ToolSet resolves a tool by name. It is consumer-owned: the registry itself
// belongs to Build, so the call path takes the lookup as a parameter.
type ToolSet interface {
	Tool(name string) (types.Tool, bool)
}

// ToolSetFunc adapts a lookup function to ToolSet.
type ToolSetFunc func(name string) (types.Tool, bool)

func (f ToolSetFunc) Tool(name string) (types.Tool, bool) { return f(name) }

// retryableError marks an error the tool author classified as transient.
type retryableError struct{ err error }

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// Retryable marks err so a tool call that returns it renders as a
// Retryable failure instead of Permanent.
func Retryable(err error) error {
	if err == nil {
		return nil
	}
	var r *retryableError
	if errors.As(err, &r) {
		return r
	}
	return &retryableError{err: err}
}

// CallTool resolves name in set, validates the complete arguments, executes
// the tool and renders every failure the model should see as a ToolResult.
// SuspendError, AbortError and cancellation are not tool failures: they pass
// through as the returned error. An unknown tool never executes.
func CallTool(ctx context.Context, set ToolSet, name string, args jsontext.Value) (types.ToolResult, error) {
	tool, ok := set.Tool(name)
	if !ok {
		if tel := telemetryFrom(ctx); tel != nil {
			tel.Count(ctx, MetricToolUnknown, 1, types.String(types.KeyToolName, name))
		}
		return types.ToolResult{
			Outcome: types.Failed,
			Error:   &types.ToolError{Kind: types.Permanent, Message: "unknown tool: " + name},
		}, nil
	}
	if err := types.ValidateToolArgs(args); err != nil {
		if tel := telemetryFrom(ctx); tel != nil {
			tel.Count(ctx, MetricToolInvalidArgs, 1,
				types.String(types.KeyToolName, name),
				types.String(types.KeySuspendReason, argsReason(err)),
			)
		}
		return types.ArgsErrorResult(err), nil
	}

	result, err := callRecovered(ctx, tool, args)
	if err != nil {
		var suspended *SuspendError
		var aborted *AbortError
		if errors.As(err, &suspended) || errors.As(err, &aborted) || errors.Is(err, context.Canceled) {
			return types.ToolResult{}, err
		}
		return classifyFailure(ctx, tool.Spec(), err), nil
	}
	if result.Outcome == types.Unknown && result.Error == nil {
		result.Error = &types.ToolError{Kind: types.OutcomeUnknown, Message: "outcome unknown"}
	}
	return result, nil
}

func argsReason(err error) string {
	var argsErr *types.ToolArgsError
	if errors.As(err, &argsErr) {
		return argsErr.Reason
	}
	return "syntax"
}

func outcomeName(res types.ToolResult) string {
	name := outcomeLabel(res.Outcome)
	if res.Error == nil {
		return name
	}
	return name + ":" + errorKindLabel(res.Error.Kind)
}

func outcomeLabel(o types.Outcome) string {
	switch o {
	case types.Succeeded:
		return "succeeded"
	case types.Failed:
		return "failed"
	default:
		return "unknown"
	}
}

func errorKindLabel(k types.ErrorKind) string {
	switch k {
	case types.Permanent:
		return "permanent"
	case types.Retryable:
		return "retryable"
	default:
		return "unknown"
	}
}

// callRecovered runs Call with panic containment: the tool author contract
// turns a recovered panic into Failed(Permanent), or Unknown for a
// SideEffect tool whose external call may already have happened.
func callRecovered(ctx context.Context, tool types.Tool, args jsontext.Value) (result types.ToolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			if tool.Spec().Effect == types.SideEffect {
				result = types.ToolResult{
					Outcome: types.Unknown,
					Error:   &types.ToolError{Kind: types.OutcomeUnknown, Message: fmt.Sprintf("tool panicked: %v", r)},
				}
				return
			}
			result = types.ToolResult{
				Outcome: types.Failed,
				Error:   &types.ToolError{Kind: types.Permanent, Message: fmt.Sprintf("tool panicked: %v", r)},
			}
		}
	}()
	return tool.Call(ctx, args)
}

func classifyFailure(ctx context.Context, spec types.ToolSpec, err error) types.ToolResult {
	kind := types.Permanent
	var denied types.EgressDenied
	switch {
	case errors.As(err, &denied):
		kind = types.Permanent
		recordEgressDenied(ctx, spec, denied)
	case errors.Is(err, context.DeadlineExceeded):
		if spec.Effect == types.SideEffect {
			kind = types.OutcomeUnknown
		} else {
			kind = types.Retryable
		}
	default:
		var r *retryableError
		if errors.As(err, &r) {
			kind = types.Retryable
		}
	}
	outcome := types.Failed
	if kind == types.OutcomeUnknown {
		outcome = types.Unknown
	}
	p := types.ProblemOf(err)
	message := p.Detail
	if errors.As(err, &denied) {
		message = denied.Error()
	}
	if message == "" || message == p.Title {
		message = p.Title
	}
	return types.ToolResult{
		Outcome: outcome,
		Error:   &types.ToolError{Kind: kind, Message: message},
	}
}

func recordEgressDenied(ctx context.Context, spec types.ToolSpec, denied types.EgressDenied) {
	u, _ := url.Parse(denied.URL)
	host := ""
	if u != nil {
		host = u.Hostname()
	}
	run, _ := types.RunInfoFrom(ctx)
	if audit, ok := AuditLogFrom(ctx); ok && audit != nil {
		_ = audit.Append(ctx, stores.AuditRecord{
			Kind: stores.AuditEgressDenied, Tool: spec.Name,
			RunID: run.RunID, SessionID: run.SessionID, Host: host, Reason: denied.Reason,
		})
	}
	if tel := types.TelemetryFrom(ctx); tel != nil {
		tel.Count(ctx, "gohan.egress.requests", 1,
			types.String("tool", spec.Name), types.String("host", host), types.String("decision", "denied"))
	}
}
