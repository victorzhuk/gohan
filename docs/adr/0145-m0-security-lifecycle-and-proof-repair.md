# 0145 — M0 security, lifecycle and proof repair

Date: 2026-10-05
Status: accepted

Supersedes the tracing-seam clause of ADR-0066, narrows the conformance-import clause of ADR-0139 to core leaf packages, and amends the telemetry, permission, stores, performance and identity capability specs. ADR-0066 and ADR-0139 stay in the record as history.

## Context

An audit of the M0/M0.5 tree found consumer-visible defects against the tree's own specs:

- `Conversation.Resume` consumed the single-use token before any session-ownership or approval-eligibility check, then replaced the transport principal with the checkpoint originator, so a caller holding a token resumed and wrote history as the owner (`identity` rule 6, `permission` *Who may approve*).
- `Resume` bypassed the lifecycle: no `Runs.Resuming`, no lease, no `EventLog` persistence, no terminal store transition, and `DriveResume` ignored its `AgentRun`, so a second suspension ended silently.
- The lease carried no ownership identity: after `Reclaim` the previous driver could still `Heartbeat`, `Finish`, `Suspend` or `Drain` the new driver's run.
- No production driver started the lease heartbeat helper, so a step or a blocked consumer longer than `LeaseTTL` (30 s) could lose the lease while still executing.
- Event persistence failure inside the stream was reported and then the event was delivered anyway, so a terminal error tuple was followed by another tuple.
- The batch protocol executed calls before all gates had decided and dropped calls after the first `Ask`.
- Metric registrations were validated at `Build` but never enforced at emission, so forbidden identity labels could still reach the exporter.
- The performance gate compared whole-benchmark latency relatively, never evaluated the frozen absolute budgets, passed when a required benchmark was absent, and cached a verdict by commit pair alone, so an advisory PASS satisfied a strict invocation.

Every statement above is a code-versus-spec deviation except where this ADR amends the spec.

## Decision

### 1. Lease ownership is fenced by a generation

`stores.Lease` gains `Generation uint64`. Zero is invalid. `Start`, `Resuming` and `Reclaim` allocate a new nonzero generation for the run; `Heartbeat` changes only `Expires`. A mutating lease call (`Heartbeat`, `Finish`, `Suspend`, `Drain`) requires the run id *and* the current generation, an active record and an unexpired lease; a stale generation fails with `ErrRunNotActive` and mutates nothing. `Expires` stays informational and is never an ownership token. Implementations allocate durable generations that never repeat for the same run; an exhausted counter refuses rather than wrapping.

### 2. Checkpoints carry their run, and consumption is conditional

`stores.Checkpoint` gains `RunID string`. The base `Checkpoints` port keeps `Put`/`Consume`/`PendingInput`. Two optional interfaces are added, discovered by type assertion:

- `CheckpointResumer`: `Peek(ctx, token)`, `ConsumeIf(ctx, token, expected, input)`, `UpdatePending(ctx, token, expected, next)`.
- `ResumeReadyLister`: `ResumeReady(ctx, limit)`.

`Peek` returns an owned snapshot and changes no token state. `ConsumeIf` and `UpdatePending` compare the caller's snapshot against the stored record atomically across every field including `Data` bytes, originator scopes and expiry; a changed snapshot returns `ErrVersionConflict`; `ConsumeIf` records the input and consumes the token in one transaction. `UpdatePending` may change only `Data` — never token identity, originator, reason, flow, backend, session or expiry. Every read path (`Put` return value, `Peek`, `Consume`, `ConsumeIf`, `PendingInput`, `ResumeReady`) returns copies, so no caller can mutate store-owned bytes.

`ResumeReady` returns consumed checkpoints whose runs are still `Suspended`, closing the crash interval between consumption and `Runs.Resuming`: recovery attempts the `Resuming` transition for each candidate, one driver wins, and a `Finished` run is never re-executed.

