# Spec Delta

## Context

This delta MODIFIES one requirement of the `sandbox` capability, `Lifecycle and resume` (`openspec/specs/sandbox/spec.md:122-149`). The requirement's `PerCall` prose and all five of its scenario blocks are reproduced byte-identical; the four scenario ids this change owns (`sandbox.per-session-persists`, `sandbox.snapshot-on-suspend`, `sandbox.resume-restores`, `sandbox.replay-no-reexec`) and the `PerCall` id `sandbox.per-call-closes` keep their exact `ID:` and WHEN/THEN lines. Three clarifications land in the requirement prose only, so no existing scenario changes: the end of a detached run publishes the durable session ref rather than a checkpoint (a detached end creates no checkpoint), a restore opens from `Checkpoint.Workspace` only when its generation is not older than the stored generation, and the live handle is closed after the durable terminal write rather than before it.

## MODIFIED Requirements

### Requirement: Lifecycle and resume

The `PerCall` lifecycle MUST run `Open`, `Exec` and `Close` inside one tool call, and `Close` MUST be called exactly once per call, including when the call panics.

The `PerSession` lifecycle MUST keep one workspace per session across turns through one live handle owned by the run: a later call in the same run MUST reuse that handle, and a call in a later run MUST open from the session's durable `WorkspaceRef` so files written in an earlier turn are visible without re-upload. The live handle MUST be bound to the owning run id and the store-minted lease generation, and a stale owner MUST be refused. On suspension the harness MUST snapshot the live handle through the fallible lifecycle path and MUST store the produced `WorkspaceRef` and its generation in `Checkpoint.Workspace` before the suspension is persisted. At the end of a detached run the harness MUST snapshot the live handle and MUST persist the produced ref as the session's durable workspace ref; a detached run creates no checkpoint. On resume the harness MUST open the workspace from `Checkpoint.Workspace` only when the checkpoint generation is not older than the session's stored generation, and a missing or expired snapshot MUST return an error instead of opening fresh. At the end of a non-detached run the harness MUST publish the durable session ref before the run's terminal write, and MUST close the live handle only after that terminal write succeeds.

#### Scenario: per-session persistence
ID: `sandbox.per-session-persists`
- WHEN turn 1 writes `data.csv` and turn 2 runs code reading it
- THEN the read succeeds without re-uploading

#### Scenario: snapshot on suspension
ID: `sandbox.snapshot-on-suspend`
- WHEN a run suspends for `HumanApproval` after writing files
- THEN `Checkpoint.Workspace` holds a `WorkspaceRef` produced by `Snapshot`

#### Scenario: resume on another pod
ID: `sandbox.resume-restores`
- WHEN the run is resumed on another pod
- THEN the workspace opened from `Checkpoint.Workspace` contains the files written before suspension

#### Scenario: replay does not re-execute
ID: `sandbox.replay-no-reexec`
- WHEN `Replay` resumes a run that executed code before suspending
- THEN the journaled `ExecResult` is used and the adapter's `Exec` is not called again

#### Scenario: per-call closes
ID: `sandbox.per-call-closes`
- WHEN a `PerCall` tool call finishes or panics
- THEN `Close` has been called exactly once

## ADDED Requirements

### Requirement: Per-session workspace handle ownership

One live workspace handle MUST exist per `(session, run owner)`, and the coordinator MUST own the exact `Workspace` that `Acquire` received from `Open`. Ownership MUST be bound to the run id and the store-minted lease generation; `Publish` and `Release` from a stale owner MUST be rejected. `Release` MUST close that exact handle, MUST NOT open a workspace in order to close it, and MUST NOT clear the durable snapshot reference. `Acquire`, `Publish` and `Release` MUST propagate their errors.

#### Scenario: handle reused by one owner
ID: `sandbox.handle-owner-exclusive` (proposed)
- WHEN two runs in one session start on two pods
- THEN only the run holding the session lease acquires the live handle and no second workspace opens

#### Scenario: stale lease generation is fenced
ID: `sandbox.handle-owner-fenced` (proposed)
- WHEN a run whose lease generation is no longer the current one calls `Publish` or `Release`
- THEN both calls are rejected and the durable snapshot reference stays unchanged

#### Scenario: release closes the acquired handle
ID: `sandbox.release-closes-acquired-handle` (proposed)
- WHEN `Release` runs for a session that acquired a live handle
- THEN the handle returned by that `Acquire` is closed exactly once and no fresh workspace is opened

### Requirement: Bounded cleanup sweep

