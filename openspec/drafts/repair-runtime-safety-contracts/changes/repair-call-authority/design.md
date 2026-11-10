# Design

## Context

This child refines parent decisions 1-3 and baseline tasks 1.1-1.2.
It supplies local reproduction evidence for integration-owned parent task 1.3.
The parent audit cites commit `544d11a`.
These documents specify intended repairs, not an observation that source already implements them.

## Decisions

### Complete call authority

Ordinary receipts authorize only their session, run, argument generation, call ID, tool and complete arguments.
Use existing invocation `RunInfo` and resume approval context.
Strengthen the private approved-call key with complete arguments and generation.
Keep receipt reads, deduplication and `DiffFromLast`.
Only subject-keyed, bounded `ApproveScope` grants provide reusable authority.
Replace the unsafe ordinary-receipt reuse expectation rather than adding a compatibility bypass.

Resolve current policy before each decision and execution.
Revalidate scope, exclusions, separation and every quorum vote.
Keep duplicate-approver refusal and preserve the token for insufficient or ineligible decisions.

### Exact identity and legacy transition

Canonicalize validated JSON without `float64` conversion.
Normalize equivalent decimal spellings and member order, including nested values.
Version new approval and journal fingerprints.
Renew legacy rounded scoped grants.
Retain ordinary receipts only for their complete original call identity and history.
Render numeric differences exactly in `DiffFromLast`.

Treat a legacy fingerprint as an unresolved-intent lookup candidate, never as proof.
Compare the candidate's full stored arguments using exact canonicalization.
Reuse its original journal entry, stored fingerprint and reserved downstream key only after an exact match.
Refuse missing evidence or unequal values before a new effect can execute.
Keep completed-success behavior that allows a genuinely new call.
Do not rewrite journal history.

### Effective execution contract

Resolve the host runner effect once in its advertised `ToolSpec`.
`WithEffect` wins over the supplied declaration.
Honor an existing nonzero declared effect.
Default an unset effect to `SideEffect`.
Permission, journal, shielding, scheduling and timeout handling use that declaration.
Install the reserved key for both initial execution and replay.

Retain exclusion metadata at tool construction.
Apply the same nested-field and JSON-name rules during schema derivation and typed decoding.
Excluded callback fields remain zero.
Transport principal identity remains authoritative.
Preserve malformed JSON and duplicate-key rejection.

### Baseline ownership

Record the user's existing approval of all five parent decisions before source edits.
This record does not approve the split or authorize child implementation.
Add the next verified ADR number without guessing it in this draft.
Synchronize all eight parent capability deltas and register their scenario IDs once.
Preserve existing scenario owners and the independently edited research-report artifacts.
Preserve existing local permission, identity and governed-tool baseline checks.
Before local repairs, capture focused behavior failures through those public boundaries.
Record the source revision, bounded command, scenario, expected behavior and failing assertion for each reproduction.
Reject compilation errors, missing commands and infrastructure failures as behavior evidence.
Each other child supplies its own pre-fix evidence before repairing its assigned boundaries.
Parent integration verifies the complete evidence set and exclusively owns aggregate parent task 1.3 acceptance.

## Scope and Ordering

Use the manifest's declared files, directories and mutable contracts.
This child has no predecessor.
All later children depend on this integrated baseline and preserve its registry, ADR, docs and changelog edits.
Shared `core`, `std`, store, adapter and testkit directories require integration even when changed lines differ.
No child receives ownership of the existing research-report implementation.

## Migration and Rollback

Drain old side-effect workers before transferring ownership to the new fingerprint format.
Do not mix old and new intent lookup indefinitely.
New code reads only legacy candidates with exact argument evidence.
Before rollback, drain new workers and preserve journal records.
Old fingerprint lookup must not resume uncertain new-format intents.

## Acceptance and Limits

Use existing-service-strict acceptance through public `Build`, native and foreign conversations, and governed tool execution.
Register and execute every assigned scenario from the deltas.
Focused regressions cover decimal equivalence, nested values, exact legacy matching and completion loss.
Retain scoped-grant, duplicate-vote, token-preservation and timeout outcome regressions.
A compilation failure does not count as a failing behavior reproduction.

Focused commands have a three-minute process deadline and a two-minute Go test timeout.
Full module/store commands have a twenty-minute process deadline and a ten-minute test timeout.
Use two package workers, two parallel tests and two lint workers.
Preserve configured cache and temporary build paths.
Allow one fixture repair and at most two further diagnostic reruns per failing boundary.
A flaky retry does not establish acceptance.
Stop for changed contracts, destructive actions, missing credentials, unresolved published dependencies or exhausted repair limits.
Parent integration retains the broad checks and twenty-four-finding reconciliation.
