package types

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestSandboxPolicyZeroValues(t *testing.T) {
	var p SandboxPolicy
	if p.Lifecycle != PerCall {
		t.Fatalf("zero policy lifecycle = %d, want PerCall", p.Lifecycle)
	}
	if len(p.Egress.Allow) != 0 {
		t.Fatalf("zero policy egress allow = %v, want empty", p.Egress.Allow)
	}
	if p.MaxExecTime != 0 {
		t.Fatalf("zero policy max exec time = %v, want 0", p.MaxExecTime)
	}
	if p.Secrets != nil {
		t.Fatalf("zero policy secrets = %v, want nil", p.Secrets)
	}
}

func TestWorkspaceListerNotOnWorkspace(t *testing.T) {
	ws := &sixMethodWorkspace{}
	var _ Workspace = ws
	var _ interface {
		Exec(context.Context, ExecRequest) (ExecResult, error)
		Put(context.Context, string, io.Reader) error
		Get(context.Context, string) (io.ReadCloser, error)
		SetEnv(context.Context, map[string]string) error
		Snapshot(context.Context) (WorkspaceRef, error)
		Close(context.Context) error
	} = ws

	if _, ok := Workspace(ws).(WorkspaceLister); ok {
		t.Fatal("a six-method workspace must not satisfy WorkspaceLister")
	}
}

type sixMethodWorkspace struct{}

func (*sixMethodWorkspace) Exec(context.Context, ExecRequest) (ExecResult, error) {
	return ExecResult{}, nil
}
func (*sixMethodWorkspace) Put(context.Context, string, io.Reader) error { return nil }
func (*sixMethodWorkspace) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}
func (*sixMethodWorkspace) SetEnv(context.Context, map[string]string) error { return nil }
func (*sixMethodWorkspace) Snapshot(context.Context) (WorkspaceRef, error) {
	return "", nil
}
func (*sixMethodWorkspace) Close(context.Context) error { return nil }
