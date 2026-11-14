package gohan

import (
	"context"
	"encoding/json"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// subflowResumer is the resume seam a flow subtool exposes to the parent's
// resume path: a child flow that suspended under its own token resumes and
// returns its typed result as bytes the parent settles the pending call
// with. An *types.SuspendError and types.ErrNotSuspendable cross unchanged.
type subflowResumer interface {
	ResumeChild(ctx context.Context, t types.ResumeToken, r stores.ResumeInput) (json.RawMessage, error)
}
