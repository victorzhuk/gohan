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
3a. Before stale runs, when `Runs` implements `PreemptedLister`, `Recover` lists `Preempted(ctx, limit)` and resumes each with `Continue()` (`Consume` → `Resuming`, no staleness wait); a token already consumed by the client is skipped silently. The deployment note is to call `Recover` once on pod start and then on the reaper cadence.
4. If the flow cannot be re-run headlessly (e.g. a `Conversation` whose client is gone), recovery finishes the run as `Failed{Uncertain}` and emits `gohan.run.abandoned`; the session remains consistent for the next `Send`.
5. `Recover` is idempotent and safe to run on every pod.
6. `stack.Inspect(ctx, runID) (RunView, error)` returns `RunView{Run, Input}`: the stored `Run` — current `Turn`, last `Seq`, pending tool calls and `Cost` so far — plus the `*ResumeInput` a suspended run waits for, from stores only, so any pod can answer it. No limits-remaining value is returned. `RunView` is declared by the driver package, which imports both leaves: `type RunView struct { stores.Run; Input *suspension.ResumeInput }`.

Persistence per turn: one `SessionLog.Append` carrying the assistant message and its tool results when the turn has no or only `ReadOnly` calls; two when it has `Idempotent`/`SideEffect` calls (the assistant message with pending calls *before* they execute, the results after), which is what step 1 requires for recovery. `Runs.Heartbeat` is folded into those writes where the store supports it (postgres does). Journal writes only for non-`ReadOnly` calls. `Explain` reports the expected count per turn shape.


## Requirements

### Requirement: Crash recovery

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

#### Scenario: headless recovery impossible
ID: `recovery.headless-recovery-impossible`
- WHEN a `Conversation` run is stale and no client is attached
- THEN the run is finished as `Failed` with `Uncertain` and `gohan.run.abandoned` increments; the next `Send` on the session succeeds
