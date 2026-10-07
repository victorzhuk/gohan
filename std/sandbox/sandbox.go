// Package sandbox turns a governed sandbox into a model-facing tool set:
// the model writes code and files inside a workspace and reads back
// results, while effect, risk, timeout and egress come from one
// SandboxPolicy.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// ErrNilSandbox is returned by New when no Sandbox is supplied.
var ErrNilSandbox = errors.New("gohan: sandbox tool set requires a sandbox")

// ErrZeroLifecycle is returned by New when the policy declares no
// lifecycle.
var ErrZeroLifecycle = errors.New("gohan: sandbox policy declares no lifecycle")

// ToolSet is the registered shape of a sandbox: the tools flow into the
// NativeSpec.Tools field like any other native tool.
type ToolSet interface {
	Tools() []types.Tool
}

// Option adjusts a sandbox tool set at construction.
type Option func(*config) error

type config struct {
	effect    types.Effect
	store     stores.OutputStore
	maxOutput int
}

// WithOutputStore spills output beyond the spec's MaxOutput cap whole, so
// read_output can serve the full content by the ref the result carries.
func WithOutputStore(s stores.OutputStore) Option {
	return func(c *config) error {
		c.store = s
		return nil
	}
}

// WithMaxOutput overrides the per-stream inline cap; the ToolSpec default
// applies when it is unset.
func WithMaxOutput(n int) Option {
	return func(c *config) error {
		c.maxOutput = n
		return nil
	}
}

func (c config) spiller(ctx context.Context) spiller {
	ri, _ := types.RunInfoFrom(ctx)
	return spiller{store: c.store, run: ri, max: c.maxOutput}
}

// WithEffect declares the tool set's effect. The effect derives from the
// policy, so an option may only tighten it: a weaker effect fails New.
func WithEffect(e types.Effect) Option {
	return func(c *config) error {
		if e < c.effect {
			return fmt.Errorf("%w: derived %s, requested %s", ErrEffectLoosened, effectName(c.effect), effectName(e))
		}
		c.effect = e
		return nil
	}
}

type set struct {
	tools []types.Tool
}

// Tools returns the four sandbox tools in registration order.
func (s set) Tools() []types.Tool { return s.tools }

// Check reconciles a pinned manifest against this set's tool names.
func (s set) Check(pinned []string) error {
	names := make([]string, 0, len(s.tools))
	for _, t := range s.tools {
		names = append(names, t.Spec().Name)
	}
	return gohan.CheckToolSet(pinned, names)
}

// New builds the sandbox tool set for one policy. The policy's egress
// travels into every tool spec unchanged, so an empty Egress denies all
// connections, including DNS.
func New(sb types.Sandbox, p types.SandboxPolicy, opts ...Option) (ToolSet, error) {
	if sb == nil {
		return nil, ErrNilSandbox
	}
	if p.Lifecycle != types.PerCall && p.Lifecycle != types.PerSession {
		return nil, ErrZeroLifecycle
	}
	cfg := config{effect: deriveEffect(p)}
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}
	runCode, err := gohan.NewTool("run_code", "Run shell code inside the sandbox and return its output.",
		func(ctx context.Context, a runCodeArgs) ([]types.Block, error) {
			return withWorkspace(ctx, sb, p, func(ctx context.Context, w types.Workspace) ([]types.Block, error) {
				return runCode(ctx, w, cfg.spiller(ctx), a)
			})
		},
		gohan.WithEffect(cfg.effect),
		gohan.WithEgress(p.Egress),
	)
	if err != nil {
		return nil, err
	}
	writeFile, err := gohan.NewTool("write_file", "Write a file into the sandbox workspace.",
		func(ctx context.Context, a writeFileArgs) ([]types.Block, error) {
			return withWorkspace(ctx, sb, p, func(ctx context.Context, w types.Workspace) ([]types.Block, error) {
				return writeFile(ctx, w, a)
			})
		},
		gohan.WithEffect(cfg.effect),
		gohan.WithEgress(p.Egress),
	)
	if err != nil {
		return nil, err
	}
	readFile, err := gohan.NewTool("read_file", "Read a file from the sandbox workspace.",
		func(ctx context.Context, a readFileArgs) ([]types.Block, error) {
			return withWorkspace(ctx, sb, p, func(ctx context.Context, w types.Workspace) ([]types.Block, error) {
				return readFile(ctx, w, cfg.spiller(ctx), a)
			})
		},
		gohan.WithEffect(cfg.effect),
		gohan.WithEgress(p.Egress),
	)
	if err != nil {
		return nil, err
	}
	listFiles, err := gohan.NewTool("list_files", "List the files in a sandbox workspace directory.",
		func(ctx context.Context, a listFilesArgs) ([]types.Block, error) {
			return withWorkspace(ctx, sb, p, func(ctx context.Context, w types.Workspace) ([]types.Block, error) {
				return listFiles(ctx, w, a)
			})
		},
		gohan.WithEffect(cfg.effect),
		gohan.WithEgress(p.Egress),
	)
	if err != nil {
		return nil, err
	}
	return set{tools: []types.Tool{runCode, writeFile, readFile, listFiles}}, nil
}

type runCodeArgs struct {
	Code string `json:"code"`
}

func runCode(ctx context.Context, w types.Workspace, sp spiller, a runCodeArgs) ([]types.Block, error) {
	res, err := w.Exec(ctx, types.ExecRequest{Argv: []string{"sh", "-c", a.Code}})
	if err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	if st, ok := chains.LimitsStateFrom(ctx); ok {
		st.ChargeSandboxSeconds(res.Seconds)
	}
	stdout, err := sp.stream(ctx, "stdout", res.Stdout, res.Truncated)
	if err != nil {
		return nil, err
	}
	stderr, err := sp.stream(ctx, "stderr", res.Stderr, false)
	if err != nil {
		return nil, err
	}
	blocks := append(stdout, stderr...)
	if res.ExitCode != 0 {
		return blocks, fmt.Errorf("exit status %d", res.ExitCode)
	}
	return blocks, nil
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func writeFile(ctx context.Context, w types.Workspace, a writeFileArgs) ([]types.Block, error) {
	if err := w.Put(ctx, a.Path, strings.NewReader(a.Content)); err != nil {
		return nil, fmt.Errorf("write %s: %w", a.Path, err)
	}
	return resultText(fmt.Sprintf("wrote %s (%d bytes)", a.Path, len(a.Content))), nil
}

type readFileArgs struct {
	Path string `json:"path"`
}

func readFile(ctx context.Context, w types.Workspace, sp spiller, a readFileArgs) ([]types.Block, error) {
	r, err := w.Get(ctx, a.Path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", a.Path, err)
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", a.Path, err)
	}
	return sp.file(ctx, a.Path, data)
}

// resultText renders tool output as model content: the text comes from
// code the model wrote, so downstream guards read it as model-written.
func resultText(s string) []types.Block {
	return []types.Block{types.Text{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginModel}},
		Text:      s,
	}}
}
