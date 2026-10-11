# Proposal: m2-sandbox-lifecycle-resume

## Why

The sandbox port already accepts `PerSession` (`core/types/sandbox.go:14-24`) and `std/sandbox.New` builds a `PerSession` tool set (`std/sandbox/sandbox.go:98-160`), but its call path refuses the lifecycle: `withWorkspace` returns `types.ErrPerSessionUnsupported` (`std/sandbox/percall.go:18`, sentinel at `core/types/errors.go:69`). `Workspace.Snapshot` is declared (`core/types/sandbox.go:82`) and never called in production code, and the checkpoint saved on suspension carries an empty `Workspace` field (`core/drive_lifecycle.go:726-741`, field at `core/stores/checkpoint.go:49`). A run that writes files and then suspends cannot be resumed on another pod, because nothing durably persists the workspace snapshot, and no owner-keyed live handle exists to close or enumerate.

## What Changes

- Add a live-handle coordinator port `types.SandboxHandles` beside the `Sandbox` port, implemented in `std/sandbox` and constructed once, so the same adapter and policy serve the tool set, the driver's lifecycle hooks and the recovery sweep. It owns one handle per `(session, run owner)`, binds ownership to the run id and the store-minted lease generation, and never opens a workspace in order to close one.
- Add the optional durable `stores.SessionWorkspaceMeta` surface with a generation compare-and-set that also validates the current lease owner in the same atomic write, plus a `WorkspaceGeneration` field on `stores.Checkpoint`, so a restore never overwrites a later completed generation and a stale owner cannot publish. The memory store validates the owner through a wired guard that holds both store locks across the write; the PostgreSQL store validates it under the session serialization lock, the session row and then the run row. Add `ImportWorkspaceMeta` for an authorized legacy ref, because `Resume` and `Recover` never import one implicitly.
- Implement the `PerSession` branch of `withWorkspace` (`std/sandbox/percall.go:16-31`): acquire the session's live handle, restore from the durable snapshot when none is live, run the body, and publish durably after every body invocation that may mutate. A publication failure returns an explicit `Unknown` result whose error kind is `OutcomeUnknown`. `PerCall` behaviour is unchanged.
- On suspension, publish through the coordinator before the checkpoint is saved and set `Checkpoint.Workspace` and its generation from the returned snapshot (`core/drive_lifecycle.go:726-741`); release the handle only after the suspension transition succeeded. At terminal end, publish before the terminal write and release only after it succeeds; a terminal write refused with `ErrSignalsPending` keeps the handle and the heartbeat; a detached run creates no checkpoint.
- Seed the session snapshot from `Checkpoint.Workspace` on resume and recovery (`core/resume.go:459`, `core/recover.go`) under the D3 restore rules, so a checkpoint older than the stored snapshot is never applied and a missing snapshot returns an error.
- Fail `Build` when a `PerSession` policy is declared without the coordinator or the durable metadata surface. Prove `Replay` uses the journaled `ExecResult` and does not call the adapter's `Exec` again.
- Run a bounded cleanup sweep from `Stack.Recover` even when no stale run is listed; claim acquisition and live acquisition exclude each other, and a failed close keeps the durable snapshot.

No `Sandbox` or `Workspace` port growth, no value-type change to `WorkspaceRef`, `Lifecycle` or `SandboxPolicy`, and no isolation technology. Killed-pod remote cleanup is a requested acceptance case that stays unmet until a sandbox provider exists: no installed adapter module provides durable snapshot retention or close-by-resource-identity. It is reported as an unmet blocker, not as an accepted exclusion. `m2-docker-sandbox` and `m2-e2b-sandbox` own adapters; `m2-sandbox-adapter-conformance` owns the egress and exec-timeout rows. Output sanitization is inherited from `protect-tool-output-storage`; this change adds no conflicting redaction.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `sandbox`: the `Lifecycle and resume` requirement's `PerSession` prose gains lease-fenced live handles, the suspension snapshot, the publish-then-release terminal order, the generation-and-owner-fenced restore and the missing-snapshot error. Its `PerCall` prose and all five scenario blocks stay verbatim. Focused new requirements add handle ownership and fencing, bounded cleanup sweep, build admission, durable generation-fenced metadata, restore rules, explicit legacy import, mutation durability and settlement, lifecycle release ordering, and cross-pod restore.

## Impact

Affected code: `core/types` (the coordinator port, `WorkspaceOwner`, `WorkspaceSnapshot`), `std/sandbox` (the coordinator implementation, its construction shared with `New`, the `PerSession` path), `core` (`WithSandboxRegistration`, the `Build` admission, the `suspend`, `finishRun` and `Recover` hooks, resume/recover restore), `core/stores` (the `SessionWorkspaceMeta` surface, its `ImportWorkspaceMeta` operation, the `SessionWorkspaceOwnerGuard` seam, its `MemorySessionLog` and `adapter/postgres` implementations, and `Checkpoint.WorkspaceGeneration`), `docs/design/architecture.md` (the placement rule the coordinator follows). The `sandbox` main-spec correction and the `stores` optional-interface edit land at apply time under one new ADR, and `docs/design/types.md` regenerates after the type additions. The sandbox port stays frozen at v1 (`docs/design/compatibility.md`). This change owns the four scenario ids `sandbox.per-session-persists`, `sandbox.snapshot-on-suspend`, `sandbox.resume-restores` and `sandbox.replay-no-reexec` (`openspec/changes/m2/design.md:84-87`); the other sandbox ids stay with their owners.

One prerequisite blocks full admission and is stated in the design's Admission section: no installed sandbox provider establishes snapshot survival after close, a missing-snapshot contract, or close by resource identity. The atomic owner validation is resolved in the design, for the memory and the PostgreSQL implementations, and is implemented by tasks 2.1 and 2.2.