`Sweep` MUST accept a positive limit and MUST process at most that many records. The stack recovery path MUST invoke `Sweep` even when the stale-run query returns no rows. Claiming a record for cleanup and acquiring a live handle for use MUST exclude each other. A failed close MUST retain a cleanup record and the durable snapshot so a later sweep retries.

#### Scenario: cleanup claim excludes live use
ID: `sandbox.cleanup-claim-excludes-live-use` (proposed)
- WHEN a sweep claims a session record while a live run still uses its handle
- THEN the claim is refused and the handle is not closed

#### Scenario: sweep honours its limit
ID: `sandbox.sweep-bounded-limit` (proposed)
- WHEN `Sweep` is called with limit three and five cleanup records exist
- THEN at most three records are processed

#### Scenario: recovery sweeps without stale runs
ID: `sandbox.recover-sweeps-without-stale-runs` (proposed)
- WHEN recovery finds no stale running rows but cleanup records exist
- THEN the sweep still runs and processes them

#### Scenario: failed close retains the snapshot
ID: `sandbox.close-failure-retains-snapshot` (proposed)
- WHEN closing a tracked handle fails
- THEN a cleanup record remains and the durable snapshot reference is unchanged

### Requirement: Per-session build admission

A build that declares `PerSession` MUST fail `Build` when no workspace coordinator or no durable workspace-metadata surface is wired, and MUST return no tool set.

#### Scenario: build refuses an incapable per-session build
ID: `sandbox.build-refuses-incapable-persession` (proposed)
- WHEN `Build` is given a `PerSession` sandbox policy without a coordinator or durable metadata surface
- THEN `Build` fails and no tool set is registered

### Requirement: Durable workspace metadata with generation compare-and-set

Durable per-session workspace metadata MUST hold a snapshot ref, a generation and an owner, and the generation MUST be the sole compare-and-set token. A write MUST atomically compare the expected generation and validate the current lease owner. A publication MUST write the expected generation plus one. A compare-and-set loser MUST return an error and MUST NOT report another owner's snapshot as its own.

#### Scenario: generation fenced compare-and-set
ID: `sandbox.ref-generation-fenced` (proposed)
- WHEN two publications race for one session
- THEN exactly one compare-and-set succeeds and the newer generation is kept

### Requirement: Owner proof in the atomic workspace write

The owner proof in a workspace metadata write MUST match the session's current run id, the run's own session, a nonzero matching lease generation and a live unexpired lease. A store with no wired owner guard MUST refuse every write. A stale owner proof MUST return `types.ErrRunNotActive`, and a stale generation token MUST return `types.ErrVersionConflict`. A refusal MUST leave the stored metadata unchanged.

#### Scenario: owner validated in the same atomic write
ID: `sandbox.owner-cas-atomic` (proposed)
- WHEN a write presents a stale lease generation
- THEN the write fails and the stored metadata is unchanged

#### Scenario: absent owner guard refuses writes
ID: `sandbox.owner-guard-required` (proposed)
- WHEN a workspace metadata write runs on a store with no wired owner guard
- THEN the write is refused and the stored metadata is unchanged

### Requirement: Restore rules for session workspace metadata

A restore MUST NOT rewrite metadata that is already equal. A checkpoint older than the stored snapshot MUST leave the stored snapshot in place. A checkpoint newer than the stored snapshot MUST have a matching durable publication, and the restore MUST fail without it. Generation zero with an empty ref MUST open a fresh workspace. Generation zero with a nonempty legacy ref MUST be imported explicitly into absent metadata.

#### Scenario: restore keeps a later generation
ID: `sandbox.restore-keeps-newer-generation` (proposed)
- WHEN a checkpoint's workspace generation is older than the session's stored generation
- THEN the resume keeps the stored ref and does not restore the older snapshot

#### Scenario: equal metadata is not rewritten
ID: `sandbox.restore-skips-equal-metadata` (proposed)
- WHEN a restore presents metadata equal to the stored metadata
- THEN no write is issued

#### Scenario: session without a ref migrates
ID: `sandbox.no-ref-session-migrates` (proposed)
- WHEN an existing session with no stored workspace metadata runs a `PerSession` call
- THEN the workspace opens fresh and its first generation is published at terminal end

#### Scenario: legacy ref requires explicit import
ID: `sandbox.legacy-ref-explicit-import` (proposed)
- WHEN stored metadata is absent and a legacy nonempty ref is presented
- THEN the import writes first generation metadata only after an explicit import call

### Requirement: Explicit legacy workspace import

An explicit import operation MUST write a legacy workspace snapshot into absent durable metadata. It MUST require session-write authority and a current owner lease, and it MUST validate the owner in the store rather than trust a caller-supplied value. It MUST reject a nonzero input generation, an empty ref and existing metadata, and every rejection MUST leave the stored metadata unchanged. `Resume` and `Recover` MUST NOT invoke it implicitly.

