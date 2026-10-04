package types

import (
	"context"
	"encoding/json"
	"time"
)

// Principal identifies the caller. It never carries secrets; credentials
// travel only in context (see Credential).
type Principal struct {
	Subject string
	Tenant  string
	Scopes  []string
}

// Session scope names gate the session paths on top of plain ownership.
// The control scope is the takeover vocabulary: session:write alone never
// moves a session into human control.
const (
	ScopeSessionRead    = "session:read"
	ScopeSessionWrite   = "session:write"
	ScopeSessionHold    = "session:hold"
	ScopeSessionControl = "session:control"
)

// SessionOwner records which principal opened a session.
type SessionOwner struct {
	Tenant  string
	Subject string
}

// CostTags attribute run cost.
type CostTags struct {
	Feature     string
	Environment string
	CostCenter  string
}

// LatencyClass is declared here because RunInfo carries it; the model
// capability owns its admission semantics.
type LatencyClass int

const (
	Interactive LatencyClass = iota
	Agentic
	Batch
)

// RunMode distinguishes a primary run from a shadow run; the release
// capability owns the shadow semantics.
type RunMode int

const (
	Primary RunMode = iota
	Shadow
)

// RunInfo describes the run a context value belongs to. RootRunID keys
// budgets and recovery for the whole tree; ParentRunID is set only on
// sub-flows.
type RunInfo struct {
	Flow         string
	SessionID    string
	RunID        string
	RootRunID    string
	ParentRunID  string
	Depth        int
	Turn         int
	Principal    Principal
	LatencyClass LatencyClass
	CostTags     CostTags
	Residency    string
	ReleaseID    string
	Variant      string
	Mode         RunMode
}

// Approval records who approved a resume. It travels in ctx on the resumed
// run; tools observe who approved and never execute with approver rights.
// The verdict itself stays stores.ApprovalVerdict on the stores ResumeInput.
type Approval struct {
	Approver Principal
	At       time.Time
}

// InputRequest is the structured prompt a run asks the user for when it
// suspends with AwaitingInput.
type InputRequest struct {
	Prompt string
	Schema json.RawMessage
}

type ctxKey int

const (
	ctxPrincipal ctxKey = iota
	ctxRunInfo
	ctxIdempotencyKey
	ctxApproval
)

// WithApproval attaches the approval a resume delivered. Only harness code
// on the resumed run sets it.
func WithApproval(ctx context.Context, a Approval) context.Context {
	return context.WithValue(ctx, ctxApproval, a)
}

// WithPrincipal attaches the transport-verified principal. Only transport
// and harness code call it.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxPrincipal, p)
}

// WithIdempotencyKey attaches the caller-supplied idempotency key. Only
// transport and harness code call it.
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, ctxIdempotencyKey, key)
}

// WithRunInfo attaches the run identity the driver records for the run in
// ctx. Only harness code calls it.
func WithRunInfo(ctx context.Context, r RunInfo) context.Context {
	return context.WithValue(ctx, ctxRunInfo, r)
}

// PrincipalFrom reports the principal in ctx, or ok == false outside a run.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxPrincipal).(Principal)
	return p, ok
}

// RunInfoFrom reports the run info in ctx, or ok == false outside a run.
func RunInfoFrom(ctx context.Context) (RunInfo, bool) {
	r, ok := ctx.Value(ctxRunInfo).(RunInfo)
	return r, ok
}

// ApprovalFrom reports the approval a resume delivered, or ok == false on a
// run that was not resumed with a decision.
func ApprovalFrom(ctx context.Context) (Approval, bool) {
	a, ok := ctx.Value(ctxApproval).(Approval)
	return a, ok
}

// IdempotencyKey reports the idempotency key in ctx, or ok == false when the
// caller supplied none.
func IdempotencyKey(ctx context.Context) (string, bool) {
	k, ok := ctx.Value(ctxIdempotencyKey).(string)
	return k, ok
}
