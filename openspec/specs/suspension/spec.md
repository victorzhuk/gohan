# Suspension and resume

Capability: `suspension` · Spec v1.3 (ADR-0133) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `suspension` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.3 Suspension

```go
type ResumeToken string

type SuspendReason string

const (
	HumanApproval    SuspendReason = "human_approval"
	AwaitingExternal SuspendReason = "awaiting_external"
	AwaitingBatch    SuspendReason = "awaiting_batch"
	AwaitingTool     SuspendReason = "awaiting_tool"
	AwaitingControl  SuspendReason = "awaiting_control"
	AwaitingInput    SuspendReason = "awaiting_input"
	Scheduled        SuspendReason = "scheduled"
	Preempted        SuspendReason = "preempted"
	HumanHandoff     SuspendReason = "human_handoff"
)

type SuspendError struct {
	Token   ResumeToken
	Reason  SuspendReason
	Payload any
	WakeAt  time.Time
}

type ResumeInput struct {
	Approver *Principal
	Verdict  ApprovalVerdict
	Args     json.RawMessage
	Data     json.RawMessage
	Reason   string
}

func Approve() ResumeInput
func Reject(reason string) ResumeInput
func EditArgs(args json.RawMessage) ResumeInput
func Deliver(data json.RawMessage) ResumeInput
func Continue() ResumeInput

type ApprovalVerdict int

const (
	VerdictApprove ApprovalVerdict = iota
	VerdictReject
	VerdictEdit
)

type InputRequest struct {
	Prompt string
	Schema json.RawMessage
}

var (
	ErrTokenConsumed  = errors.New("gohan: resume token already consumed")
	ErrTokenExpired   = errors.New("gohan: resume token expired")
	ErrTokenMismatch  = errors.New("gohan: resume token belongs to another flow or runtime")
	ErrInputInvalid   = errors.New("gohan: delivered input does not match the requested schema")
)

type Waker interface {
	Schedule(ctx context.Context, t ResumeToken, at time.Time) error
}
```

Sources of suspension:

- Gate `Ask` → `HumanApproval`, payload = pending `ToolUse`.
- A tool returns `gohan.SuspendTool(reason, payload)` → e.g. `AwaitingTool` with a job handle; `Resume(Deliver(result))` becomes the tool result.
- A tool or flow asking the user for structured data → `AwaitingInput` with `InputRequest{Prompt, Schema}`; `Resume(Deliver(data))` is validated against the schema (`interop`).
- A provider batch submission → `AwaitingBatch`.
- `Scheduled` → gohan calls `Waker.Schedule(token, WakeAt)`; the user's scheduler calls `Resume(token, Deliver(nil))`.
- `HumanHandoff` → a tool or flow returned `gohan.SuspendTool(HumanHandoff, HandoffContext{…})`; the run is not suspended but ends `Done{Reason: StopHandedOff}` and the session's `Control` becomes `ControlHandoffRequested` (`flow` *Takeover*); there is no token to resume.
- `Preempted` → the harness suspended the run during `Stack.Shutdown` (`runtime`); the client or `Recover` calls `Resume(token, Continue())`; `Continue()` is valid only for this reason.
- Emergency-control state stale or unavailable before a new effect → `AwaitingControl` (ADR-0079); resumed automatically by the flags provider's freshness watcher or by `Waker` at the maximum wait, after which the effect is denied.

Rules: tokens are single-use; a suspension inside a sub-flow surfaces as the parent's suspension with a chained token (`subflows`); resume on another pod works; `Resume` with a token from a different flow or runtime returns `ErrTokenMismatch` before any component runs.


## Requirements

### Requirement: Suspension and resume

#### Scenario: approve on another pod
ID: `suspension.approve-on-another-pod`
- WHEN the gate returns Ask for `create_booking` on pod 1
- THEN `Invoke` returns `*SuspendError{Reason: HumanApproval}`
- AND WHEN pod 2 calls `Resume(ctx, token, Approve())` with principal `op` in `ctx`
- THEN the tool executes once, as the originator, with `ApprovalFrom(ctx)` = op, and the flow completes

#### Scenario: token reuse
ID: `suspension.token-reuse`
- WHEN the same approval message is delivered twice
- THEN the second `Resume` returns `ErrTokenConsumed` and the tool is not executed again

#### Scenario: reject and edit
ID: `suspension.reject-and-edit`
- WHEN `Reject("no")` / `EditArgs(x)` is used
- THEN the model sees error result "no" / the tool executes with `x`

#### Scenario: approval notice delivered
ID: `suspension.approval-notice-delivered`
- WHEN a `Detached` run suspends with `HumanApproval` and no client is attached
- THEN a `RunNotice{Kind: NoticeSuspended, Reason: "HumanApproval"}` reaches the tenant's `Notifier`, and the approver's client fetches the `ApprovalRequest` through `Inspect(runID)`

#### Scenario: async tool
ID: `suspension.async-tool`
- WHEN a tool returns `SuspendTool(AwaitingTool, handle)`
- THEN `Invoke` returns `*SuspendError{Reason: AwaitingTool, Payload: handle}`
- AND WHEN `Resume(token, Deliver(result))` is called
- THEN `result` becomes the tool result and the run continues

#### Scenario: scheduled
ID: `suspension.scheduled`
- WHEN a flow suspends with `Scheduled` and `WakeAt=t`
- THEN `Waker.Schedule(token, t)` is called exactly once

#### Scenario: mismatch
ID: `suspension.mismatch`
- WHEN a token from flow A is resumed on flow B
- THEN `ErrTokenMismatch` before any component runs
