# Design: m2-sandbox-lifecycle-resume

## Context

The sandbox port, the policy and the governed tool set ship from `2026-10-07-m2-sandbox-governance`: `Sandbox.Open(ctx, WorkspaceRef, SandboxPolicy) (Workspace, error)` (`core/types/sandbox.go:70-73`), the six-method `Workspace` including `Snapshot(ctx) (WorkspaceRef, error)` (`core/types/sandbox.go:77-85`), `Lifecycle` with `PerCall` and `PerSession` (`core/types/sandbox.go:14-24`), and `std/sandbox.New` building the four tools (`std/sandbox/sandbox.go:98-160`). The port method set is unchanged and frozen at the M2 exit under `docs/design/compatibility.md`.

Three things the port declares are unused in production code, and this change is where they take effect:

- `PerSession` is accepted by `New` and derives its effect, but the call path refuses it: `withWorkspace` returns `types.ErrPerSessionUnsupported` before opening anything (`std/sandbox/percall.go:16-31`, sentinel at `core/types/errors.go:69`). The `PerCall` path is complete and stays: one open, a deferred close guarded by a `sync.Once` (`std/sandbox/percall.go:25`), and secret resolution and redaction around the body (`std/sandbox/secrets.go:23`).
- `Workspace.Snapshot` has no production caller: the only references are the interface declaration (`core/types/sandbox.go:82`) and the frozen Contract, so nothing produces a `WorkspaceRef` to persist.
- `Checkpoint.Workspace WorkspaceRef` exists on the stored checkpoint (`core/stores/checkpoint.go:49`), and the suspend hook saves a checkpoint without it: `Lifecycle.suspend` builds a `stores.Checkpoint` and calls `r.Save` (`core/drive_lifecycle.go:699-743`, the `r.Save` call at `:730`) with `Workspace` left empty.

No session-metadata surface carries a `WorkspaceRef` today. `SessionStateMeta` carries the shared-state value and version only (`core/stores/session.go:120-127`), and `SessionPatch` carries index fields (`Title`, `Archived`, `Pinned`, `Hold`, `Handoff`) only (`core/stores/session.go:96-104`). The journal seam that makes replay work for tool calls already ships: `stores.Journal` records a call and answers by fingerprint (`core/stores/journal.go:56-61`), a `Completed` entry's `Result` is the replayable `ExecResult` (`core/stores/journal.go:13-19`), and `replayModelStep` restores a recorded response instead of re-issuing it (`core/drive_model_journal.go:52-55`, entered from `core/drive_turn.go:296-308`). This change does not add a journal; it binds the sandbox call to it and adds the workspace restore so a replay sees the same files.

## Goals / Non-Goals

**Goals:** Implement the `PerSession` lifecycle so one workspace persists across turns through a session-scoped `WorkspaceRef`; snapshot the workspace on suspension and carry the ref in `Checkpoint.Workspace`; restore the workspace from that ref on a resume or recovery on another pod; prove that a replayed `run_code` uses the journaled `ExecResult` and never calls the adapter's `Exec` again; turn the four owned scenario ids green on the public path.

**Non-Goals:** No isolation technology, no `adapter/docker` and no `adapter/e2b` (separate changes). No port signature change, no new port method, no `Sandbox` or `Workspace` growth: the lifecycle change is an implementation, not a signature change. No second workspace handle for `PerCall`. No new egress, secret, output, limit or host-runner behaviour; those ship from the governance change. No snapshot format of the harness's own: the ref stays opaque. No `tool/exec` change.

## Decisions

### D1 — The per-session workspace ref lives on a new optional session-metadata surface

The spec's Contract rule 4 stores the `PerSession` `WorkspaceRef` in session metadata, but no shipped surface can carry it (Context). This change declares one optional interface beside `SessionStateMeta` (`core/stores/session.go:120-127`), discovered by type assertion under the same rule every other optional store surface follows (`SessionForker` at `core/stores/session.go:32-37`, `SessionStateMeta` at `:120-127`):

