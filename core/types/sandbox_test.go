package types

import (
	"context"
	"io"
	"testing"
)

type minimalWorkspace struct{}

func (minimalWorkspace) Exec(context.Context, ExecRequest) (ExecResult, error) {
	return ExecResult{}, nil
}
func (minimalWorkspace) Put(context.Context, string, io.Reader) error       { return nil }
func (minimalWorkspace) Get(context.Context, string) (io.ReadCloser, error) { return nil, io.EOF }
func (minimalWorkspace) SetEnv(context.Context, map[string]string) error    { return nil }
func (minimalWorkspace) Snapshot(context.Context) (WorkspaceRef, error)     { return "", nil }
func (minimalWorkspace) Close(context.Context) error                        { return nil }

func TestSandboxPolicyZeroValues(t *testing.T) {
	var p SandboxPolicy
	if p.Lifecycle != PerCall {
		t.Errorf("zero lifecycle = %v, want PerCall", p.Lifecycle)
	}
	if len(p.Egress.Allow) != 0 {
		t.Errorf("zero egress allow = %v, want empty", p.Egress.Allow)
	}
	var ref WorkspaceRef
	if string(ref) != "" {
		t.Errorf("zero WorkspaceRef = %q, want empty", ref)
	}
}

func TestWorkspaceListerNotOnWorkspace(t *testing.T) {
	var w Workspace = minimalWorkspace{}
	if _, ok := w.(WorkspaceLister); ok {
		t.Error("a six-method workspace must not satisfy WorkspaceLister")
	}
	var _ Sandbox = sandboxFunc(nil)
}

type sandboxFunc func(context.Context, WorkspaceRef, SandboxPolicy) (Workspace, error)

func (f sandboxFunc) Open(ctx context.Context, ref WorkspaceRef, p SandboxPolicy) (Workspace, error) {
	return f(ctx, ref, p)
}
