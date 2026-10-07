# Proposal: m2-sandbox-lifecycle-resume

## Why

The sandbox port already accepts `PerSession` (`core/types/sandbox.go:14-24`) and `std/sandbox.New` builds a `PerSession` tool set (`std/sandbox/sandbox.go:98-160`), but its call path refuses the lifecycle: `withWorkspace` returns `types.ErrPerSessionUnsupported` (`std/sandbox/percall.go:18`, sentinel at `core/types/errors.go:69`). The `Workspace.Snapshot` method is declared (`core/types/sandbox.go:82`) and never called in production code, and the checkpoint saved on suspension carries an empty `Workspace` field (`core/drive_lifecycle.go:726-741`, field at `core/stores/checkpoint.go:49`). A run that writes files and then suspends cannot be resumed on another pod, because nothing persists the workspace ref. This change implements the half of the `Lifecycle and resume` requirement that the governance change left open.

## What Changes

- Add an optional `stores.SessionWorkspaceMeta` surface beside `SessionStateMeta` (`core/stores/session.go:120-127`) carrying the per-session `WorkspaceRef` across turns. This is a new optional store surface; it is declared here because the core budget rule requires it to be named.
- Implement the `PerSession` branch of `withWorkspace` (`std/sandbox/percall.go:16-31`): open from the stored session ref (zero ref when absent), run the body, call `Workspace.Snapshot`, persist the produced ref through `SessionWorkspaceMeta`, and leave `Close` to the reaper. `PerCall` behaviour is unchanged.
- Set `Checkpoint.Workspace` in the suspend hook before `r.Save` (`core/drive_lifecycle.go:699-743`) from the session's snapshotted ref, so the checkpoint carries the workspace for a resume on any pod.
- Seed the session workspace ref from `Checkpoint.Workspace` on the resume and recovery paths (`core/resume.go:459`, `core/recover.go`) before the run re-drives, so the next sandbox call opens the restored workspace.
- Prove that `Replay` uses the journaled `ExecResult` and does not call the adapter's `Exec` again, over the shipped journal (`core/stores/journal.go:56-61`).

No port signature, value type, stored shape or event changes. No isolation technology and no adapter work; `m2-docker-sandbox` and `m2-e2b-sandbox` own adapters, and `m2-sandbox-adapter-conformance` owns the egress and exec-timeout rows.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `sandbox`: the `Lifecycle and resume` requirement gains the `PerSession` persistence, snapshot-on-suspend, resume-restore and replay guarantees. The requirement's `PerCall` prose and all five of its scenario blocks stay verbatim.

## Impact

Affected code: `core/stores` (a new optional session metadata surface and its `MemorySessionLog` implementation), `std/sandbox/percall.go` (the `PerSession` path), `core/drive_lifecycle.go` (the suspend hook), `core/resume.go` and `core/recover.go` (seeding the ref on resume and recovery). No `openspec/specs/` edit is authored by this package; the main-spec edit lands at apply time. The sandbox port stays frozen at v1 (`docs/design/compatibility.md`), and `docs/design/types.md` is regenerated after the type addition. This change owns the four scenario ids `sandbox.per-session-persists`, `sandbox.snapshot-on-suspend`, `sandbox.resume-restores` and `sandbox.replay-no-reexec` (`openspec/changes/m2/design.md:84-87`); the other eleven sandbox ids stay with their owners.
