# 0172. Call authority parent split

Status: accepted

## Context

The `repair-runtime-safety-contracts` design repairs governed calls, checkpoint recovery, lease ownership, durable runtime events, provider composition, Docker isolation, egress pinning and release verification. Its implementation order decomposes that parent into six ordered children: `repair-call-authority`, `repair-token-recovery`, `repair-provider-composition`, `repair-sandbox-isolation`, `repair-network-pinning` and `repair-release-verification`. The parent remains the integration umbrella and exclusively owns aggregate integration acceptance across all six children.

`repair-call-authority` has no predecessor. Every later child depends on its integrated baseline and must preserve its registry, ADR, docs and changelog edits. Shared `core`, `std`, store, adapter and testkit directories require integration even when changed lines differ. No child receives ownership of the existing research-report implementation.

The parent audit is grounded at commit `544d11a`. The designs describe intended repairs, not evidence that the implementation already satisfies them. [ADR-0170](0170-governed-call-authority-and-runtime-repair-baseline.md) preserves the repository owner's approval of the five parent design decisions. That approval covers the design choices only; it does not approve the decomposition or authorize child implementation. This record states the decomposition and the integrated baseline this child establishes: call-bound approvals, revalidated quorum, exact versioned fingerprints with legacy renewal, one effective host effect, stable downstream keys and recursive exclusion enforcement.

## Decision

### 1. Complete call authority

Ordinary receipts authorize only their session, run, argument generation, call ID, tool and complete arguments. The existing invocation `RunInfo` and resume approval context are reused, and the private approved-call key is strengthened with complete arguments and generation. Receipt reads, deduplication and `DiffFromLast` remain. Only subject-keyed, bounded `ApproveScope` grants provide reusable authority. The unsafe ordinary-receipt reuse expectation is replaced rather than retained through a compatibility bypass.

Current policy is resolved before each decision and execution. Scope, exclusions, separation and every quorum vote are revalidated. Duplicate-approver refusal stays, and the token is preserved for insufficient or ineligible decisions.

### 2. Exact identity and legacy transition

Validated JSON is canonicalized without `float64` conversion. Equivalent decimal spellings and member order, including nested values, normalize exactly. New approval and journal fingerprints are versioned. Legacy rounded scoped grants require renewed approval. Ordinary receipts remain readable only for their complete original call identity and history. `DiffFromLast` renders numeric differences exactly.

A legacy fingerprint is an unresolved-intent lookup candidate, never proof. The candidate's full stored arguments are compared using exact canonicalization. Its original journal entry, stored fingerprint and reserved downstream key are reused only after an exact match. Missing evidence or unequal values are refused before a new effect can execute. Completed-success behavior that allows a genuinely new call remains. Journal history is not rewritten.

### 3. Effective execution contract

The host runner effect is resolved once in its advertised `ToolSpec`. `WithEffect` wins over the supplied declaration, an existing nonzero declared effect is honored, and an unset effect defaults to `SideEffect`. Permission, journal, shielding, scheduling and timeout handling use that declaration. The reserved key is installed for both initial execution and replay.

Exclusion metadata is retained at tool construction. Schema derivation and typed decoding apply the same nested-field and JSON-name rules. Excluded callback fields remain zero. Transport principal identity remains authoritative. Malformed JSON and duplicate keys keep their rejection.

### 4. Baseline ownership

The user's existing approval of all five parent decisions is recorded before source edits. That record does not approve the split or authorize child implementation. The next verified ADR number is used rather than guessed. All eight parent capability deltas are synchronized and their scenario IDs are registered once, preserving existing scenario owners and the independently edited research-report artifacts.

Existing local permission, identity and governed-tool baseline checks are preserved. Before local repairs, focused behavior failures are captured through those public boundaries, recording the source revision, bounded command, scenario, expected behavior and failing assertion. Compilation errors, missing commands and infrastructure failures are rejected as behavior evidence. Each other child supplies its own pre-fix evidence before repairing its assigned boundaries. Parent integration verifies the complete evidence set and exclusively owns aggregate parent task 1.3 acceptance.

## Consequences

Later children depend on this integrated baseline and preserve its registry, ADR, docs and changelog edits. Shared directories require integration even when changed lines differ. No child owns the research-report implementation.

Fingerprint deployment drains old side-effect workers before transferring ownership to the new format and does not mix old and new intent lookup indefinitely. New code reads only legacy candidates with exact argument evidence. Before rollback, new workers are drained and journal records are preserved; old fingerprint lookup must not resume uncertain new-format intents.

Acceptance is existing-service-strict through public `Build`, native and foreign conversations, and governed tool execution. Every assigned scenario is registered and executed. Focused regressions cover decimal equivalence, nested values, exact legacy matching and completion loss, and scoped-grant, duplicate-vote, token-preservation and timeout outcome regressions are retained. A compilation failure does not count as a failing behavior reproduction, and a flaky retry does not establish acceptance. Focused commands keep the three-minute process deadline and two-minute Go test timeout; full module/store commands keep the twenty-minute process deadline and ten-minute test timeout, with two package workers, two parallel tests and two lint workers. Changed contracts, destructive actions, missing credentials, unresolved published dependencies or exhausted repair limits stop the work. Parent integration retains the broad checks and twenty-four-finding reconciliation.

No release publication, tag rewrite, production migration or third-party upgrade is authorized by this record.

## Related records

- [Parent design](../../openspec/changes/repair-runtime-safety-contracts/design.md): the twelve parent decisions define the full repair set and finding coverage.
- [Implementation order](../../openspec/changes/IMPLEMENTATION_ORDER.md): declares the six-child execution order and the parent's integration role.
- [ADR-0170](0170-governed-call-authority-and-runtime-repair-baseline.md): records the approved parent decisions 2, 5, 6, 9 and 10.
- [Child design](../../openspec/changes/repair-call-authority/design.md): source of the four decisions restated above.
- [Child proposal](../../openspec/changes/repair-call-authority/proposal.md): scope, capabilities and exclusions.
- [Child tasks](../../openspec/changes/repair-call-authority/tasks.md): task 1.2 owns this record and the delta synchronization.