#### Scenario: import writes generation one
ID: `sandbox.import-writes-generation-one` (proposed)
- WHEN an authorized caller with a live lease imports a generation-zero nonempty ref into absent metadata
- THEN the store holds generation one with that ref and the validated owner

#### Scenario: import refuses without mutation
ID: `sandbox.import-refuses` (proposed)
- WHEN an import presents an unauthorized caller, a stale owner, a nonzero input generation, an empty ref or existing metadata
- THEN the import returns an error and the stored metadata is unchanged

### Requirement: Workspace mutation durability

A `PerSession` call that may mutate the workspace MUST publish the workspace durably after every body invocation that may mutate, including a body that reports a partial-write error and a body whose execution exit is nonzero. A read-only call MUST NOT publish.

#### Scenario: partial write still publishes
ID: `sandbox.partial-mutation-publishes` (proposed)
- WHEN a mutating body returns a partial-write error
- THEN the harness still attempts the durable publication

#### Scenario: nonzero exit still publishes
ID: `sandbox.nonzero-exit-publishes` (proposed)
- WHEN a mutating body returns a nonzero execution exit
- THEN the harness still attempts the durable publication

### Requirement: Publication failure settlement

A durable publication failure MUST return an explicit `Unknown` result whose error kind is `OutcomeUnknown`. The existing execution path MUST record that uncertainty under the original call key through one governance and journal path, and the harness MUST NOT run a second such path. Any recorded error or output MUST be sanitized before persistence.

#### Scenario: mutation published before completion
ID: `sandbox.mutation-published-before-complete` (proposed)
- WHEN a `write_file` call mutates the workspace and the durable publication fails
- THEN the result outcome is `Unknown` with error kind `OutcomeUnknown` under the original call key

#### Scenario: publication error sanitized before persistence
ID: `sandbox.publication-error-sanitized` (proposed)
- WHEN a publication failure records an error message
- THEN the persisted message carries no unsanitized adapter output

### Requirement: Suspension release ordering

Suspension MUST publish through the coordinator before the checkpoint is saved, and MUST release only after the checkpoint is stored and the run's suspension transition succeeded. A snapshot or compare-and-set failure MUST save no checkpoint and MUST complete no suspension. A run with no acquired workspace MUST NOT publish. A failed close during the release MUST retain the durable snapshot and a cleanup record.

#### Scenario: release follows suspension storage
ID: `sandbox.release-after-suspend-storage` (proposed)
- WHEN a `PerSession` run suspends
- THEN `Publish` precedes the checkpoint save, `Release` follows the suspension transition, and the durable snapshot survives

#### Scenario: failed suspension publish saves no checkpoint
ID: `sandbox.suspend-publish-failure-aborts` (proposed)
- WHEN the snapshot or the compare-and-set fails during suspension
- THEN no checkpoint is saved and the run does not suspend

### Requirement: Terminal release ordering

The terminal path MUST publish before the terminal write and MUST release only after that write succeeds. A terminal write refused with `ErrSignalsPending` MUST keep the live handle and the heartbeat. A terminal publication failure MUST record `Failed` without a successful result ref, MUST NOT invent an uncertain-workspace run state, and MUST NOT repeat the failing publication while recording the failure.

#### Scenario: terminal release follows the terminal write
ID: `sandbox.terminal-publish-before-complete` (proposed)
- WHEN a `PerSession` run reaches its terminal write
- THEN the snapshot is published before the terminal write and the handle is released only after it succeeds

#### Scenario: refused terminal write keeps the handle
ID: `sandbox.signals-pending-keeps-handle` (proposed)
- WHEN the terminal write is refused with `ErrSignalsPending`
- THEN the live handle and the run heartbeat stay alive and no release runs

### Requirement: Cross-pod restore with independent coordinators

A restore on another pod MUST use its own coordinator instance and adapter instance, and MUST restore through the shared durable metadata and snapshot backend. A snapshot MUST survive the close of the original live resource. A restore MUST NOT depend on the live handle recorded by another process.

#### Scenario: independent pod restores the snapshot
ID: `sandbox.cross-pod-independent-coordinators` (proposed)
- WHEN pod A writes, snapshots and closes its handle and pod B restores the ref with its own coordinator
- THEN pod B reports the file written by pod A and opens no fresh workspace

#### Scenario: missing snapshot returns an error
ID: `sandbox.restore-missing-snapshot-errors` (proposed)
- WHEN the stored snapshot is missing or expired at restore time
- THEN the restore returns an error and opens no fresh workspace