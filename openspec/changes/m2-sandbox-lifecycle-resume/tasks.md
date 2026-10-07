# Tasks: m2-sandbox-lifecycle-resume

A task is done when its listed scenario IDs have a passing subtest named exactly the ID on the public path, its `verify:` command is green, and `task lint` and `task test` are green for what it touched. The fake `Sandbox` and the fake `SessionWorkspaceMeta` live in `_test.go` files and are never exported; no test reaches a network or a container. The `PerCall` path and the governance behaviour are unchanged: `sandbox.per-call-closes` and the other governance ids stay green.

## 1. Session workspace metadata surface in `core/stores`

- [ ] 1.1 Declare the optional `SessionWorkspaceMeta` surface beside `SessionStateMeta` (`core/stores/session.go:120-127`) — `WorkspaceRef(ctx, sessionID) (WorkspaceRef, bool, error)` and `SetWorkspaceRef(ctx, sessionID, ref) error` — as a type assertion target, never embedded in a port. Verify `go build ./...` and `go test -timeout 2m ./core/stores/... -run TestSessionWorkspaceMeta`.
  - evidence: `core/stores/session.go` (`SessionWorkspaceMeta`); `core/stores/session_memory.go` (`MemorySessionLog.WorkspaceRef`, `MemorySessionLog.SetWorkspaceRef`)
- [ ] 1.2 Implement the surface on `MemorySessionLog` beside `SharedStateMeta`/`SetSharedStateMeta` (`core/stores/session_memory.go:747-748,836-838`), keyed by session id, `ok=false` and the zero ref for a session with none. Verify `go test -timeout 2m ./core/stores/... -run TestSessionWorkspaceMeta`.
  - evidence: `core/stores/session_memory.go` (`WorkspaceRef`, `SetWorkspaceRef`); `core/stores/session_memory_test.go` (`TestSessionWorkspaceMeta`)
- [ ] 1.3 Land the `stores` main-spec edit the surface needs, under a new ADR (next number): the optional-interface entry beside `SessionIndex` (`openspec/specs/stores/spec.md:252-255`), the sentence in the *Optional interfaces* requirement (`:456`), and the Postgres-contract note (`:643`); then regenerate `docs/design/types.md`. Driver-owned at apply time. Evidence: `openspec/specs/stores/spec.md`; `docs/adr/` (the new ADR); design D8.
- [ ] 1.4 Implement `SessionWorkspaceMeta` on `adapter/postgres`'s concrete session store, per the spec's rule that adapter-specific seams are methods on the concrete adapter types (`openspec/specs/stores/spec.md:643`), and run the shared `storetest.SessionLog` conformance against it (`openspec/specs/stores/spec.md:351`). Verify `go -C adapter/postgres test -timeout 10m -tags full ./...`. Evidence: `adapter/postgres` (`WorkspaceRef`, `SetWorkspaceRef`); the `storetest` conformance run.

## 2. The `PerSession` tool path in `std/sandbox`

- [ ] 2.1 Replace the `types.ErrPerSessionUnsupported` branch in `withWorkspace` (`std/sandbox/percall.go:16-31`) with the `PerSession` path: read the ref through `SessionWorkspaceMeta` (refuse with `types.ErrPerSessionUnsupported` when the surface is absent), `Open` from the stored ref or the zero ref, resolve secrets and run the body as `PerCall` does, then `Workspace.Snapshot` and `SetWorkspaceRef`, and never `Close`. `PerCall` keeps its once-only deferred close unchanged. Verify `go test -timeout 2m ./std/sandbox/... -run TestPerSessionLifecycle`.
  - evidence: `std/sandbox/percall.go` (`withWorkspace`); `std/sandbox/persession.go` (the `PerSession` path); `std/sandbox/percall_test.go` (`TestPerSessionLifecycle`)
  - observed: `Sandbox.Open` is called with the session's stored ref on the second call and with the zero ref on the first, and the fake records `Snapshot` after each call with no `Close`.
- [ ] 2.2 Add the `sandbox.per-session-persists` subtest, named exactly its ID, in `std/sandbox/scenarios_test.go`: turn 1 writes `data.csv` and the produced ref reaches `SetWorkspaceRef`; turn 2's `Open` receives that ref and the read returns the file without a `Put` re-upload. Verify `go test -timeout 2m ./std/sandbox/... -run TestSandboxPerSessionPersists`.
  - evidence: `std/sandbox/scenarios_test.go` (`TestSandboxPerSessionPersists/sandbox.per-session-persists`)

## 3. Snapshot-on-suspend checkpoint hook in `core`

- [ ] 3.1 Set `Checkpoint.Workspace` in `Lifecycle.suspend` before `r.Save` (`core/drive_lifecycle.go:699-743`, the `r.Save` call at `:730`): read the session's ref through `SessionWorkspaceMeta` and copy it into the checkpoint — the single mechanism; D2 snapshots after every call, so the stored ref is always `Snapshot`-produced. Empty ref for a `PerCall` run or a run with no sandbox. Verify `go test -timeout 2m ./core/... -run TestSuspendSnapshotsWorkspace`.
  - evidence: `core/drive_lifecycle.go` (`suspend`); `core/drive_lifecycle_test.go` (`TestSuspendSnapshotsWorkspace`)