```go
type SessionWorkspaceMeta interface {
	WorkspaceRef(ctx context.Context, sessionID string) (WorkspaceRef, bool, error)
	SetWorkspaceRef(ctx context.Context, sessionID string, ref WorkspaceRef) error
}
```

`MemorySessionLog` implements it beside `SharedStateMeta`/`SetSharedStateMeta` (`core/stores/session_memory.go:747-748,836-838`). This is a new optional store surface, named here because the core budget rule requires it: it is a store interface, and it is optional, so a `SessionLog` without it cannot run `PerSession` and the tool set refuses with `types.ErrPerSessionUnsupported` exactly as it does today. The ref is not versioned like shared state: only the owner run writes it, `Snapshot` is idempotent for an unchanged workspace, and last-write-wins is the correct semantics for one session's own workspace pointer. The value is an opaque `WorkspaceRef`, not a new stored shape, so no checkpoint schema or event changes.

### D2 — The `PerSession` tool path opens from the stored ref and snapshots back; the reaper closes

`withWorkspace` (`std/sandbox/percall.go:16-31`) branches on `p.Lifecycle`. `PerCall` keeps today's Open → body → once-close path unchanged. `PerSession`:

1. Resolve `SessionWorkspaceMeta` from the run `ctx`; without it, refuse with `types.ErrPerSessionUnsupported`.
2. Read the stored ref (`WorkspaceRef`, `ok`). Absent means the session's first sandbox call: `Open` with the zero `WorkspaceRef`, which the port already declares as the fresh-workspace value (`core/types/sandbox.go:9-12`).
3. Resolve secrets and run the body as `PerCall` does (`resolveSecrets`, `std/sandbox/secrets.go:23`; `redactSecrets`), so secret handling is identical across lifecycles.
4. Call `Workspace.Snapshot`, then `SetWorkspaceRef` with the produced ref, so the next call in the session — and the suspend hook — can open from it.
5. Do not `Close`. The handle is released by the reaper when the session's lease expires (D7).

Step 4's Snapshot after every call is what makes persistence durable and keeps the stored ref pointing at a snapshot that holds the latest files; it is also the ref the checkpoint carries (D4). Opening from a snapshot ref per call means a resumed session on another pod needs no live handle to continue: `Open(ctx, ref, p)` restores.

### D3 — The snapshot format is the adapter's, not the harness's

`WorkspaceRef` is an opaque `string` (`core/types/sandbox.go:12`) and `Checkpoint.Workspace` carries it unchanged (`core/stores/checkpoint.go:49`). The harness MUST NOT parse, prefix or validate the ref's contents: it reads a ref from `Snapshot`, persists it through D1 and hands it back to `Open`. This is the format decision: the harness declares no format, and an adapter is free to encode a microVM id, a gVisor checkpoint path or a container commit id. The conformance suite (`m2-sandbox-adapter-conformance`) and the adapters bind to the opacity, not to a shape.

### D4 — The suspend hook sets `Checkpoint.Workspace` from the session's snapshotted ref

`Lifecycle.suspend` already builds the checkpoint and calls `r.Save` (`core/drive_lifecycle.go:699-743`, `:730`). This change sets `Workspace` on that `stores.Checkpoint` before `Save`: when the run held a `PerSession` sandbox, the suspend hook reads the session's ref through `SessionWorkspaceMeta` (D1) and copies it into `Checkpoint.Workspace`. Because D2 snapshots after every call, that ref is `Snapshot`-produced and holds the files written before suspension, satisfying `sandbox.snapshot-on-suspend` without a second `Snapshot` at suspend time. The metadata copy is the only mechanism: no live workspace handle is bound into the run context, and D2's per-call snapshot keeps the stored ref fresh. `Checkpoint.Workspace` is empty for a `PerCall` run or a run with no sandbox, and `Resume` treats an empty ref as "no workspace to restore".

