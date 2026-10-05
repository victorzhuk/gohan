// Package exec provides the host command runner: it executes a typed argv on
// the host without isolation, under the tool port's effect-aware outcome
// contract. Model-authored code must go through the sandbox capability, not
// this package.
package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// defaultTimeout matches the SideEffect default in the tools spec; exec
// defaults to that effect.
const defaultTimeout = 60 * time.Second

// Cmd describes one host command. Argv derives the argument vector from the
// typed tool arguments on every call; a shell string is never accepted. Env
// is the allowlist of KEY=VALUE entries the child may see: the process
// environment is never inherited, so an empty Env means the child runs with
// an empty environment. Dir is the working directory; empty means the
// process's current directory.
type Cmd struct {
	Argv func(args json.RawMessage) ([]string, error)
	Dir  string
	Env  []string
}

type runner struct {
	spec         types.ToolSpec
	cmd          Cmd
	timeout      time.Duration
	acknowledged bool
}

// Option adjusts the runner at construction.
type Option func(*runner)

// AllowHostExec acknowledges that the tool runs on the host without
// isolation. Registration warns without it.
func AllowHostExec() Option {
	return func(r *runner) { r.acknowledged = true }
}

// WithTimeout overrides the per-call timeout. The timeout kills the whole
// process group, so a spawned child cannot outlive the call.
func WithTimeout(d time.Duration) Option {
	return func(r *runner) {
		if d > 0 {
			r.timeout = d
		}
	}
}

// New builds a host runner tool. The spec's Effect decides timeout
// semantics: only a declared SideEffect maps a timeout to an Unknown
// outcome, so pass Effect explicitly rather than relying on the zero value.
func New(spec types.ToolSpec, cmd Cmd, opts ...Option) types.Tool {
	r := &runner{spec: spec, cmd: cmd, timeout: spec.Timeout}
	if r.timeout <= 0 {
		r.timeout = defaultTimeout
	}
	for _, opt := range opts {
		opt(r)
	}
	if !r.acknowledged {
		slog.Warn("tool/exec runs on the host without isolation; register with AllowHostExec to acknowledge",
			"tool", r.spec.Name)
	}
	return r
}

// Spec implements types.Tool.
func (r *runner) Spec() types.ToolSpec { return r.spec }

// Call implements types.Tool. It is safe for concurrent use: all
// per-request state comes from args and ctx.
func (r *runner) Call(ctx context.Context, args json.RawMessage) (types.ToolResult, error) {
	argv, err := r.cmd.Argv(args)
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("%s: build argv: %w", r.spec.Name, err)
	}
	if len(argv) == 0 {
		return types.ToolResult{}, fmt.Errorf("%s: empty argv", r.spec.Name)
	}
	res := runProcess(ctx, runConfig{
		Dir:     r.cmd.Dir,
		Env:     r.cmd.Env,
		Argv:    argv,
		Timeout: r.timeout,
	})
	if res.Err != nil {
		return types.ToolResult{}, fmt.Errorf("%s: %w", r.spec.Name, res.Err)
	}
	if res.TimedOut {
		return r.timeoutResult(res), nil
	}
	if res.ExitErr != nil {
		return types.ToolResult{
			Content: []types.Block{types.Text{Text: res.Stdout}},
			Outcome: types.Failed,
			Error: &types.ToolError{
				Kind:    types.Permanent,
				Message: fmt.Sprintf("exit status %d: %s", res.ExitCode, res.Stderr),
			},
		}, nil
	}
	return types.ToolResult{
		Content: []types.Block{types.Text{Text: res.Stdout}},
		Outcome: types.Succeeded,
	}, nil
}

// timeoutResult maps a timeout or context deadline by effect: a SideEffect
// command may have taken effect, so its outcome is Unknown; a ReadOnly or
// Idempotent command can simply be retried.
func (r *runner) timeoutResult(res processResult) types.ToolResult {
	if r.spec.Effect == types.SideEffect {
		return types.ToolResult{
			Content: []types.Block{types.Text{Text: res.Stdout}},
			Outcome: types.Unknown,
			Error: &types.ToolError{
				Kind:    types.OutcomeUnknown,
				Message: fmt.Sprintf("timed out after %s; side effect may or may not have happened", r.timeout),
			},
		}
	}
	return types.ToolResult{
		Content: []types.Block{types.Text{Text: res.Stdout}},
		Outcome: types.Failed,
		Error: &types.ToolError{
			Kind:    types.Retryable,
			Message: fmt.Sprintf("timed out after %s", r.timeout),
		},
	}
}