- [ ] 3.2 Add the `sandbox.snapshot-on-suspend` subtest, named exactly its ID: a `PerSession` run that wrote files suspends for `HumanApproval` and the saved checkpoint's `Workspace` holds the `Snapshot`-produced ref. Verify `go test -timeout 2m ./core/... -run TestSandboxSnapshotOnSuspend`.
  - evidence: `core/drive_lifecycle_test.go` (`TestSandboxSnapshotOnSuspend/sandbox.snapshot-on-suspend`)
- [ ] 3.3 Snapshot at the end of a detached run, as Contract rule 4 requires: where a detached run lands (`markRunEnded`, beside `dropRun` in `core/stream_lifetime.go`), call `Workspace.Snapshot` for a `PerSession` run and store the produced ref through `SetWorkspaceRef`; a `PerCall` run or a run with no sandbox is an empty no-op. Verify `go test -timeout 2m ./core/... -run TestDetachedEndSnapshotsWorkspace`.
  - evidence: `core/stream_lifetime.go` (`markRunEnded`); `core/drive_lifecycle_test.go` (`TestDetachedEndSnapshotsWorkspace`)

## 4. Resume restore and replay in `core`

- [ ] 4.1 Seed the session ref from `Checkpoint.Workspace` before the run re-drives on both paths: `Resume` (`core/resume.go:459`) and `Recover` (`core/recover.go:22-31`, `Recover` at `:28`). When the ref is non-empty, call `SessionWorkspaceMeta.SetWorkspaceRef(ctx, cp.SessionID, cp.Workspace)` before any tool executes, beside the credential re-issue in `recoveryContext` (`core/recover.go:610`). Verify `go test -timeout 2m ./core/... -run TestResumeRestoresWorkspace`.
  - evidence: `core/resume.go` (seed on resume); `core/recover.go` (seed on recovery); `core/..._test.go` (`TestResumeRestoresWorkspace`)
  - observed: the seed runs before the first sandbox call, so the next `Open` uses the checkpoint's ref.
- [ ] 4.2 Add the `sandbox.resume-restores` subtest, named exactly its ID: resuming on a second fake pod `Open`s from `Checkpoint.Workspace` and the fake workspace reports the file written before suspension. Verify `go test -timeout 2m ./core/... -run TestSandboxResumeRestores`.
  - evidence: `core/..._test.go` (`TestSandboxResumeRestores/sandbox.resume-restores`)
- [ ] 4.3 Add the `sandbox.replay-no-reexec` subtest, named exactly its ID: a run whose `run_code` completed before suspending replays from the journaled `ExecResult` (`core/stores/journal.go:56-61,13-19`; `replayModelStep` entry `core/drive_model_journal.go:52-55`, `core/drive_turn.go:296-308`) and the fake `Exec` call count does not increase. Verify `go test -timeout 2m ./core/... -run TestSandboxReplayNoReexec`.
  - evidence: `core/..._test.go` (`TestSandboxReplayNoReexec/sandbox.replay-no-reexec`)

## 5. Reaper boundary

- [ ] 5.1 Reach the port from the lease-expiry sweep without a `core` → `std/sandbox` import: carry the configured `types.Sandbox` on the `Stack` through a build option, and on a reclaim that finds a session with no live lease and a stored `PerSession` ref, open with that ref, call `Close`, and clear the ref through `SetWorkspaceRef` with the empty value. A build with no `Sandbox` wired leaves the boundary inert. Verify `go test -timeout 2m ./core/... -run TestReaperClosesSessionWorkspace`.
  - evidence: `core/build_options.go` (`WithSandbox`); `core/recover.go` (reaper close path); `core/..._test.go` (`TestReaperClosesSessionWorkspace`)

## 6. Package gates

- [ ] 6.1 Regenerate `docs/design/types.md` after the store-surface addition and confirm the `Workspace` and `WorkspaceRef` index entries are unchanged; verify `task spec:types`.
  - evidence: `docs/design/types.md` (`SessionWorkspaceMeta`; `Workspace`, `WorkspaceRef` unchanged)
- [ ] 6.2 Run the repository wrappers and resolve failures; verify `task lint` and `task test`.
  - evidence: `task lint` 0 issues; `task test` green across root and modules
- [ ] 6.3 Validate the change package; verify `openspec validate m2-sandbox-lifecycle-resume --strict --no-interactive` → `is valid`, and `task spec:gate GATE=M2` reports the four owned sandbox ids covered with the adapter ids still open under their own owners.
  - evidence: `openspec validate m2-sandbox-lifecycle-resume --strict --no-interactive`; `task spec:gate GATE=M2`
  - observed: the four owned ids — `sandbox.per-session-persists`, `sandbox.snapshot-on-suspend`, `sandbox.resume-restores`, `sandbox.replay-no-reexec` — are covered; `sandbox.per-call-closes` stays with `m2-sandbox-governance`, and the egress and exec-timeout ids stay with `m2-sandbox-adapter-conformance`.