### D5 — Resume and recovery seed the session ref from `Checkpoint.Workspace`

`Resume` consumes the token and re-drives the checkpoint (`core/resume.go:459`); `Recover` reclaims a stale run and replays its state (`core/recover.go:22-31`, `Recover` at `:28`). Both read the `stores.Checkpoint` they already hold. This change adds: when `Checkpoint.Workspace` is non-empty, seed `SessionWorkspaceMeta.SetWorkspaceRef(ctx, cp.SessionID, cp.Workspace)` before the run re-drives. The next sandbox call then follows D2 step 2 and `Open`s from that ref, so the restored workspace holds the files written before suspension. The seed runs before any tool executes, beside the credential re-issue that `recoveryContext` already performs (`core/recover.go:610`), so restoration cannot race a tool call. No new admission is needed: the resume path already validates the transport principal, the token and the input before this point.

### D6 — Replay reuses the journaled `ExecResult`; the change proves it and does not add a journal

A sandbox call is a tool call, so it is journaled under the shipped path: `Reserve` pins the call key, `Complete` records the `types.ToolResult`, and a replay answers from the `Completed` entry's `Result` (`core/stores/journal.go:56-61,13-19`). `Replay`/`Recover` re-enter the batch phase and replay recorded results from the state and journal rather than re-executing (`core/drive_model_journal.go:52-55`, `core/drive_turn.go:296-308,565-575`). This change adds no journal, no entry field and no replay branch; it proves the guarantee for a sandbox `ExecResult` and adds the workspace restore (D5) that must accompany the replay so the replayed result and the restored files agree. `sandbox.replay-no-reexec` asserts the adapter's `Exec` is not called again.

### D7 — The reaper boundary: `Close` for a `PerSession` workspace is the lease-expiry sweep

The spec puts `Close` for `PerSession` on the reaper when the session's lease expires (Contract rule 4). `core` owns the existing lease-expiry reclaim: `recoverStaleAfter = stores.LeaseTTL` (`core/recover.go:22`) and `Stack.Recover` reclaims runs whose lease expired (`core/recover.go:28-31`). The close path reaches the port without a `core` → `std/sandbox` import (depguard denies it, as the governance design recorded for the host-exec warning): the `Stack` carries the configured `types.Sandbox` through a build option, and on a reclaim that finds a session with no live lease and a stored `PerSession` ref, the reaper opens the workspace with that ref and calls `Close`, then clears the ref through `SetWorkspaceRef` with the empty value. Opening to close is the only path the frozen port allows — `Sandbox` is a one-method factory and gains no close-by-ref method — and it is safe because a restored workspace's `Close` releases the snapshot's resources without reading or writing files. A build with no `Sandbox` wired leaves the boundary inert: nothing was opened, so nothing leaks.

### D8 — No Contract code block changes; the port freezes unchanged

This change edits no Contract code block in `openspec/specs/sandbox/spec.md`. The port (`Sandbox`, `Workspace`), the value types (`WorkspaceRef`, `Lifecycle`, `SandboxPolicy`) and the optional interfaces (`SecretResolver`, `WorkspaceLister`) are unchanged at v1; the ADR that settles the sandbox capability is ADR-0129 (cited in the spec header, `openspec/specs/sandbox/spec.md:3`) and the sandboxed-execution ADR is ADR-0093. The one new type is `SessionWorkspaceMeta` in `core/stores`. It is not part of the sandbox capability's Contract block, and it is Contract-adjacent in `stores`: `SessionIndex`, its precedent, lives in the `stores` Contract code block (`openspec/specs/stores/spec.md:252-255`) and in the Contract prose and the Postgres contract requirement (`:336`, `:643`). The `stores` main-spec edit at apply time therefore has three parts: the optional-interface entry beside `SessionIndex`, the sentence in the `Optional interfaces` requirement (`:456`), and the Postgres-contract note (`:643`); each lands with the main-spec edit at apply time under a new ADR (next number) deciding the optional surface, and `docs/design/types.md` regenerates to index it. No sandbox Contract code block changes. `docs/design/types.md` is regenerated to index the new store surface; the `Workspace` and `WorkspaceRef` index entries are unchanged.

