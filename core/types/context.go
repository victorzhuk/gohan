package types

import (
	"context"
	"sync"
)

type sandboxHandlesKey struct{}

// WithSandboxHandles installs the run's sandbox coordinator in ctx. The
// harness wires it from the registered instance; tool paths resolve it
// with SandboxHandlesFrom.
func WithSandboxHandles(ctx context.Context, h SandboxHandles) context.Context {
	return context.WithValue(ctx, sandboxHandlesKey{}, h)
}

// SandboxHandlesFrom reports the sandbox coordinator in ctx, or
// ok == false outside a run wired with one.
func SandboxHandlesFrom(ctx context.Context) (SandboxHandles, bool) {
	h, ok := ctx.Value(sandboxHandlesKey{}).(SandboxHandles)
	return h, ok
}

type workspaceOwnerKey struct{}

// WithWorkspaceOwner installs the lease-fenced owner the run's sandbox
// calls acquire and publish under. The harness derives it from the run's
// store-minted lease.
func WithWorkspaceOwner(ctx context.Context, owner WorkspaceOwner) context.Context {
	return context.WithValue(ctx, workspaceOwnerKey{}, owner)
}

// WorkspaceOwnerFrom reports the owner in ctx, or ok == false outside a
// run wired with one.
func WorkspaceOwnerFrom(ctx context.Context) (WorkspaceOwner, bool) {
	owner, ok := ctx.Value(workspaceOwnerKey{}).(WorkspaceOwner)
	return owner, ok
}

// WorkspaceUse records whether the run acquired a sandbox handle. The
// tool path marks it on Acquire; the lifecycle reads it at the suspension
// and terminal seams, so a run with no acquired workspace publishes
// nowhere.
type WorkspaceUse struct {
	mu       sync.Mutex
	acquired bool
}

// MarkAcquired records that this run holds a live sandbox handle.
func (u *WorkspaceUse) MarkAcquired() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.acquired = true
}

// Acquired reports whether the run acquired a sandbox handle.
func (u *WorkspaceUse) Acquired() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.acquired
}

type workspaceUseKey struct{}

// WithWorkspaceUse installs the run's acquisition tracker in ctx.
func WithWorkspaceUse(ctx context.Context, u *WorkspaceUse) context.Context {
	return context.WithValue(ctx, workspaceUseKey{}, u)
}

// WorkspaceUseFrom reports the tracker in ctx, or ok == false outside a
// run wired with one.
func WorkspaceUseFrom(ctx context.Context) (*WorkspaceUse, bool) {
	u, ok := ctx.Value(workspaceUseKey{}).(*WorkspaceUse)
	return u, ok
}
