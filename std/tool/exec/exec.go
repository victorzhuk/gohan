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

	"github.com/victorzhuk/gohan/core/stores"
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
	effect       types.Effect
	effectSet    bool
	store        stores.OutputStore
	tel          types.Telemetry
	acknowledged bool
}

// Option adjusts the runner at construction.
type Option func(*runner)

// AllowHostExec acknowledges that the tool runs on the host without
// isolation. Registration warns without it.
func AllowHostExec() Option {
	return func(r *runner) { r.acknowledged = true }
}

// WithEffect overrides the spec's declared effect. The option wins over the
// declaration and the resolved value is advertised through Spec, so
// permission, journal, shielding, scheduling and timeout behavior consume
// one effect.
func WithEffect(e types.Effect) Option {
	return func(r *runner) { r.effect, r.effectSet = e, true }
}

// WithOutputStore stores output larger than the spec's MaxOutput cap whole
// and counts each store on tel, so read_output can serve the full content
// by the ref the result carries.
func WithOutputStore(s stores.OutputStore, tel types.Telemetry) Option {
	return func(r *runner) { r.store, r.tel = s, tel }
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

// New builds a host runner tool. The resolved effect is one value across
// execution and the advertised spec: WithEffect wins over the supplied
// declaration, an unset declaration is SideEffect, and a timeout maps by
// that effect: Unknown for SideEffect, Failed(Retryable) for ReadOnly or
// Idempotent.
func New(spec types.ToolSpec, cmd Cmd, opts ...Option) types.Tool {
	r := &runner{spec: spec, cmd: cmd, timeout: spec.Timeout}
	if r.timeout <= 0 {
		r.timeout = defaultTimeout
	}
	for _, opt := range opts {
		opt(r)
	}
	if !r.effectSet {
		// Effect declares ReadOnly at int zero; an unset declaration still
		// acts on the world, so it defaults to SideEffect.
		if spec.Effect == types.ReadOnly {
			r.effect = types.SideEffect
		} else {
			r.effect = spec.Effect
		}
	}
	r.spec.Effect = r.effect
	if !r.acknowledged {
		slog.Warn("tool/exec runs on the host without isolation; register with AllowHostExec to acknowledge",
			"tool", r.spec.Name)
	}
	return r
}

// HostExecAcknowledged reports whether construction carried AllowHostExec.
// Registration-time checks read the fact through this method.
func (r *runner) HostExecAcknowledged() bool { return r.acknowledged }

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
	stdout, ref, err := r.capStream(ctx, "stdout", res.Stdout)
	if err != nil {
		return types.ToolResult{}, fmt.Errorf("%s: %w", r.spec.Name, err)
	}
	if res.TimedOut {
		return r.timeoutResult(stdout, ref), nil
	}
	if res.ExitErr != nil {
		stderrBlocks, _, err := r.capStream(ctx, "stderr", res.Stderr)
		if err != nil {
			return types.ToolResult{}, fmt.Errorf("%s: %w", r.spec.Name, err)
		}
		stderr := ""
		if len(stderrBlocks) > 0 {
			if t, ok := stderrBlocks[0].(types.Text); ok {
				stderr = t.Text
			}
		}
		return types.ToolResult{
			Content: stdout,
			Ref:     ref,
			Outcome: types.Failed,
			Error: &types.ToolError{
				Kind:    types.Permanent,
				Message: fmt.Sprintf("exit status %d: %s", res.ExitCode, stderr),
			},
		}, nil
	}
	return types.ToolResult{Content: stdout, Ref: ref, Outcome: types.Succeeded}, nil
}

// capStream renders one output stream through the capper, so an oversized
// stream is truncated with a marker and, when a store is wired, stored whole
// under a ref read_output can serve.
func (r *runner) capStream(ctx context.Context, stream, data string) ([]types.Block, string, error) {
	ri, _ := types.RunInfoFrom(ctx)
	blocks, ref, err := OutputCapper{
		Max:   r.spec.MaxOutput,
		Store: r.store,
		Tel:   r.tel,
		Run:   ri,
		Tool:  r.spec.Name,
	}.Cap(ctx, stream, []byte(data))
	if err != nil {
		return nil, "", err
	}
	return blocks, ref, nil
}

// timeoutResult maps a timeout or context deadline by effect: a SideEffect
// command may have taken effect, so its outcome is Unknown; a ReadOnly or
// Idempotent command can simply be retried.
func (r *runner) timeoutResult(content []types.Block, ref string) types.ToolResult {
	if r.effect == types.SideEffect {
		return types.ToolResult{
			Content: content,
			Ref:     ref,
			Outcome: types.Unknown,
			Error: &types.ToolError{
				Kind:    types.OutcomeUnknown,
				Message: fmt.Sprintf("timed out after %s; side effect may or may not have happened", r.timeout),
			},
		}
	}
	return types.ToolResult{
		Content: content,
		Ref:     ref,
		Outcome: types.Failed,
		Error: &types.ToolError{
			Kind:    types.Retryable,
			Message: fmt.Sprintf("timed out after %s", r.timeout),
		},
	}
}