### D9 — Public-path proofs

The four owned scenarios are subtests named exactly their ids, driven through the public `Build`/`Send`/`Resume` path with an in-package fake `Sandbox` that records `Open`/`Exec`/`Snapshot`/`Close` calls and a fake `SessionWorkspaceMeta` that records the ref, so no test reaches a container or a network:

- `sandbox.per-session-persists` (`std/sandbox/scenarios_test.go`) — turn 1 writes `data.csv` and the ref is persisted; turn 2's `Open` receives that ref and the read returns the file; the second call never re-uploads it.
- `sandbox.snapshot-on-suspend` (`core/...`, driving `suspend`) — a `PerSession` run that wrote files suspends for `HumanApproval`; the saved checkpoint's `Workspace` holds the `Snapshot`-produced ref.
- `sandbox.resume-restores` (`core/...`, driving `Resume`) — resuming on a second fake pod opens from `Checkpoint.Workspace` and the fake workspace reports the file written before suspension.
- `sandbox.replay-no-reexec` (`core/...`, driving the replay path) — a run whose `run_code` completed before suspending replays the journaled `ExecResult`; the fake `Exec` call count does not increase.

## Risks / Trade-offs

- [Risk] A new optional store surface (`SessionWorkspaceMeta`) touches the `core/stores` port tier the freeze watches. → Accepted: optional surfaces are the declared growth path (`SessionForker`, `SessionStateMeta`), the sandbox port itself does not change, and a `SessionLog` without it keeps today's refusal. The surface is named in this design because the core budget rule requires store-interface additions to be declared.
- [Risk] Snapshotting after every `PerSession` call could be expensive on a real adapter. → Accepted and bounded: correctness first, and the ref must hold the latest files for a suspend that may arrive between calls. An adapter is free to make `Snapshot` cheap against an unchanged workspace; the tool set does not snapshot twice in one call.
- [Risk] The reaper opens a workspace only to close it, which is inelegant on the frozen port. → Accepted: `Sandbox` is a one-method factory with no close-by-ref method (D7), and closing through `Open` + `Close` is the only path that does not grow the port. A build without a `Sandbox` wired leaves the boundary inert.
- [Risk] The `PerSession` ref and the checkpoint ref can diverge if a call snapshots after the suspend hook reads the ref. → Mitigated: the suspend hook reads the ref after the last call in the turn has snapshotted (D4), so the checkpoint carries a ref at least as fresh as the last completed call.
- [Trade-off] Resume restores by seeding session metadata rather than binding a live workspace handle into the run context. → Accepted: metadata is the durable, pod-independent carrier the Contract rule names, and it keeps the sandbox handle's lifetime inside `std/sandbox`, not `core`.

## Migration Plan

No data migration and no stored-shape change: `Checkpoint.Workspace` already holds a `WorkspaceRef` and was empty; it now carries a ref for a `PerSession` run. `core/stores` gains the optional `SessionWorkspaceMeta` interface and its `MemorySessionLog` implementation; `std/sandbox/percall.go` replaces the `PerSession` refusal branch with the path in D2; `core/drive_lifecycle.go` sets `Checkpoint.Workspace` in `suspend`; `core/resume.go` and `core/recover.go` seed the session ref before re-driving; `core` gains the `Sandbox` build option the reaper reads. No exported contract changes outside the new optional surface. Shared driver files (`core/build_options.go`) follow the milestone's admission-order rule (`openspec/changes/m2/tasks.md:19`): the `m2-context-persisted-compaction` lane is admitted first, and the two lanes never touch a shared driver file concurrently. Keep the module pre-v1 and create no release tag while authoring; `m2-api-freeze` runs the port compatibility check and cuts `v1.0.0` at the M2 exit.
