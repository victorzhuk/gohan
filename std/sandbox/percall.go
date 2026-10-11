package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/outputs"
)

// withWorkspace runs one workspace lifecycle: Open, the call body, Close.
// The deferred close is guarded by a once, so a body that panics still
// closes exactly once and can neither leak the workspace nor close it
// twice. The body receives the sanitizer built from the resolved secrets,
// because the values are local to this call: the body must apply it at the
// output boundary, before any byte is capped, excerpted or stored.
func withWorkspace(ctx context.Context, sb types.Sandbox, p types.SandboxPolicy, publish bool, body func(context.Context, types.Workspace, outputs.SanitizeFunc) ([]types.Block, error)) ([]types.Block, error) {
	if p.Lifecycle == types.PerSession {
		return perSession(ctx, p, publish, body)
	}
	w, err := sb.Open(ctx, types.WorkspaceRef(""), p)
	if err != nil {
		return nil, fmt.Errorf("open workspace: %w", err)
	}
	var closeOnce sync.Once
	defer closeOnce.Do(func() { closeWorkspace(ctx, w) })
	secrets, err := resolveSecrets(ctx, w, p)
	if err != nil {
		return nil, err
	}
	blocks, err := body(ctx, w, boundarySanitizer(secrets))
	return redactSecrets(ctx, secrets, blocks, err)
}

// closeWorkspace releases a workspace; a close failure cannot change the
// settled result, so it only logs.
func closeWorkspace(ctx context.Context, w types.Workspace) {
	if err := w.Close(ctx); err != nil {
		slog.Warn("sandbox workspace close failed", "error", err)
	}
}
