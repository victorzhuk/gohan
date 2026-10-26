# 0170. Governed call authority and runtime repair baseline

Status: accepted

## Context

The `repair-runtime-safety-contracts` design defines repairs across governed calls, checkpoint recovery, Docker isolation and egress. Its `repair-call-authority` child owns the shared approval record before source or test edits. The designs describe intended repairs, not evidence that the implementation already satisfies them.

The repository owner approved the five decisions below during this repair session. This record preserves that existing approval. It attributes no additional identity, timestamp or signature to the approval.

## Decision

### 1. Optional token-aware recovery

Accept the parent design's optional `core/stores.CheckpointRuns` surface with `SuspensionToken` and `ResumingCheckpoint`. Acquisition must atomically check the suspended state, current token and exclusive session ownership before issuing a lease generation. Frozen `Runs` methods remain unchanged.

External resume without this surface returns `ErrCheckpointIncompatible` before consuming the token. Automatic preempted or consumed-token recovery omits the unsupported path. Ordinary stale-run recovery remains available. Discovery must select the current, unexpired checkpoint for the correct suspension reason. A post-consumption acquisition failure preserves the recorded decision for recovery.

### 2. Explicit checkpoint lifetime

Accept `WithMemoryCheckpointTTL(time.Duration)` and `postgres.WithCheckpointTTL(time.Duration)` as explicit store policies. A positive TTL supplies expiry from the store clock only when `ExpiresAt` is zero. Explicit expiry retains its meaning. Zero TTL means no automatic expiry, and negative policy is refused before writing.

Remove the driver's assignment of execution-lease expiry to checkpoint expiry. Do not add a core lifetime default or change the `Checkpoints` method set or `Checkpoint` fields. Applications that require finite suspension lifetimes must configure them.

### 3. Versioned exact fingerprints and legacy renewal

Accept exact JSON canonicalization without conversion through `float64`, including equivalent decimal spellings and nested values. New approval and journal identities use versioned exact fingerprints. `DiffFromLast` must preserve the numeric distinctions enforced by the gate.

Legacy rounded scoped grants require renewed approval. Ordinary receipts remain readable for history and deduplication, but authorize only their complete recorded call identity. An unresolved legacy journal fingerprint is a lookup candidate, not proof. Reuse its original entry, stored fingerprint and downstream key only after exact comparison of its full stored arguments. Missing evidence fails closed. Completed successful intents retain fresh-call behavior, and journal history is not rewritten.

### 4. Trusted Docker setup with dropped workload privileges

Accept a trusted namespace anchor from the configured trusted base image, separate from restored workspace content. The anchor establishes the complete network policy before any workload starts. It holds `NET_ADMIN` for setup and counter reads. Administration must not use public workload `Exec` and must have a ten-second bound per operation.

The workload joins the prepared namespace, drops all capabilities, enables `no-new-privileges`, overrides the image entrypoint and disables inherited healthchecks. Existing UID behavior and file APIs remain unchanged. Labels associate anchors with workloads for close, failed setup and orphan cleanup without removing anchors serving live workloads. Deny-all retains `NetworkMode: none` without an anchor.

### 5. Checked-address proxy dialing

Accept the already validated target IP as the destination sent through supported HTTP proxies. Preserve the original HTTP Host, TLS server name, proxy credentials, per-response deadline, redirect checks and release behavior. Reject unsupported proxy modes before dispatch.

This decision supersedes the archived acceptance of proxy-side DNS. The approved guarantee is that the checked destination remains the actual peer, even when client and proxy DNS differ.

## Consequences

Stores without token-aware recovery cannot provide the affected resume paths until extended. Finite checkpoint retention requires explicit application wiring. Legacy rounded grants lose authority, and unresolved legacy intents require exact argument evidence.

Fingerprint deployment drains old side-effect workers before transferring ownership. Rollback drains new workers and preserves journal records. Old lookup must not resume uncertain new-format intents.

Docker isolation adds trusted anchor resources and cleanup duties. Proxies that reject IP destinations fail explicitly rather than retain an unproved DNS guarantee.

The approval covers these five design choices only. It does not approve the decomposition or authorize child implementation. It does not establish implementation, scenario synchronization, test acceptance or release readiness. It grants no authority to publish releases, rewrite tags, execute production migrations or upgrade third-party libraries.

## Related records

- [Parent design](../../openspec/changes/repair-runtime-safety-contracts/design.md): decisions 2, 5, 6, 9 and 10 define the approved choices.
- [Child design](../../openspec/changes/repair-call-authority/design.md): baseline ownership and exact-identity transition.
- [Child proposal](../../openspec/changes/repair-call-authority/proposal.md): scope and exclusions.
- [Child tasks](../../openspec/changes/repair-call-authority/tasks.md): task 1.1 records this approval. Task 1.2 retains the remaining baseline synchronization work.
