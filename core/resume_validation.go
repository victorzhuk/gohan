package gohan

import (
	"context"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// validateResume runs the pre-Consume guards of the resume seam. A context
// without a principal is refused, and a context that already carries a
// RunInfo belongs to a running tool, sub-flow, provider or decider, which
// may never resume anything (identity.resume-inside-run-refused). Because
// both refusals happen before Checkpoints.Consume, a refused resume leaves
// the token unconsumed.
//
// The approver is derived only from the verified transport principal
// (identity.approver-from-transport-only): a caller-supplied
// ResumeInput.Approver is overwritten, never trusted. The returned context
// carries the approval so the resumed run reads it through ApprovalFrom.
func validateResume(ctx context.Context, in stores.ResumeInput) (context.Context, stores.ResumeInput, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return ctx, in, types.ErrNoPrincipal
	}
	if _, running := RunInfoFrom(ctx); running {
		return ctx, in, types.ErrResumeInsideRun
	}
	in.Approver = &p
	ap := types.Approval{
		Approver: p,
		At:       time.Now().UTC(),
	}
	return types.WithApproval(ctx, ap), in, nil
}
