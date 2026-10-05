# Permission gate and approvals

Capability: `permission` · Spec v1.3 (ADR-0127) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `permission` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.10 Permission gate

```go
type Verdict int

const (
	Allow Verdict = iota
	DenyVerdict
	Ask
)

func permission.Gate(d gohan.Decider[*gohan.ToolInvocation, Verdict], opts ...permission.Option) gohan.ToolMiddleware
```

Defaults without a decider: `ReadOnly` → Allow, `Idempotent` → Allow, `SideEffect` → Ask. `MinConfidence(x)`: below threshold → Ask. Scope check always runs first and cannot be overridden.

```go
// (illustrative) names may change during M0; see contract tiers
type ApprovalRequest struct {
	Tool          ToolSpec
	Call          ToolUse
	Fingerprint   Fingerprint
	Risk          RiskTier
	Reversible    bool
	ReadBack      string
	ArgOrigins    map[string]Origin
	DiffFromLast  json.RawMessage
	Consequence   string
	ExpiresAt     time.Time
	OnExpiry      ExpiryAction
	Eligible      Eligibility
}

type Eligibility struct {
	Scopes          []string
	ExcludeSubjects []string
	Quorum          int
}

type ApprovalPolicy struct {
	Scope                  string
	SeparateFromOriginator bool
	Quorum                 int
	Escalation             []string
}

type ApprovedVia struct {
	Scope     string
	Policy    RiskTier
	ActingFor string
}

var ErrApproverNotEligible = errors.New("gohan: principal may not approve this request")

type ExpiryAction int

const (
	RejectOnExpiry ExpiryAction = iota
	EscalateOnExpiry
)

// (illustrative) the gate decider's input
type ToolInvocation struct {
	Spec   ToolSpec
	Call   ToolUse
	Run    RunInfo
	Taints []ArgTaint
}

type Grant struct {
	Tool        string
	Fingerprint Fingerprint
	Subject     string
	Approver    string
	ExpiresAt   time.Time
}

func ApproveScope(ttl time.Duration) ResumeInput
```

Order inside the gate: hard blocks (scope check, live emergency flags, `Deny` from rules) → taint policy (`taint`; `Deny` fails the call, `Ask` forces suspension regardless of grants) → existing session grant for `(tool, fingerprint, subject)` → decider → `Ask`. A live emergency deny is re-checked on resume after approval and wins over the approval. `Ask` suspends with an `ApprovalRequest` as payload; `DiffFromLast` is computed against the last approved call with the same tool in the session; `ArgOrigins` says per argument whether it came from user text, a tool result or model inference. `Resume(ApproveScope(2h))` approves the call (the approver comes from the ctx principal) and stores a `Grant` in session metadata; any change in fingerprint (or in the declared `FingerprintFields`) misses the grant. Grants never outlive the session, are capped by `MaxGrantTTL` (default 4 h), and are audit records (`AuditGrant`). Tenant-wide standing permissions are expressed as rules or scopes in reviewed configuration, not as grants.

**Who may approve.** An `ApprovalPolicy` per `RiskTier` is evaluated inside `Resume` before an `Approve()`, `EditArgs()` or `ApproveScope()` is accepted; `Reject()` needs only session access. `Resume` reloads the request's `ApprovalRequest` from `Checkpoint.Data` and evaluates the policy for its `Risk` against the persisted `History.Owner`, the transport principal and the request's own eligibility. The policy comes from an injected `permission.ApprovalPolicySource` (`Conversation` option; `std/permission` supplies the default over `TierPolicy`), never from core's own choice: a conversation configured without a source refuses `Approve()`, `EditArgs()` and `ApproveScope()` with `ErrApproverNotEligible`, and the policy is resolved again before a decision is accepted so a tightened policy applies to approvals already collected. Owner exceptions use the session owner, never `Checkpoint.Originator`, and never bypass the `RiskHigh` scope requirement. `Reject()` needs only session access. A collected approval is stored in the checkpoint and bound to the session, run, argument generation, call id, tool name and complete argument value; authorization is rechecked immediately before the tool executes, because a fingerprint can be unchanged while a non-fingerprinted argument changed. `EditArgs()` is a revision proposal: it requires edit eligibility, increments the argument generation, clears every collected approval, and leaves the request pending for its own approvals. Approvals and rejections are recorded as a trusted `gohan.approval` receipt in `Message.Meta` when they reach quorum, and previous approved arguments for `DiffFromLast` are read from the latest such receipt for the tool, never from `Journal.ByFingerprint`. Defaults (`std/permission`, overridable with `agent.WithApprovalPolicy(tier, p)`; `flowdef` declares `approval:` per tool): `RiskLow` — the session owner or `session:write`; `RiskMedium` — the owner or `approve:<tool>`; `RiskHigh` — `approve:<tool>` **and** `SeparateFromOriginator` (the approver's subject differs from `Checkpoint.Originator.Subject` and from the run's calling principal), `Quorum` 1; `RiskHigh` with `Reversible: false` defaults to `Quorum` 2. An ineligible principal gets `ErrApproverNotEligible` (`Permanent`), the token stays unconsumed and audit records `approval_refused{reason}` with `reason` one of `separate_from_originator` (self-approval; `permission.self-approval-refused-high-risk`), `missing_scope`, `excluded_subject` or `duplicate_approver` (a repeated quorum approval; `permission.quorum-two-approvers`). With `Quorum > 1` the token stays valid until quorum: each partial approval is recorded in the checkpoint, approvers must be distinct subjects, and `EditArgs` is accepted only from the first approver. `ApprovalRequest.Eligible` carries the resolved scopes, excluded subjects and quorum so transports can route the request; `EscalateOnExpiry` walks `ApprovalPolicy.Escalation` (scopes in order) and passes the next target to `Waker.Schedule` in the payload. Journal and audit record `Approver` and `ApprovedVia{Scope, Policy, ActingFor}`, where `ActingFor` is the human whose credential was exchanged when a service approves on behalf of someone; proposer and approver are always separate fields. A session grant (`ApproveScope`) requires the same eligibility as the approval it derives from.