An external resume requires `CheckpointResumer`; a conversation configured for approvals also requires `ResumeReadyLister`. A store without them is refused with `ErrCheckpointIncompatible` before consumption rather than falling back to the unconditional `Consume`.

### 3. Checkpoint data is a versioned envelope

`Checkpoint.Data` carries a versioned envelope, decoded by one shared decoder used by resume, recovery and preempted recovery:

```go
const checkpointEnvelopeVersion = 1

type checkpointEnvelope struct {
    Version    int                  `json:"version"`
    Run        types.RunInfo        `json:"run"`
    Generation uint64               `json:"generation"`
    State      runtime.State        `json:"state"`
    Approvals  []checkpointApproval `json:"approvals,omitempty"`
    Input      *types.InputRequest  `json:"input,omitempty"`
}

type checkpointApproval struct {
    Call        types.ToolUse          `json:"call"`
    Risk        types.RiskTier         `json:"risk"`
    Fingerprint stores.Fingerprint     `json:"fingerprint"`
    Reversible  bool                   `json:"reversible"`
    Eligible    permission.Eligibility `json:"eligible"`
    ApprovedBy  []types.Principal      `json:"approved_by,omitempty"`
}
```

`permission.ApprovalRequest` is never marshalled directly: its `Tool` field contains `ToolSpec.Verify`, a function type. The public request is reconstructed from this snapshot plus the registered tool declaration, and display fields go through `NewApprovalRequest`. Envelope keys are exactly those listed; nested shipped types keep their existing JSON encoding.

The decoder rejects empty, `null`, non-object, malformed, duplicate-key and trailing-value data; a payload carrying any envelope key must be a complete valid version 1 envelope; otherwise the legacy raw `runtime.State` encoding is accepted only when it carries at least one known state field. Legacy data with `Reason == HumanApproval` is refused with `ErrCheckpointIncompatible` — a raw state cannot prove the request, policy or argument binding. A valid checkpoint addressed to another flow or backend returns `ErrTokenMismatch`. All of this happens before consumption or any effect.

`Checkpoint.RunID`, `Flow`, `Originator`, `Reason`, `ExpiresAt` and the envelope are written before `Runs.Suspend`.

### 4. Approval policy is injected, never chosen in core

`core/permission` declares the consumer-owned port:

```go
type ApprovalPolicySource interface {
    ApprovalPolicy(ctx context.Context, risk types.RiskTier, tool string, reversible bool) (ApprovalPolicy, error)
}
```

`Conversation` gains `WithConversationApprovalPolicy(source permission.ApprovalPolicySource)`; `std/permission` supplies the default implementation over `TierPolicy`; core never imports `std`. Missing wiring refuses approvals and edits with `ErrApproverNotEligible`. An error from the source never grants permission. The policy is resolved at request creation and again before an approval or an edit is accepted, so a tightened policy applies to already collected approvals.

