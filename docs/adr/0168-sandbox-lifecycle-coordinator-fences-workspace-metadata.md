# 0168. Sandbox lifecycle coordinator fences workspace metadata by generation and owner

Status: accepted

## Context

The `PerSession` sandbox lifecycle was declared and refused: `std/sandbox.New`
accepted `PerSession` and `withWorkspace` returned `types.ErrPerSessionUnsupported`
before opening anything (`std/sandbox/percall.go`, sentinel at
`core/types/errors.go`). `Workspace.Snapshot` had no production caller, and the
checkpoint saved on suspension carried an empty `Workspace` field
(`Lifecycle.suspend`, `core/drive_lifecycle.go`). A run that wrote files and then
suspended could not be resumed on another pod, because nothing durably persisted
the snapshot, no owner-keyed live handle existed to close, and two publications
could race one session pointer with no compare-and-set token.

Four constraints shaped the settlement. The layer rule keeps ports, types and
contracts in `core` and every adapter and policy value in `std`, with `core` never
importing `std` (`docs/design/architecture.md`, `.golangci.yml`), so a coordinator
that owns adapter and policy state belongs in `std/sandbox`. The sandbox port is
frozen at the M2 exit under `docs/design/compatibility.md`, so no method may be
added to `Sandbox` or `Workspace`. Ownership already has a store-minted proof, the
run `Lease{RunID, Generation, Expires}`, whose generation is minted by `Start`,
`Resuming` and `Reclaim` and preserved by `Heartbeat`. The two stores validate
under different serialization: `MemorySessionLog` holds two mutexes and
`adapter/postgres` holds an advisory session lock and row locks.

## Decision

**Coordinator port and placement (design D1).** `core/types` declares the optional
`SandboxHandles` port beside `Sandbox`, with exactly `Acquire`, `Publish`, `Release`
and `Sweep`, plus the value types `WorkspaceOwner{RunID, LeaseGeneration}` and
`WorkspaceSnapshot{Ref, Generation}`. There is no `Withdraw` operation and no
withdrawal milestone; `Publish` publishes an owner-fenced snapshot without releasing
the live handle, and a successful `Publish` is never read as quiescence. The
implementation lives in `std/sandbox`, not `core`, because it owns the adapter and
the policy. `std/sandbox` constructs one coordinator and passes the same instance to
the tool set and to `Build`; `core` holds it only through one `Option`,
`WithSandboxRegistration(SandboxRegistration{Handles, Policy})`, and `Build` fails
when a registered policy declares `PerSession` with no coordinator or no
`SessionWorkspaceMeta`, returning no tool set.

**Generation-plus-owner atomic metadata (design D3, D12).** `core/stores` declares
the optional `SessionWorkspaceMeta` surface beside `SessionIndex`:
`WorkspaceMeta(ctx, sessionID) (WorkspaceMeta, bool, error)`,
`SetWorkspaceMeta(ctx, sessionID, expectedGeneration int64, m WorkspaceMeta) (int64, error)`
and `ImportWorkspaceMeta(ctx, sessionID, owner WorkspaceOwner, legacy WorkspaceSnapshot) (WorkspaceSnapshot, error)`.
`WorkspaceMeta` holds a `Ref`, a `Generation` and an `Owner`. The generation is the
sole compare-and-set token; the store mints no version the caller cannot see. A
write compares `expectedGeneration` and validates the owner lease in one atomic
step, and a publication writes `expectedGeneration + 1`. The refusal precedence is
one order both stores apply: authorize the session write, compare the workspace
token (including the equal-generation-different-ref case), validate the owner proof,
then assign. A stale token returns `types.ErrVersionConflict`; a stale owner returns
`types.ErrRunNotActive`; the token refusal precedes the owner refusal. Restore
follows D3: equal metadata issues no write (the restore caller compares and skips),
an older checkpoint leaves the stored snapshot and never discards it (a zero-valued
old checkpoint is oldest), a newer checkpoint needs a matching durable publication
or fails, and a fresh open applies only when the session holds no stored metadata and
the checkpoint carries no ref. `stores.Checkpoint` gains the additive
`WorkspaceGeneration int64`; a checkpoint written before this change reads zero.

**Memory lock order and owner guard (design D10).** `MemorySessionLog` validates the
owner through a wired `SessionWorkspaceOwnerGuard` seam,
`WithWorkspaceOwner(ctx, sessionID, owner, write func() error) error`, wired with
`WithSessionWorkspaceOwnerGuard(guard)`. `SetWorkspaceMeta` takes
`MemorySessionLog.mu` and checks caller authority, compares the stored generation,
then calls the guard, which holds `MemoryRuns.mu` while it validates the four
conditions (`onSess[sessionID] == owner.RunID`, the run's session, a nonzero matching
lease generation, and a live unexpired lease against the store clock) and runs the
write under both locks. The lock order is fixed and one-directional:
`MemorySessionLog.mu` precedes `MemoryRuns.mu`, the same order `Delete` already
uses. An absent guard refuses every write. The guard callback runs inside both locks
and MUST NOT take either mutex, call another store, or call a provider or caller
callback, because a caller-supplied extractor that re-enters the session store while
the runs mutex is held would deadlock.

**PostgreSQL session-then-run row locks (design D11).** `adapter/postgres`
implements the same contract in one transaction: take the existing session
serialization lock (`lockSession`), lock `session_record`, lock the owning
`run_record` last, authorize the session write, compare the stored workspace
generation (returning `types.ErrVersionConflict` on a mismatch), validate session
identity, run identity, current generation, active state and lease expiry (returning
`types.ErrRunNotActive` on a failed proof), write `expectedGeneration+1`, and commit
without releasing the owner row lock. Because `Heartbeat` and `Reclaim` lock
`run_record`, neither can change the validated lease between the validation and the
commit. Any operation that needs both rows uses the session-before-run order; a
run-only mutation keeps run-only locking.

**Suspend and terminal ordering (design D4).** Suspension runs
`Publish` → `r.Save` with the returned ref and generation in `Checkpoint.Workspace`
and `Checkpoint.WorkspaceGeneration` → the run's suspension transition → `Release` →
`Suspended`; a snapshot or compare-and-set failure saves no checkpoint and completes
no suspension, and a run with no acquired workspace publishes nowhere. The terminal
path runs `Publish` → the terminal write → `Release` → the terminal event; a detached
run uses this order and creates no checkpoint, and a terminal write refused with
`ErrSignalsPending` preserves the live handle and the heartbeat so no release runs.
`Release` closes the exact `Workspace` that `Acquire` received from `Open` and never
opens a workspace in order to close one. A failed close retains the durable snapshot
and a cleanup record for the next bounded `Sweep`, which `Stack.Recover` invokes even
when `Runs.Stale` returns no rows.

**Killed-pod cleanup blocker (design Admission).** Remote cleanup of a resource
whose owning process is gone stays unmet. It needs a sandbox provider contract that
supplies durable resource enumeration and close by resource identity, and no
installed adapter module (`adapter/eino`, `adapter/postgres`, `adapter/openai`,
`adapter/anthropic`, `adapter/otel`) declares one. The `m2-docker-sandbox` lane
supplies the Docker leg of the provider contract; the `m2-e2b-sandbox` lane is
blocked and supplies none. Task 6.5 stays unchecked and the case is reported as an
unmet admission blocker, not as an accepted scope exclusion.

## Related records

ADR-0093 (sandboxed execution) declares the port; ADR-0129 fixes its capability
version. ADR-0117 governs API compatibility and the optional-interface growth path
this change follows.
