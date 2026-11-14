# Crash recovery

Capability: `recovery` · Spec v1.1 (ADR-0110) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `recovery` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.13a Crash recovery

The harness is stateless; every run is recoverable from `SessionLog` + `Journal` + `Runs`.

1. `Invoke`/`Send` → `Runs.Start` (lease) → per turn: when the turn has non-`ReadOnly` calls, `SessionLog.Append` the assistant message *before* executing them, then `Append` the results; otherwise one `Append` with message and results → `Runs.Finish`.
2. A process that dies mid-turn leaves: a `Running` run with a stale heartbeat, an assistant message with pending calls, journal entries `Reserved` (unknown) or `Completed`.
3. A reaper (user-owned cron/queue) calls `stack.Recover(ctx, limit)`: `Runs.Stale` → `Reclaim` → for each run, `Replay` from the last persisted turn: completed calls are replayed from the journal, reserved ones re-execute with their pinned key, never-started ones execute normally. Recovery honours the original `RunLimits` and principal (via `CredentialSource`).
3a. Before stale runs, when `Runs` implements `PreemptedLister`, `Recover` lists `Preempted(ctx, limit)` without a staleness wait. For a current `Preempted` checkpoint, it uses `ConsumeIf` and then takes a token-fenced `ResumingCheckpoint` lease. Without `CheckpointRuns`, the preempted path is skipped. After the stale pass, `Recover` calls `ResumeReady` when the checkpoint store implements `ResumeReadyLister`. A discovery error is returned with its cause, including `types.ErrCheckpointIncompatible` for unbound memory checkpoint discovery. After successful discovery, consumed candidates without `CheckpointRuns` are skipped. Otherwise recovery fences the lease with the already-consumed token without consuming it again (ADR-0146). The stale pass does not require `CheckpointRuns`, but `Recover` can still return a later discovery error. Call `Recover` once on pod start and then on the reaper cadence.
4. If the flow cannot be re-run headlessly (e.g. a `Conversation` whose client is gone), recovery finishes the run as `Failed{Uncertain}` and emits `gohan.run.abandoned`; the session remains consistent for the next `Send`.
5. `Recover` is idempotent and safe to run on every pod.
6. `stack.Inspect(ctx, runID) (RunView, error)` returns `RunView{Run, Input}`: the stored `Run` — current `Turn`, last `Seq`, pending tool calls and `Cost` so far — plus the `*ResumeInput` a suspended run waits for, from stores only, so any pod can answer it. No limits-remaining value is returned. `RunView` is declared by the driver package, which imports both leaves: `type RunView struct { stores.Run; Input *stores.ResumeInput }`.

Persistence per turn: one `SessionLog.Append` carrying the assistant message and its tool results when the turn has no or only `ReadOnly` calls; two when it has `Idempotent`/`SideEffect` calls (the assistant message with pending calls *before* they execute, the results after), which is what step 1 requires for recovery. `Runs.Heartbeat` is folded into those writes where the store supports it (postgres does). Journal writes only for non-`ReadOnly` calls. `Explain` reports the expected count per turn shape.


## Requirements

### Requirement: Crash recovery

`Recover` MUST reclaim stale runs and replay from the last persisted turn when the preceding recovery operations succeed. It MUST replay completed calls from the journal and re-execute reserved calls with their pinned key. Recovery MUST honour the original `RunLimits` and principal. It MUST propagate a `ResumeReady` discovery error after the stale pass.

#### Scenario: pod dies mid-turn
ID: `recovery.pod-dies-mid-turn`
- WHEN the process is killed after `create_booking` completed and was journaled, but before the run finished
- THEN `Recover` reclaims the run, replays the booking result from the journal without re-executing, continues the loop and finishes the run

#### Scenario: pod dies inside a side effect
ID: `recovery.pod-dies-inside-a-side-effect`
- WHEN the process is killed while `create_booking` is `Reserved`
- THEN recovery re-executes it with the same pinned key and the run finishes with `Uncertain` empty if the API dedupes, else the downstream API receives the same key twice

#### Scenario: no double run
ID: `recovery.no-double-run`
- WHEN a client retries `Invoke` for a session whose run is still leased
- THEN `ErrRunActive` is returned immediately and no model call occurs

