package types

import (
	"context"
	"io"
	"time"
)

// WorkspaceRef identifies a persisted sandbox snapshot. A PerCall workspace
// opens with the zero value; a PerSession workspace opens with the ref a
// checkpoint restored.
type WorkspaceRef string

// Lifecycle selects how long one workspace lives.
type Lifecycle int

const (
	// PerCall scopes the workspace to one tool call: open, execute, close.
	PerCall Lifecycle = iota
	// PerSession scopes the workspace to the session; it survives across
	// calls through its snapshot ref.
	PerSession
)

// SandboxPolicy states what a workspace may do and how long it lives. An
// empty Egress denies all egress, including DNS.
type SandboxPolicy struct {
	Lifecycle   Lifecycle
	Egress      EgressPolicy
	CPU         string
	Memory      string
	Disk        string
	MaxExecTime time.Duration
	Secrets     []SecretRef
}

// SecretRef grants one named secret value under one named environment
// variable. The value resolves through SecretResolver at Open.
type SecretRef struct {
	Name string
	Env  string
}

// ExecRequest is one command execution inside the workspace.
type ExecRequest struct {
	Argv    []string
	Stdin   []byte
	Cwd     string
	Timeout time.Duration
}

// ExecResult carries the process outcome. A non-zero exit is a result, not
// an error; the error reports the adapter failing to start the process.
type ExecResult struct {
	ExitCode  int
	Stdout    []byte
	Stderr    []byte
	Truncated bool
	Seconds   float64
}

// WorkspaceEntry is one listed path inside a workspace.
type WorkspaceEntry struct {
	Path string
	Size int64
	MIME string
}

// Sandbox opens workspaces under a policy. One method covers both
// lifecycles: the ref argument selects restore or fresh.
type Sandbox interface {
	Open(ctx context.Context, ref WorkspaceRef, p SandboxPolicy) (Workspace, error)
}

// Workspace is one open sandbox. SetEnv is the only path a resolved secret
// value takes into the workspace; Open carries no environment.
type Workspace interface {
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
	Put(ctx context.Context, path string, r io.Reader) error
	Get(ctx context.Context, path string) (io.ReadCloser, error)
	SetEnv(ctx context.Context, env map[string]string) error
	Snapshot(ctx context.Context) (WorkspaceRef, error)
	Close(ctx context.Context) error
}

// WorkspaceLister is the optional listing capability a workspace declares
// beside the port. Callers reach it by type assertion on the value Open
// returned; Workspace never grows a List method.
type WorkspaceLister interface {
	List(ctx context.Context, path string) ([]WorkspaceEntry, error)
}

// SandboxResources enumerates durable sandbox resources and closes one by
// its resource identity, so a later pod can reclaim what a dead pod left.
// It is reached by type assertion on the Sandbox value; the port does not grow.
type SandboxResources interface {
	Enumerate(ctx context.Context) ([]WorkspaceRef, error)
	CloseByRef(ctx context.Context, ref WorkspaceRef) error
}

// WorkspaceResource is the optional resource-identity capability a
// workspace declares beside the port. It exposes the identity of the exact
// live resource Open returned, not a snapshot tag. Callers reach it by
// type assertion on the value Open returned; Workspace never grows a
// ResourceRef method.
type WorkspaceResource interface {
	ResourceRef() WorkspaceRef
}

// WorkspaceOwner fences a live handle to one run and one lease generation.
// The lease generation is store-minted, so a stale owner cannot pass as the
// current one.
type WorkspaceOwner struct {
	RunID           string
	LeaseGeneration uint64
}

// WorkspaceSnapshot is the durable outcome of one publication. Generation
// is the persisted compare-and-set token the store advanced.
type WorkspaceSnapshot struct {
	Ref        WorkspaceRef
	Generation int64
}

// SandboxHandles is the optional lifecycle coordinator beside the Sandbox
// port. It owns one live Workspace per (session, owner), publishes durable
// snapshots and sweeps leftover resources. It is registered on the build
// and resolved from the run context; Sandbox and Workspace never grow.
type SandboxHandles interface {
	Acquire(ctx context.Context, sessionID string, owner WorkspaceOwner) (Workspace, error)
	Publish(ctx context.Context, sessionID string, owner WorkspaceOwner) (WorkspaceSnapshot, error)
	Release(ctx context.Context, sessionID string, owner WorkspaceOwner) error
	Sweep(ctx context.Context, limit int) error
}
