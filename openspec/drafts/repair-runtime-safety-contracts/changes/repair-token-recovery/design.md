# Design

## Context

This child keeps parent decisions 4-7 together.
A store-only extension cannot establish recovery safety without its consumption, discovery and public caller changes.
The authority child supplies the approved baseline and registry.
These are intended contracts, not claims about current implementation.

## Decisions

### Exclusive acquisition and database time

Memory `Start`, `Resuming` and `Reclaim` use one locked session-owner predicate and consistent `onSess` updates.
Reclaim accepts only expired stored `Running` or `Resuming` state.
A competing live session run refuses acquisition without changing either run.
PostgreSQL locks the session before the target run using the existing serialization order.

Read `clock_timestamp()` after ownership locks inside each relevant transaction.
Use that instant consistently for the transition.
Use database time for production leases, checkpoints, recovery discovery, workspace ownership and notice claims.
Retain exported `WithClock` only as an explicit testing override.
Independent adapter instances without that override must resist process-clock skew.

### Explicit checkpoint lifetime

Remove the driver's `ExpiresAt = lease.Expires` assignment.
Add `WithMemoryCheckpointTTL(time.Duration)` and `postgres.WithCheckpointTTL(time.Duration)` constructor options.
For zero explicit expiry, positive TTL computes expiry from the store clock before encoding.
Explicit expiry keeps its existing meaning.
Zero TTL retains no automatic expiry.
Refuse negative policy before writes.
Do not change `Checkpoint`, `Checkpoints` or the core lifetime default.
Applications needing finite retention must configure the store explicitly.

### Optional token-fenced surface

Declare this optional surface in `core/stores`:

```go
type CheckpointRuns interface {
    SuspensionToken(context.Context, string) (types.ResumeToken, error)
    ResumingCheckpoint(context.Context, string, types.ResumeToken, time.Duration) (Lease, error)
}
```

Keep frozen `Runs` methods unchanged.
`SuspensionToken` reports only the current suspended token.
`ResumingCheckpoint` atomically checks suspended state, expected token and exclusive session ownership before minting a lease generation.
Token mismatch leaves the newer suspension unchanged.
Both concrete stores implement the surface.
Use released parameter/result types for adapter structural compatibility.
Do not assert against an unpublished root interface in the adapter's independent build.

External resume requires the optional surface before consumption.
Without it, return `ErrCheckpointIncompatible` with the token untouched.
Automatic preempted and consumed-token recovery skip unsupported optional paths.
Ordinary stale-run recovery and new sends remain available.
A pre-consumption refusal preserves the token.
A lease failure after conditional consumption preserves the recorded decision for recovery.

### Consumption and eligible discovery

Read the current token and inspect the checkpoint's actual `Preempted` reason.
Conditionally consume `Continue()` before acquiring the lease for that exact token.
Never infer consumption from input values.
An empty consumed continuation remains distinguishable from an unused token.
A crash after consumption remains recoverable exactly once.
Human approval never becomes automatic preemption.

Filter current token, consumed state, expiry, suspended state and reason before applying discovery limits.
Use existing PostgreSQL reason columns and memory reasons from `SuspendWithReason`.
Legacy reasonless records require current checkpoint evidence.
No new SQL column is planned.

Bind memory discovery through `WithMemoryCheckpointRuns(CheckpointRuns)`.
Take a checkpoint snapshot before consulting Runs.
Recheck the token at acquisition without a checkpoint/run mutex cycle.
An unbound memory store explicitly refuses recovery discovery.
PostgreSQL filters checkpoint and run rows in its query.
Update all shipped wiring and conformance factories exercising optional discovery.

### Durable events and accounting

Share existing redacted record-before-delivery semantics across live, resumed and recovered execution.
Persist ordinary events, terminal failures and the final boundary using the next stored sequence.
A second conversation's `Attach` must observe the recovered tail and exactly one final boundary.
Use a bounded completion context.
Check `finishTurn` errors before restoring a carried `Done`.
A failed terminal write cannot yield success.
Preserve steer, limit, handoff, cost and terminal-reason behavior.

Persist cumulative admitted tool counts.
Restore them once on Resume or recovery.
Do not charge an already admitted pending call again.
Reject new batches exceeding the run's cumulative limit.

## Scope and Ordering

Integrate `repair-call-authority` before this child.
Preserve its approval context, journal identity, registry and changelog changes in shared directories.
The provider child integrates this outcome before validating governed foreign calls.
The release child validates the resulting PostgreSQL adapter against published root types.
Parent integration owns cross-child approval/recovery event ordering and race evidence.

## Migration and Compatibility

Retain explicit expiry records and frozen method sets.
Custom stores without optional support fail closed or skip only the unsupported automatic path.
Do not infer compatibility from a local workspace replacement.
Do not add a DDL migration or execute a production migration.
The independently edited research-report change remains outside this child's implementation ownership.

## Acceptance and Limits

Use public Send, Resume, recovery, Attach and concrete store boundaries.
Use a real isolated PostgreSQL container and deterministic harness clocks.
Cover competing acquisition, stale generations, old tokens, empty input, discovery limit one and terminal-write failures.
Run optional-surface present and absent cases and full store conformance.

Focused commands have a three-minute process deadline and a two-minute Go test timeout.
Full module/store commands have a twenty-minute process deadline and a ten-minute test timeout.
Use two package workers, two parallel tests and two lint workers.
Preserve configured cache and temporary build paths.
Allow one fixture repair and at most two further diagnostic reruns per boundary.
Do not count a flaky retry as acceptance.
Stop for changed contracts, destructive operations, missing credentials, unresolved published dependencies or exhausted repair limits.
Parent integration retains broad checks and finding reconciliation.