Expiry: `OnExpiry` defaults to `RejectOnExpiry` (the pending call becomes a `Failed(Permanent)` result whose reason is `not_executed: expired`; the run continues or ends by policy); a pending approval also ends undecided when the session is taken over (`not_executed: handed_off`, `flow` *Takeover*) or when the call is rejected (`not_executed: rejected by <approver>`, `runtime`) — every such result carries a `Failed(Permanent)` reason beginning `not_executed:` (`runtime`); `EscalateOnExpiry` re-suspends with an escalation target via `Waker`. `ApprovalPolicy.MaxPending` (per subject and per tenant, default 20) makes a further `Ask` fail with `*LimitExceededError{Limit: "pending_approvals"}` so a queue cannot be flooded. Metrics: `gohan.approval.requested/approved/rejected/expired/granted_by_scope`, `gohan.approval.review_time`, `gohan.approval.queue_age`, `gohan.approval.rate`; `gohan.approval.rubber_stamp_suspected` fires when the rolling approval rate exceeds a threshold (default 0.9) with median review time under a floor.


## Requirements

### Requirement: Approval scope and requests

#### Scenario: grant removes the repeat ask
ID: `permission.grant-removes-the-repeat-ask`
- WHEN `create_booking` with fingerprint F is approved via `ApproveScope(2h)` by principal `op` and the model calls it again with fingerprint F in the same session
- THEN the second call executes without suspension and the audit shows `granted_by_scope`

#### Scenario: fingerprint change misses the grant
ID: `permission.fingerprint-change-misses-the-grant`
- WHEN the second call differs in a `FingerprintFields` field
- THEN the gate asks again

#### Scenario: grant does not cross sessions or principals
ID: `permission.grant-does-not-cross-sessions-or-principals`
- WHEN the same fingerprint is called in another session or by another subject
- THEN the gate asks

#### Scenario: grants not inherited on fork
ID: `permission.grants-not-inherited-on-fork`
- WHEN a session grant exists in the parent and the same fingerprint is called in a fork
- THEN the gate asks

#### Scenario: takeover requires scope
ID: `permission.takeover-requires-scope`
- WHEN a principal with `session:write` but not `session:control` calls `TakeOver`
- THEN `ErrSessionForbidden` is returned before any store write

#### Scenario: self-approval refused for high risk
ID: `permission.self-approval-refused-high-risk`
- WHEN a `RiskHigh` call suspends and the originating subject calls `Resume(token, Approve())` with `approve:<tool>`
- THEN `ErrApproverNotEligible` is returned, the token remains usable and audit holds `approval_refused{reason=separate_from_originator}`

#### Scenario: approve scope required for medium risk
ID: `permission.approve-scope-required-medium`
- WHEN a `RiskMedium` call suspends and a principal with only `session:write` in the tenant approves
- THEN `ErrApproverNotEligible`; the owner or a principal with `approve:<tool>` succeeds

#### Scenario: ineligible approver keeps token
ID: `permission.ineligible-keeps-token`
- WHEN an ineligible approval is refused and an eligible principal then approves
- THEN the second `Resume` continues the run and the journal records the eligible approver only

#### Scenario: quorum of two
ID: `permission.quorum-two-approvers`
- WHEN an irreversible `RiskHigh` call needs `Quorum` 2 and two distinct eligible subjects approve
- THEN the run continues after the second approval, the checkpoint records both, and a second approval from the first subject is refused

#### Scenario: escalation targets
ID: `permission.escalation-targets`
- WHEN an approval expires with `EscalateOnExpiry` and `Escalation: ["approve:refunds", "approve:finance-lead"]`
- THEN the run re-suspends with `Eligible.Scopes` set to the next target and `Waker.Schedule` receives it

#### Scenario: approval audit records eligibility
ID: `permission.approval-audit-eligibility`
- WHEN a service approves on behalf of a human via token exchange
- THEN the audit record holds `Approver` (the service), `ApprovedVia{Scope, Policy, ActingFor: <human>}` and the originator separately

#### Scenario: grant inherits policy
ID: `permission.grant-inherits-policy`
- WHEN `ApproveScope(ttl)` is submitted for a `RiskHigh` fingerprint by a principal not eligible under the tier's policy
- THEN `ErrApproverNotEligible` and no grant is stored

#### Scenario: rich request
ID: `permission.rich-request`
- WHEN the gate asks
- THEN `Suspended.Payload` is an `ApprovalRequest` with `ArgOrigins` per argument and `DiffFromLast` against the previous approved call of that tool

#### Scenario: expiry default
ID: `permission.expiry-default`
- WHEN an approval token expires with `RejectOnExpiry`
- THEN the pending call becomes `Failed(Permanent)` naming the expiry and `gohan.approval.expired` increments

#### Scenario: queue flood
ID: `permission.queue-flood`
- WHEN a subject already has the policy's `MaxPending` approvals pending
- THEN a further `Ask` fails the run with `*LimitExceededError{Limit: "pending_approvals"}`