Owner exceptions use the persisted `History.Owner`, never `Checkpoint.Originator`. `RiskHigh` requires the approval scope even for the session owner (this corrects `std/permission`'s blanket owner exception). Rejection needs session write access but no approval scope. `EditArgs` is a revision proposal: it requires edit eligibility, increments the argument generation, recomputes fingerprints and display data, and clears every collected approval — the edited request stays pending and needs its own approvals, including from the editor.

Execution authorization is bound to session, `RunID`, argument generation, call id, tool name and the complete argument value, rechecked immediately before the tool executes. A fingerprint alone is insufficient because `FingerprintFields` may exclude changed arguments. An approval for old arguments can never authorize edited arguments. Argument generation overflow returns `ErrCheckpointIncompatible` without mutating the checkpoint; it is a separate counter from `Lease.Generation`.

Previous approved arguments come from a trusted receipt in `Message.Meta` (key `gohan.approval`), written by the harness when approval reaches quorum and before the tool executes:

```go
type approvalReceipt struct {
    Version    int               `json:"version"`
    RunID      string            `json:"run_id"`
    Generation uint64            `json:"generation"`
    CallID     string            `json:"call_id"`
    Tool       string            `json:"tool"`
    Args       json.RawMessage   `json:"args"`
    Approvers  []types.Principal `json:"approvers"`
}
```

Receipts are keyed by `(run_id, generation, call_id)`; recovery never appends a second one. Incoming messages carrying `gohan.approval` are rejected before append, so caller metadata can never be promoted to a trusted receipt. Receipt-only messages are excluded from assembled provider requests but preserved in persisted history. `Journal.ByFingerprint` is not used for this: entries carry no arguments and a changed argument set has a different fingerprint.

### 5. Resume authorizes before it consumes

The effect-free preparation order is normative:

1. Require the verified principal; refuse a context carrying `RunInfo` with `ErrResumeInsideRun`.
2. `Peek` the token.
3. Decode and validate the checkpoint, its binding, reason and input.
4. Load history and authorize the transport principal against the owner.
5. Resolve policy and check every approval binding.
6. Resolve originator credentials; a credential error propagates.
7. Validate the saved runtime state and build the `AgentRun` wiring without calling `Runtime.Start` and without component effects.
8. A partial approval or an edit goes through `UpdatePending` and returns without effects.
9. A complete decision goes through `ConsumeIf`.
10. Only after success: receipts, decision results, `Runs.Resuming`, restored runtime execution.

`Runtime.Start` is called only after consumption and the `Resuming` transition; an initialization failure follows the lifecycle failure and recovery path. Preparation failures leave the token pending and produce no persistent change and no component effect. Separate `Checkpoints` and `SessionLog` calls are not a cross-store transaction and are not described as one.

Every resume reason requires session write access — the owner or `session:write`, same tenant — checked before consumption; token possession alone is not authority. `AwaitingInput`/`AwaitingTool`/`AwaitingExternal`/`AwaitingBatch` validate the delivery against the saved request; `Scheduled`, `AwaitingControl` and `Preempted` accept only the empty delivery their reason defines and recheck control freshness or the permission gate before effects; `HumanHandoff` is refused with `ErrNotSuspendable` and mints no token; an unknown reason is `ErrCheckpointIncompatible`. Inappropriate `Args`/`Data`/`Reason`/verdict combinations are `ErrInputInvalid`. A caller-supplied `Approver` never supplies authority — the harness installs the verified transport principal after validation. Trusted internal recovery may use the persisted run identity, and it may not manufacture an approval or bypass a missing approval proof.

### 6. One lifecycle drives initial, resumed and recovered execution

`DriveLifecycle` gains `WithLifecycleResumeState(state runtime.State)`, assigns copies of resumable mutable data, and calls `Runtime.Start` once for the per-run wiring while selecting the saved state instead of the returned initial position. `conversation.Resume`, `Stack.recoverRun`, `Stack.recoverPreemptedRun` and `ResumePreempted` all run through it; `DriveResume` stays a public entry point implemented on the same initialization and stepping code, and its separate `driveSteps` semantics are removed. No compatibility branch keeps different terminal behaviour.

The lifecycle owns exactly one heartbeat, one current lease, suspension persistence, terminal store transitions and failure cleanup; the conversation supplies the event-persistence boundary and recovery persists events through it too. The heartbeat starts immediately after lease acquisition, stays active while the consumer blocks and while a shielded side effect finishes, reports its failure to the driver (which cancels cancellable work and starts no new effect after ownership loss), supplies the lease used by `Drain`/`Suspend`/`Finish`, and is stopped and joined before the iterator returns. Iterator exhaustion is never proof of successful completion.

Event persistence happens before delivery and its failure ends the stream: one final tuple carrying the nil event and the error, and no further event or `Done`. For agent-controlled `Send`, operation reattachment and the authoritative `Start` happen before history loading and the input append; control and authorization metadata are read before `Start`; the `ControlHuman` append-only exception is preserved. `Continue` uses the same acquired-lease preparation and closes the run as `Failed` when preparation fails. A duplicate operation naming an existing operation wins over an active-session refusal.

### 7. Telemetry is a dependency-free port with std policy

ADR-0066's clause that the OTel API is the seam is superseded. Core declares a dependency-free `Telemetry` port; `std/telemetry` owns conventions, the metric label policy and a `Decorate(sink, convention)` wrapper that applies `Convention.ApplyAttrs` to initial and final span attributes, `Count` and `Record`; `adapter/otel` owns vendor translation. The root module stays dependency-free.

The metric allow-list is enforced at emission as well as registration: an unregistered metric is not emitted, a forbidden identity attribute is omitted even under a canonical `gohan.*` spelling, an unregistered label is omitted, `tenant` passes only with `WithTenantLabel()`, duplicate normalized labels keep the last admissible caller value, and package release/variant stamps override caller spellings. Emission methods stay void and never panic. Canonical attribute keys are normalized to registration vocabulary before matching.

Core emits the child spans its spec requires at the governed seams it owns: model invocation, tool invocation, guard consultation and the permission decider.

### 8. Performance proof is complete and its cache is honest

The gate loads the frozen budgets, requires every configured gated benchmark and its raw comparator on both sides with three non-empty rounds, computes tool overhead as fastest chain minus fastest raw call and model overhead against its matched raw call, and enforces those absolute deltas plus the 5 % relative regression on the reference runner. A missing, empty or malformed measurement fails; the gate never selects a weaker comparison. Allocations stay locally authoritative; local latency stays advisory.

The verdict cache key becomes a SHA-256 digest of a versioned identity object covering the cache schema version, both full commit SHAs, the benchmark selector, benchtime, timeout and rounds, tolerance and latency-enforcement mode, a digest of the validated baseline configuration, the Go toolchain and runner identity. Configuration is validated before the cache lookup and the identity is stored and revalidated inside the cached verdict, so an advisory PASS can never satisfy a strict invocation and a schema-1 verdict is ignored.

### 9. Anonymous mode is a real, bounded option

`AllowAnonymous()` becomes a build option carried through `Stack` and `Conversation`; the entry points that require a principal route through the shared seam check. Anonymous mode permits unowned function-flow invocation only: it invents no tenant, grants no access to an owned session and bypasses no store authorization.

### 10. Acceptance tooling sees the whole workspace

`spec:coverage` merges the `-json` streams of the root, `adapter/otel` and `examples` modules in one pipeline with `set -e`/`pipefail`, so a failing producer fails the task and the examples-module `engines.*` subtests are visible. A `spec:gate` task runs that pipeline with `--gate <milestone>` (default M0.5) as the milestone exit check, and `spec` runs the types regeneration first and the gate second in order.

### 11. The leak check compares identities

`gohantest.LeakCheck` compares goroutine identities from complete, bounded `runtime.Stack(_, true)` snapshots instead of a net `runtime.NumGoroutine()` count, keeps the existing scheduler-yield bound, and fails when the snapshot cannot be completed within its ceiling. A goroutine that ends while another remains must not cancel the finding.

### 12. Testkit boundary

ADR-0139's rule that conformance imports leaf packages rather than the driver is narrowed to core leaf packages: outer test fixtures in `testkit` may import the driver to construct and exercise `Conversation`. No core leaf package may import the driver.

## Consequences

- Stored shapes change (`Lease.Generation`, `Checkpoint.RunID`, checkpoint envelope, `Message.Meta` receipts). These are v0 breaking changes, recorded here and carried by `CHANGELOG`.
- Every `Runs` implementation must allocate generations; every `Checkpoints` implementation that serves a conversation with approvals must implement `CheckpointResumer` and `ResumeReadyLister`. `storetest` covers stale-generation refusals on all four mutating calls and the atomic conditional operations.
- The hardened conversation refuses an approval-capable configuration whose store lacks those interfaces, instead of running with weaker recovery.
- The performance workflow's cache key changes; existing verdict cache files are ignored.
- `docs/design/types.md` is regenerated after the spec amendments land.
