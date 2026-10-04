package stores

import (
	"context"
	"encoding/json"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// ApprovalVerdict records what an approver decided about a pending call.
type ApprovalVerdict int

const (
	VerdictApprove ApprovalVerdict = iota
	VerdictReject
	VerdictEdit
)

// ResumeInput is the decision a Resume delivers to a suspended run. The
// approver is set by the harness from the verified transport principal;
// callers never supply it.
type ResumeInput struct {
	Approver *types.Principal
	Verdict  ApprovalVerdict
	Args     json.RawMessage
	Data     json.RawMessage
	Reason   string
}

// WorkspaceRef identifies a persisted sandbox snapshot. It is sandbox-owned
// vocabulary declared here because the Checkpoints port depends on it; the
// alias step later replaces it with the shared type.
type WorkspaceRef string

// Checkpoint is everything a suspended run needs to resume on any pod.
// Originator is a Principal by type, so it cannot carry the caller's
// credential: the token stays in ctx and is re-issued on resume.
type Checkpoint struct {
	SchemaVersion  SchemaVersion
	SessionID      string
	Flow           string
	Backend        string
	BackendVersion string
	Reason         types.SuspendReason
	Originator     types.Principal
	Data           []byte
	Child          types.ResumeToken
	Workspace      WorkspaceRef
	ExpiresAt      time.Time
}

// Checkpoints stores suspension checkpoints keyed by single-use resume
// tokens. Consume is atomic: the second caller gets types.ErrTokenConsumed.
type Checkpoints interface {
	Put(ctx context.Context, cp Checkpoint) (types.ResumeToken, error)
	Consume(ctx context.Context, t types.ResumeToken, in ResumeInput) (Checkpoint, error)
	PendingInput(ctx context.Context, runID string) (Checkpoint, ResumeInput, error)
}
