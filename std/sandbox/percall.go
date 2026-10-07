package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/victorzhuk/gohan/core/types"
)

// withWorkspace runs one workspace lifecycle: Open, the call body, Close.
// The deferred close is guarded by a once, so a body that panics still
// closes exactly once and can neither leak the workspace nor close it
// twice.
func withWorkspace(ctx context.Context, sb types.Sandbox, p types.SandboxPolicy, body func(context.Context, types.Workspace) ([]types.Block, error)) ([]types.Block, error) {
	if p.Lifecycle == types.PerSession {
		return nil, types.ErrPerSessionUnsupported
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
	blocks, err := body(ctx, w)
	return redactSecrets(ctx, secrets, blocks, err)
}

// closeWorkspace releases a workspace; a close failure cannot change the
// settled result, so it only logs.
func closeWorkspace(ctx context.Context, w types.Workspace) {
	if err := w.Close(ctx); err != nil {
		slog.Warn("sandbox workspace close failed", "error", err)
	}
}
