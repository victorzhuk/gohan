package sandbox

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/outputs"
)

// perSession runs one call against the session's live handle. The handle
// outlives the call: only the coordinator's Release closes it. A mutating
// call publishes after the body, and a publication failure settles the
// call as an explicit unknown result, because the mutation may already be
// durable inside the workspace. The recorded error text is sanitized with
// the same pass the output boundary uses, so no raw adapter message is
// stored.
func perSession(ctx context.Context, p types.SandboxPolicy, publish bool, body func(context.Context, types.Workspace, outputs.SanitizeFunc) ([]types.Block, error)) ([]types.Block, error) {
	h, ok := types.SandboxHandlesFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("per-session workspace: %w", types.ErrPerSessionUnsupported)
	}
	ri, ok := types.RunInfoFrom(ctx)
	if !ok || ri.SessionID == "" {
		return nil, fmt.Errorf("per-session workspace: %w", types.ErrPerSessionUnsupported)
	}
	owner, ok := types.WorkspaceOwnerFrom(ctx)
	if !ok || owner.RunID == "" {
		return nil, fmt.Errorf("per-session workspace: %w", types.ErrPerSessionUnsupported)
	}
	w, err := h.Acquire(ctx, ri.SessionID, owner)
	if err != nil {
		return nil, fmt.Errorf("acquire workspace: %w", err)
	}
	if u, ok := types.WorkspaceUseFrom(ctx); ok {
		u.MarkAcquired()
	}
	secrets, err := resolveSecrets(ctx, w, p)
	if err != nil {
		return nil, err
	}
	blocks, bodyErr := body(ctx, w, boundarySanitizer(secrets))
	if publish {
		if _, pubErr := h.Publish(ctx, ri.SessionID, owner); pubErr != nil {
			return blocks, types.UnknownOutcomeError{Message: sanitizeText(secrets, pubErr.Error())}
		}
	}
	return redactSecrets(ctx, secrets, blocks, bodyErr)
}