#### Scenario: preempted resumed before stale
ID: `recovery.preempted-before-stale`
- WHEN a run was preempted by `Shutdown` on pod 1 two seconds ago and pod 2 calls `Recover`
- THEN the run is resumed with `Continue()` without waiting for `LeaseTTL`, and `gohan.run.recovered` increments

#### Scenario: consumed preempted checkpoint is recoverable
ID: `recovery.consumed-preempted-is-recoverable`
- WHEN a client consumes a preempted checkpoint and its process dies before `Runs.Resuming`
- THEN `Recover` resumes the run with the recorded input, executes the remaining work exactly once and finishes the run

#### Scenario: headless recovery impossible
ID: `recovery.headless-recovery-impossible`
- WHEN a `Conversation` run is stale and no client is attached
- THEN the run is finished as `Failed` with `Uncertain` and `gohan.run.abandoned` increments; the next `Send` on the session succeeds

### Requirement: Automatic preemption recovery consumes its exact token

Automatic preemption recovery MUST conditionally consume the current `Preempted` checkpoint before taking its token-fenced resume lease. It MUST NOT automatically resume a human approval or another suspension reason. An obsolete token MUST NOT replace a later suspension.

#### Scenario: Recovery reaches a second suspension
ID: `recovery.preemption-token-consumed`
- WHEN recovery consumes checkpoint A and execution later suspends on checkpoint B
- THEN A cannot be resumed again and B remains current

#### Scenario: A human approval is pending
ID: `recovery.human-approval-not-preempted`
- WHEN a run is suspended for human approval and automatic recovery runs
- THEN recovery does not decide or execute the approval-controlled call

#### Scenario: A competing suspension is neither acquired nor replayed
ID: `recovery.consumed-token-fenced`
- WHEN token A is consumed and a competing acquisition records a newer suspension B during recovery
- THEN recovery does not replay onto B, the run stays `Suspended`, B's approval remains pending and A stays `ErrTokenConsumed`

### Requirement: Recorded consumption is independent of input contents

Recovery MUST use persisted consumption state and recorded input. A consumed empty `Continue()` MUST remain distinguishable from an unconsumed checkpoint. A crash between conditional consumption and lease acquisition MUST remain recoverable exactly once.

#### Scenario: The client dies after consuming an empty continuation
ID: `recovery.empty-continue-consumed`
- WHEN a client consumes a preempted token with empty continuation input and dies before acquiring its resume lease
- THEN recovery continues that consumed decision exactly once

### Requirement: Recovery records the public event stream durably

Recovered execution MUST persist its ordinary events, terminal failure and final boundary using the same ordering and redaction guarantees as live execution. A conversation on another instance MUST be able to attach to the recovered tail and terminate at its boundary.

#### Scenario: A second instance attaches after recovery finishes
ID: `recovery.eventlog-tail-durable`
- WHEN recovery completes a run and another conversation attaches after the prior cursor
- THEN it receives the recovered tail and exactly one terminal boundary without waiting for cancellation

#### Scenario: One terminal boundary survives a drive spanning several write bounds
ID: `recovery.eventlog-bound-outlives-drive`
- WHEN a recovered drive takes longer than a single event-log write bound
- THEN exactly one terminal boundary is recorded

#### Scenario: Recovered output keeps the tenant redaction maps
ID: `recovery.redaction-tenant-continuity`
- WHEN a recovered run emits content redacted under the tenant's maps
- THEN the recovered output's pseudonym is recorded in the same tenant session map as the live output

### Requirement: Missing optional recovery support has explicit behavior

A governed external resume without token-fenced store support MUST return `ErrCheckpointIncompatible` before consumption. Automatic preempted recovery MUST skip an unsupported token-fenced path. After successful consumed-checkpoint discovery, recovery MUST skip candidates without token-fenced run-store support. `Recover` MUST propagate discovery errors, including unbound memory discovery's `ErrCheckpointIncompatible`. Supported stale-run recovery occurs before consumed-checkpoint discovery and does not require token-fenced support. New sends remain available.

#### Scenario: A custom run store has no token-fenced extension
ID: `recovery.optional-token-surface-absent`
- WHEN a custom store implements the frozen run port without the optional token-fenced surface
- THEN external resume refuses without consuming its token and automatic recovery does not use that unsupported path
