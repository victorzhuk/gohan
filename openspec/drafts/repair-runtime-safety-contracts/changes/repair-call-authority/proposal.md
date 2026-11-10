# Proposal

## Why

Ordinary approvals and lossy fingerprints can authorize a different call. Journal replay can lose its reserved downstream identity.

## What Changes

- Bind ordinary authority to the complete persisted call identity.
- Revalidate current policy for every counted vote before execution.
- Introduce exact, versioned numeric fingerprints with safe legacy renewal and unresolved-intent comparison.
- Use one effective host effect and one downstream key for initial execution and replay.
- Enforce nested identity exclusions during typed decoding.
- Establish the approved ADR, scenario registry and compatibility baseline for the parent split.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `permission`: call-bound approvals, current eligibility and legacy grant renewal.
- `identity`: recursive exclusions during execution decoding.
- `tools`: effective host effects, exact fingerprints and stable downstream keys.

## Impact

Declared scope includes `core`, `std/journal.go`, `std/tool/exec`, related tests and existing compatibility documentation.
The baseline also synchronizes the eight parent deltas and registers their scenarios without implementing other children's behavior.
The parent manifest declares the full mutable scope and execution order.

## Acceptance

Public native and foreign conversations reject unrelated receipt reuse and changed complete arguments.
Current quorum policy governs execution without consuming insufficient decisions.
Exact numeric comparisons preserve decimal equivalence without merging unequal values.
Completion-loss replay preserves the original reserved downstream key.
Governed host execution and typed callbacks satisfy the assigned scenario IDs in `tasks.md`.

## Boundaries

Parent tasks 1.1-1.2, 2.1-2.6 and 3.1-3.4 belong here.
This child supplies local pre-fix evidence for parent task 1.3.
Parent integration owns the aggregate reproduction acceptance across all six children.
This child does not implement token-aware recovery, provider decoding, Docker, egress or independent release verification.
The existing research-report change retains its ownership and five original edits.
No release publication, tag rewrite, production migration or third-party upgrade is authorized.
