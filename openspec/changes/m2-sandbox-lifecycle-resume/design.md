# Design: m2-sandbox-lifecycle-resume

## Context

The sandbox port, the policy and the governed tool set ship from `2026-10-07-m2-sandbox-governance`: `Sandbox.Open(ctx, WorkspaceRef, SandboxPolicy) (Workspace, error)` (`core/types/sandbox.go:70-73`), the six-method `Workspace` including `Snapshot(ctx) (WorkspaceRef, error)` (`core/types/sandbox.go:77-85`), `Lifecycle` with `PerCall` and `PerSession` (`core/types/sandbox.go:14-24`), and `std/sandbox.New` building the four tools (`std/sandbox/sandbox.go:98-160`). The port method set is unchanged and frozen at the M2 exit under `docs/design/compatibility.md`.

Four things the port declares or lacks shape this change:

- `PerSession` is accepted by `New` and derives its effect (`std/sandbox/sandbox.go:104-107`), but the call path refuses it: `withWorkspace` returns `types.ErrPerSessionUnsupported` before opening anything (`std/sandbox/percall.go:16-31`, sentinel at `core/types/errors.go:69`). The `PerCall` path is complete and stays.
- `Workspace.Snapshot` has no production caller, so nothing produces a `WorkspaceRef` to persist.
- `Checkpoint.Workspace WorkspaceRef` exists on the stored checkpoint (`core/stores/checkpoint.go:49`), and the suspend hook saves a checkpoint without it (`Lifecycle.suspend`, `core/drive_lifecycle.go:726-741`, `r.Save` at `:730`). The run's durable terminal write is `Lifecycle.finishRun` (`core/drive_lifecycle.go:1080-1091`); a terminal write refused with `ErrSignalsPending` keeps the run alive so the heartbeat survives.
- No surface carries a live handle, an owner or a durable `WorkspaceRef`. `SessionStateMeta` carries only the shared-state value and version (`core/stores/session.go:120-127`). Ownership has a store-minted proof already: `Lease{RunID, Generation, Expires}` where `Generation` is minted by `Start`, `Resuming` and `Reclaim` (`core/stores/runs.go:76-81`) and preserved unchanged by `Heartbeat`, which refreshes only `Expires` (`core/stores/runs_leases.go:14-29`; the same rule is already stated in `openspec/specs/stores/spec.md:394`). The journal seam that makes replay work ships: `stores.Journal` answers by fingerprint (`core/stores/journal.go:56-61`) and a `Completed` entry's `Result` is the replayable payload (`:13-19`).

The layer rule constrains placement. `docs/design/architecture.md:45-47` keeps ports, types and contracts in `core` and every default, matcher, heuristic, preset or policy value in `std`, with `core` never importing `std` (`.golangci.yml:11-37`). The coordinator holds policy and adapter state, so its implementation belongs in `std/sandbox`, not `core`.

## Goals / Non-Goals

**Goals:** implement the `PerSession` lifecycle with one lease-fenced live handle per `(session, run)`; persist a generation-fenced durable snapshot and carry it in the checkpoint at suspension; publish the durable snapshot before the run's terminal write and release the handle only after it succeeds; restore on another pod only from a generation not older than the stored one; close only the handle `Acquire` received from `Open`; fail `Build` when a `PerSession` build lacks the required surfaces; turn the owned scenario ids green on the public path.

**Non-Goals:** no isolation technology, no `adapter/docker` and no `adapter/e2b` (separate changes). No port growth: `Sandbox` and `Workspace` keep their exact method sets. No second workspace handle for `PerCall`. No generic scheduler, retry or fallback framework. No new egress, secret, output, limit or host-runner behavior; those ship from governance. No snapshot format of the harness's own: the ref stays opaque. No conflicting output redaction: sanitization stays with `protect-tool-output-storage` (`std/sandbox/secrets.go` scans results only). No cleanup of a resource owned by a dead process; see the Admission section.

## Admission

One material prerequisite is unresolved, so this package is not admitted for implementation as a whole:

- **No installed sandbox provider.** The adapter modules are `adapter/eino`, `adapter/postgres`, `adapter/openai`, `adapter/anthropic` and `adapter/otel`; none declares a sandbox provider. Nothing in the repository establishes that a snapshot survives `Workspace.Close`, how long a snapshot is retained, what a missing snapshot returns, or how a resource is closed by identity after its owning process is gone. Cross-pod restore acceptance needs the first three. Killed-pod cleanup needs the fourth.

Killed-pod remote cleanup is a requested acceptance case. It stays open under task 6.5 and is not an accepted scope exclusion. The package cannot close the gap until a provider contract supplies durable enumeration and close by resource identity, so the case remains unmet and is reported as such in the lane acceptance report.

The second prerequisite is resolved. The atomic owner check is no longer an open design question: D10 fixes the memory lock and guard protocol and D11 fixes the PostgreSQL session-then-run transaction. Both implementations implement that design; neither invents a second protocol.

The local proofs (D9) do not depend on the provider beyond an in-memory fake adapter.

## Decisions

### D1 — The coordinator implementation belongs in `std/sandbox`; core holds registration and lifecycle hooks

`core` cannot import `std` (`.golangci.yml:11-37`), and the coordinator holds the adapter and the policy, which are `std`-side values. Contracts therefore live in the floor and the implementation in `std`:

```go
// core/types/sandbox.go — proposed, all identifiers new
// WorkspaceOwner fences a live handle to one run and one lease generation.
type WorkspaceOwner struct {
	RunID           string
	LeaseGeneration uint64
}

// WorkspaceSnapshot is the durable outcome of one publication.
type WorkspaceSnapshot struct {
	Ref        WorkspaceRef
	Generation int64
}

// SandboxHandles is the optional lifecycle coordinator beside the Sandbox
// port. It owns one live Workspace per (session, owner) and never grows
// Sandbox or Workspace.
type SandboxHandles interface {
	Acquire(ctx context.Context, sessionID string, owner WorkspaceOwner) (Workspace, error)
	Publish(ctx context.Context, sessionID string, owner WorkspaceOwner) (WorkspaceSnapshot, error)
	Release(ctx context.Context, sessionID string, owner WorkspaceOwner) error
	Sweep(ctx context.Context, limit int) error
}
```

`SandboxHandles` MUST expose exactly `Acquire`, `Publish`, `Release` and `Sweep`. There is no `Withdraw` operation and no withdrawal milestone in this design. `Publish` MUST durably publish an owner-fenced snapshot without releasing the live handle or withdrawing the lease. A successful `Publish` MUST NOT be read as quiescence: it is a durable publication, not a drain, a revocation or a teardown. `Release` runs only after the suspension transition or the terminal write succeeds. `types.ErrSignalsPending` retains the live handle and its heartbeat and issues no `Release`. A failed close retains the durable snapshot and the cleanup record. This ruling closes R4; D4 and D7 keep the ordering they already fix.

`Build` receives one already-constructed coordinator and passes the same instance to both callers:

```go
// core/build_options.go — proposed
type SandboxRegistration struct {
	Handles types.SandboxHandles
	Policy  types.SandboxPolicy
}

func WithSandboxRegistration(reg SandboxRegistration) Option
```

The return type is `Option`, the type the existing option family uses (`core/build_options.go:17-19` declares `type Option func(*config) error`). A `BuildOption` identifier does not exist in the package, so no new option type is declared.

`std/sandbox` constructs the coordinator from the adapter, the policy and the metadata surface, then hands the instance to `New`, so the tool set and the lifecycle hooks share one coordinator. `core` holds the registered instance for `suspend`, `finishRun` and `Recover`. `core` performs no adapter and no policy implementation. `WithSandboxRegistration` is a proposed `Option`; the existing option family in `core/build_options.go` is the pattern it follows.

### D2 — Retained, owner-fenced handles replace per-call fresh handles

`withWorkspace` (`std/sandbox/percall.go:16-31`) branches on `p.Lifecycle`. `PerCall` keeps today's Open → body → once-close path unchanged. `PerSession`:

1. Resolve `SandboxHandles` from the run context; absent, refuse with `types.ErrPerSessionUnsupported`.
2. `Acquire(ctx, sessionID, WorkspaceOwner{RunID, LeaseGeneration})`. If this owner already holds the handle, return the same handle. Otherwise open from the stored snapshot ref, record the owner and the handle, and register it. A second run owner in the same session is refused by the session lease, so the handle has exactly one owner.
3. Resolve secrets and run the body as `PerCall` does, so secret handling is identical across lifecycles.
4. Publish after every body invocation that may mutate (D6). Read-only calls publish nothing.
5. Do not close. The handle is released after the suspension storage or after the terminal write (D4).

### D3 — The durable snapshot is fenced by generation and validated by owner in one atomic write (D10 fixes the memory protocol, D11 the PostgreSQL protocol)

`SessionStateMeta` (`core/stores/session.go:120-127`) is the precedent. This change declares a sibling optional surface:

```go
// core/stores/session.go — proposed
// WorkspaceMeta is the durable per-session workspace pointer. Generation is
// the persisted compare-and-set token; zero denotes absent metadata.
type WorkspaceMeta struct {
	Ref        types.WorkspaceRef
	Generation int64
	Owner      types.WorkspaceOwner
}

type SessionWorkspaceMeta interface {
	WorkspaceMeta(ctx context.Context, sessionID string) (WorkspaceMeta, bool, error)
	SetWorkspaceMeta(ctx context.Context, sessionID string, expectedGeneration int64, m WorkspaceMeta) (int64, error)
}
```

There is no second token. `Generation` is the sole compare-and-set token, so the store does not mint a version the caller cannot see. `SetWorkspaceMeta` compares `expectedGeneration` and validates `m.Owner.LeaseGeneration` against the current lease generation in one atomic step. `Publish` writes `expectedGeneration + 1`. A compare-and-set loser returns an error; it never returns another owner's snapshot as its own, and there is no rebase and no successful-retry language. Restore rules:

- A restore that presents metadata equal to the stored metadata issues no write. The restore caller decides this, comparing the stored `Ref` and `Generation` from `WorkspaceMeta` against the checkpoint's and never calling `SetWorkspaceMeta` on equality; the store reports no equality outcome. A stored generation presented with a different ref is a token mismatch and is refused with `types.ErrVersionConflict`.
- A checkpoint older than the stored generation leaves the stored snapshot in place and records the skipped restore. A zero-valued checkpoint written before this change is older than every stored generation, so it never discards a newer session snapshot.
- A checkpoint newer than the stored generation MUST have a matching durable publication, and the restore fails without one.
- Fresh open applies only when both conditions hold: no durable workspace metadata is stored for the session, and the checkpoint carries no ref. An absent metadata record and an empty checkpoint ref are each necessary and neither is sufficient alone. A stored ref of any generation blocks a fresh open even when the checkpoint is empty, because the stored snapshot is the newer durable record.
- Generation zero with a nonempty legacy ref requires an explicit `ImportWorkspaceMeta` call (D12) into absent metadata.

`MemorySessionLog` implements the surface beside `SharedStateMeta`/`SetSharedStateMeta` (`core/stores/session_memory.go:747-748,836-838`) under the lock protocol of D10. `adapter/postgres` implements it on its concrete session store under the transaction protocol of D11. `stores.Checkpoint` gains one additive field at apply time, `WorkspaceGeneration int64`, so a resume can compare generations; a checkpoint written before this change reads zero.

#### Refusal precedence

One order settles every `SetWorkspaceMeta` refusal, and both stores apply the same four steps in the same order:

1. **Authorize** the caller through the existing session-write check. An unauthorized caller is refused before any generation or owner read, so an unauthorized write never learns whether a token or a lease is current.
2. **Compare the workspace compare-and-set token.** A stored generation different from `expectedGeneration` returns `types.ErrVersionConflict` and writes nothing.
   Also compare the incoming metadata with the stored metadata when a record exists. If `m.Generation` equals the stored generation but `m.Ref` differs from the stored ref, return `types.ErrVersionConflict` and write nothing. Perform this check before validating the lease owner proof. Equality of `expectedGeneration` with the stored token alone does not trigger this refusal. A new publication at `expectedGeneration + 1` may carry a different ref.
3. **Validate the lease owner proof.** A proof failing any of the four D10 checks returns `types.ErrRunNotActive` and writes nothing.
4. **Assign** the metadata under the lock and return `expectedGeneration + 1`.

Precedence is observable, not merely internal: a caller presenting both a stale token and a stale owner receives the token refusal (`ErrVersionConflict`), because step 2 precedes step 3. The step order changes no atomicity claim. The memory store still holds `MemorySessionLog.mu` and `MemoryRuns.mu` across the authorize, compare, validate and assign, as D10 specifies; PostgreSQL still locks `session_record` before `run_record` and commits without releasing either, as D11 specifies.

### D4 — Suspension publishes before the save and releases after the suspension transition; the terminal path publishes before the terminal write

Ordering is the whole correctness claim, so it is fixed:

- **Suspension** (`Lifecycle.suspend`, `core/drive_lifecycle.go:726-741`, `r.Save` at `:730`): `Publish` → `r.Save` with the returned ref and generation in `Checkpoint.Workspace` and `Checkpoint.WorkspaceGeneration` → the run's suspension transition → `Release` → `Suspended`. A run with no acquired workspace performs no snapshot. A snapshot or compare-and-set failure produces no successful suspension, and no checkpoint is saved. When `Close` fails during the release, the durable snapshot stays and a cleanup record remains for the next sweep.
- **Terminal** (a completed, failed or detached run): `Publish` → the terminal write → `Release` → the terminal event. A detached run uses this same order and creates no checkpoint. A terminal write refused with `ErrSignalsPending` (`core/types/errors.go:37`) preserves the live handle and the heartbeat, and no release runs. A terminal `Publish` failure records `Failed` without a successful result ref; it does not invent an uncertain-workspace `RunState`, and it does not repeat the failing `Publish` while recording the failure.

### D5 — Resume and recovery restore only from a generation not older than the stored one

`Resume` consumes the token and re-drives the checkpoint (`core/resume.go:459`); `Recover` reclaims a stale run (`core/recover.go:22-31`). Both read the `stores.Checkpoint` they already hold. Before the run re-drives, beside the credential re-issue (`core/recover.go:610`), the restore runs under the D3 rules. The restore runs before any tool executes, so it cannot race a tool call.

### D6 — A mutating call publishes after the body and settles failure as an explicit unknown result

A sandbox call is journaled like any tool call: `Complete` records the `types.ToolResult` (`core/stores/journal.go:56-61`) and a replay answers from it (`:13-19`). A `Publish` failure is not an ordinary tool error, because the mutation may already have happened inside the workspace. The `PerSession` path therefore returns an explicit `types.ToolResult` with `Outcome: types.Unknown` and `Error.Kind: types.OutcomeUnknown` (`core/tool_call.go:76-85`), and the existing execution path records the uncertainty under the original `CallKey` through its one governance and journal path (`core/tool_call.go:144-181`). No second governance or journal path is added. The sandbox effect derivation (`std/sandbox/effect.go:16-21`) is unchanged.

The publication attempt covers every body invocation that may mutate, not only a clean success: a nonzero execution exit and a partial-write error still attempt the publication, because the mutation may be durable in the workspace even though the call reports failure. Read-only calls (`read_file`, `list_files`) publish nothing. Any error message or output the path records is sanitized before persistence, by the owner of `protect-tool-output-storage`.

### D7 — Release closes the acquired handle; the sweep is bounded and locally sourced

`Release` closes the exact `Workspace` that `Acquire` returned from `Open` (`core/types/sandbox.go:77-85`). The coordinator never opens a workspace in order to close one: a fresh `Open` restores from a snapshot and creates a new resource, so closing it cleans nothing. A successful close removes the live record and leaves the durable snapshot reference untouched. A failed close leaves a cleanup record and the durable snapshot, so a later sweep retries.

Claiming a record for cleanup and acquiring a live handle for use are mutually exclusive, so a sweep cannot close a handle a live run is using. A cleanup record carries the lease generation and the resource identity of the record it describes, so a stale claim cannot act on a new owner's handle. `Sweep(ctx, limit)` takes a positive limit and processes at most that many records. `Stack.Recover` invokes `Sweep` even when `Runs.Stale` returns no rows (`core/recover.go:37-58`), because cleanup records outlive a run's stale listing.

Remote cleanup of a resource whose owner process is absent is a requested acceptance case that stays unmet until a provider supplies durable enumeration and close by resource identity (Admission, task 6.5). A process-local live map cannot enumerate resources from a dead pod, so no implementation in this package can close that gap today.

### D8 — Admission, contract edits and the frozen port

`Build` (`core/build.go:157`) MUST fail when a registered policy declares `PerSession` and the build wires no coordinator or no `SessionWorkspaceMeta` (`sandbox.build-refuses-incapable-persession`), returning no tool set. The `sandbox` port and its value types are unchanged, so the frozen Contract code block does not move: the ADR that settles the capability is ADR-0129 (`openspec/specs/sandbox/spec.md:3`) and the sandboxed-execution ADR is ADR-0093. Two contract edits land at apply time under one new ADR, whose number is decided then: the `sandbox` main-spec `Lifecycle and resume` prose correction from D4 and D5, and the `stores` optional-interface entry for `SessionWorkspaceMeta` beside `SessionIndex` (`openspec/specs/stores/spec.md:252-255`).

The `sandbox` correction is two edits that MUST land together in one apply, because the Contract summary and the requirement prose disagree today and either edit alone leaves a wrong statement in the main spec. The Contract `Lifecycle` bullet (`openspec/specs/sandbox/spec.md:71`) says the harness "stores the ref in `Checkpoint.Workspace`" at the end of a detached run, and the `Lifecycle and resume` requirement (the change delta at `openspec/changes/m2-sandbox-lifecycle-resume/specs/sandbox/spec.md:13`) is corrected to state that a detached end publishes the ref as the session's durable workspace metadata and creates no checkpoint, that the restore is fenced on the stored generation and the owner, and that the handle is released after the terminal write. Correcting only the requirement leaves the Contract bullet claiming checkpoint storage on a path that writes no checkpoint. Both files are main specs, so neither is edited while authoring; this entry records the required edit and its acceptance shape, and task 7.3 carries it.

The scenario registry, the Contract edits and the type additions land at apply time. `docs/design/types.md` regenerates to index `SandboxHandles`, `WorkspaceOwner`, `WorkspaceSnapshot`, `WorkspaceMeta`, `SessionWorkspaceOwnerGuard`, `ImportWorkspaceMeta` and `Checkpoint.WorkspaceGeneration`; the `Workspace` and `WorkspaceRef` index entries are unchanged. The ADR also records the memory lock order of D10, the PostgreSQL row-lock order of D11, the explicit import of D12, and the unresolved provider blocker. No ADR number is reserved during authoring.

### D9 — Public-path proofs

The owned scenarios are subtests named exactly their ids, driven through the public `Build`/`Send`/`Resume` path with an in-package fake `Sandbox` that records `Open`, `Exec`, `Snapshot` and `Close`, and a shared snapshot backend. The fake backend outlives the handle close, so a snapshot recorded before a close is still readable after it.

- `sandbox.per-session-persists` (`std/sandbox`) — turn 1 writes `data.csv`; the terminal publication stores a snapshot; turn 2's `Acquire` restores that snapshot and the read returns the file without a re-upload.
- `sandbox.snapshot-on-suspend` (`core`) — a `PerSession` run that wrote files suspends for `HumanApproval`; the saved checkpoint holds the `Snapshot`-produced ref and its generation.
- `sandbox.resume-restores` (`core`) — resuming opens from `Checkpoint.Workspace` and the workspace reports the file written before suspension.
- `sandbox.replay-no-reexec` (`core`) — a run whose `run_code` completed before suspending replays the journaled `ExecResult`; the fake `Exec` count does not increase.
- The cross-pod cases build two stacks with independent coordinator instances and independent adapter instances over shared durable metadata and one snapshot backend. Pod A writes, snapshots and closes its own handle. Pod B restores the ref and reports pod A's file. A missing or expired snapshot returns an error and opens no fresh workspace. A cleanup case starts from a record whose owner process is absent, recovery reaches cleanup with zero stale running rows, and a live lease prevents cleanup. No test seeds metadata through private field writes; every case writes metadata through `SetWorkspaceMeta` on a store instance and reads it back through `WorkspaceMeta`.
- The import cases drive `ImportWorkspaceMeta` (D12) on both stores: one case seeds a session through the public append path, takes the owner through `Runs.Start`, imports a generation-zero snapshot, and reads back generation one with the same ref. The refusal cases present an unauthorized principal, a stale owner, a nonzero input generation, an empty ref and existing metadata, and each case asserts the stored metadata is unchanged.

### D10 — The memory store validates the owner under one mutex it keeps through the write

`MemorySessionLog` holds its own mutex (`core/stores/session_memory.go:64-66`) and `MemoryRuns` holds a second (`core/stores/runs.go:142`). The owner proof lives in the run record, so validating it under the session mutex alone admits a stale owner: `Delete` already proves this shape, because it holds `MemorySessionLog.mu` while `SessionLeaseActive` takes `MemoryRuns.mu` (`core/stores/session_memory.go:416-418,437-446`). `SetWorkspaceMeta` repeats the pattern with a full proof instead of a boolean.

```go
// core/stores/session_memory.go — proposed
// SessionWorkspaceOwnerGuard validates a write owner while its own lock is
// held. The callback runs inside that lock, so it MUST NOT call back into
// either store.
type SessionWorkspaceOwnerGuard interface {
	WithWorkspaceOwner(ctx context.Context, sessionID string, owner types.WorkspaceOwner, write func() error) error
}

// WithSessionWorkspaceOwnerGuard wires the guard. Wire the same MemoryRuns
// instance the stack uses.
func WithSessionWorkspaceOwnerGuard(guard SessionWorkspaceOwnerGuard) MemorySessionOption
```

The protocol, in order:

1. `SetWorkspaceMeta` takes `MemorySessionLog.mu` and reads the caller authority through the existing `scopeSessionWrite` check (`core/stores/session_memory.go:25-30`).
2. It reads the stored generation and compares it with `expectedGeneration`. A mismatch returns `types.ErrVersionConflict` (`core/types/errors.go:40`) and issues no write.
3. It calls `guard.WithWorkspaceOwner` and lets the guard run the write under its own lock. An absent guard refuses every write.
4. The guard validates the proof against `MemoryRuns`, then calls `write` with both locks held.

The proof requires all four checks against the run record (`core/stores/runs.go:435-459`, `core/stores/runs_leases.go:13-57`): `onSess[sessionID] == owner.RunID`, the run's `SessionID` equals `sessionID`, `owner.LeaseGeneration` equals the stored lease generation and is nonzero, and the record is live and unexpired against the store clock. Each condition maps to exactly one error, and no condition is allowed to report the other's sentinel: a lease-owner proof that fails any of the four checks returns `types.ErrRunNotActive` (`core/types/errors.go:34`), because the caller holds no live ownership; a workspace compare-and-set token whose stored value differs from `expectedGeneration` returns `types.ErrVersionConflict` (`core/types/errors.go:40`), because the caller's token is stale rather than its ownership. The two sentinels are disjoint: the owner proof never answers with `ErrVersionConflict` and the workspace token never answers with `ErrRunNotActive`.

The callback MUST NOT take either mutex, call another store, or call a provider or caller callback. It only assigns the metadata fields.

The lock order is fixed and one-directional: `MemorySessionLog.mu` precedes `MemoryRuns.mu`. `Delete` already holds the session mutex while `SessionLeaseActive` takes the runs mutex, so the reverse acquisition is never introduced. The consequence is recorded as a risk: a caller-provided principal or dependency extractor MUST NOT re-enter the session store while a run mutation holds the runs mutex.

### D11 — PostgreSQL validates the owner under session-then-run row locks in one transaction

`Start` takes the advisory session lock first and then reads the newest run row (`adapter/postgres/runs.go:156-207`); `Heartbeat` and `Reclaim` each lock only `run_record` (`adapter/postgres/runs.go:224-264`, `:393-433`); `getSession` locks the session row for update (`adapter/postgres/session.go:125-140`). `SetWorkspaceMeta` follows that order:

1. One transaction. Take the existing session serialization lock (`lockSession`, `adapter/postgres/runs.go:150-155`).
2. Lock `session_record` for the session.
3. Lock the owning `run_record` last.
4. Authorize the session write through the existing access check, then compare the stored workspace generation with `expectedGeneration`; a mismatch returns `types.ErrVersionConflict` and writes nothing.
5. Validate session identity, run identity, current generation, active state and lease expiry under the row locks; a failed proof returns `types.ErrRunNotActive` and writes nothing.
6. Write `expectedGeneration+1` and commit without releasing the owner row lock.

Steps 4 and 5 are the D3 refusal precedence, in the same order the memory store applies: authorize, compare the workspace token, validate the owner, assign. Both stores therefore answer a request carrying a stale token and a stale owner with `ErrVersionConflict`.

Because `Heartbeat` and `Reclaim` lock `run_record`, neither can change the validated lease between the validation and the commit. Any operation that needs both rows uses the session-before-run order. A run-only mutation keeps run-only locking.

### D12 — A legacy workspace ref is imported by an explicit store operation

D3's restore rules need one named operation, because `SetWorkspaceMeta` cannot express "generation zero in, generation one out" without a caller that already knows the target generation.

```go
// core/stores/session.go — proposed, on the optional SessionWorkspaceMeta surface
ImportWorkspaceMeta(ctx context.Context, sessionID string, owner types.WorkspaceOwner, legacy types.WorkspaceSnapshot) (types.WorkspaceSnapshot, error)
```

`MemorySessionLog` and the PostgreSQL session store both implement it. It requires the existing session-write authority and a current owner lease. It never accepts a caller-supplied owner without store validation. The preconditions are `legacy.Generation == 0`, a nonempty `legacy.Ref`, and absent durable workspace metadata. The effect is one atomic write of generation one carrying the supplied ref and the validated owner, and the returned snapshot is that persisted value. The refusals are an unauthorized caller, a stale owner, a nonzero input generation, an empty ref, and existing metadata. Every refusal leaves the store unchanged.

`Resume` and `Recover` never call the operation implicitly. A session whose only ref is a legacy one refuses to restore until an authorized caller imports it. The tests seed the session through the public append path and the owner through `Runs.Start`, then import through `ImportWorkspaceMeta`. No test writes private fields.

## Open items

Four review findings were raised. Two are closed below by the rulings recorded here (R3, R4), one is closed by the D3 result shape (R7), and R6 stays OPEN and blocking. Each OPEN item states its boundary and names no mechanism, because the design does not settle it and a guessed mechanism would be a new decision without evidence. No OPEN item is claimed resolved by a task that merely touches the same code.

- **R3 — Post-transition write authority. CLOSED BY STORE GROUNDING.** The design previously asserted that a post-transition publication might or might not satisfy the owner proof. That assertion is falsified by the memory store. `Suspend` writes the transition and drops the lease in one locked store call: `rec.run.State = Suspended`, `rec.token = t`, `rec.lease = Lease{}`, `rec.live = false` (`core/stores/runs_notices.go:136-152`); `Finish` performs the identical teardown (`core/stores/runs_notices.go:92-114`). `heldLocked` requires `rec.lease.Generation == l.Generation`, an unexpired `Expires` and `rec.live` (`core/stores/runs.go:478-488`), so after either transition any presented `Lease` fails all three checks with `types.ErrRunNotActive`. Two cases follow, and they are authorized differently:

  - **Exact-record teardown.** The run that owns the handle closes it after the transition. That close is not a store operation: it is provider-side `Close` on the already-returned `Workspace`. No store authority is needed for it, and none is available — a `Release` presenting the suspended run's lease could not pass `heldLocked` either. Any design demanding an owner proof for this post-transition release would always fail, so none does.
  - **Live publication.** A successor publishing writes under the fresh lease from `Resuming` (`core/stores/runs.go:410-431`) or `Reclaim` (`core/stores/runs_leases.go:34-58`). Generations are store-minted and strictly increasing (`core/stores/runs.go:281-286`; minted at `:311-323` and `:420-427`, and by `Reclaim` at `core/stores/runs_leases.go:47-54`), so the successor's generation differs from the suspended run's. That generation fence is the only publication authority in this repository; no separate publish token exists.

  `Heartbeat` preserves the presented generation and refreshes only `Expires` (`core/stores/runs_leases.go:23`), and a stale-generation probe with a refreshed expiry is still refused (`core/stores/runs_leases_test.go:46-53`). D4 and D10/D11 are unaffected: publish-before-save and publish-before-terminal-write already order every store write ahead of the transition, so no `Publish` in this design runs after the lease is gone.
- **R4 — Withdraw quiescence. CLOSED BY DESIGN RULING.** The quiescence question had no defined target in this repository, so the design removes the target instead of inventing one. `SandboxHandles` exposes exactly `Acquire`, `Publish`, `Release` and `Sweep` (D1); there is no `Withdraw` operation, no withdrawal milestone, and no quiescence claim. `Publish` publishes an owner-fenced snapshot and releases nothing; success is never read as quiescence. D4 and D7 keep the ordering they already fix.
- **R6 - Provider admission and task 6.5. PARKED, OPEN, and blocking.** No installed sandbox provider establishes the required snapshot and resource lifecycle contract. PostgreSQL bytea storage exists (`adapter/postgres/migrations/00001_baseline.sql:73-74`, `adapter/postgres/outputs.go:40-84`). It provides durable bytes, not sandbox execution or durable enumeration and close of the original resource after owner death. Snapshot survival after `Workspace.Close`, retention, and missing-snapshot errors remain unproved. The Docker socket and group access do not establish daemon liveness, which remains [UNOBSERVED]. Current CI provides no Docker-service evidence and does not run `task test:full` (SubstrateRewrite scout evidence, 2026-10-11). Task 6.5 stays unchecked and remains unmet, not an accepted exclusion. This lane adds no isolation technology, host-exec substitute, or harness snapshot format (`design.md:20`). Reopen R6 when `m2-docker-sandbox` delivers a provider contract and real evidence for snapshot survival, retention, missing-snapshot errors, durable enumeration, and close by resource identity after owner death. Docker admission also requires a successful bounded daemon ping and CI execution against a provisioned daemon. The cleanup proof must close the original resource, protect live ownership, and run recovery with zero stale runs. Preserve frozen waves 1-4 and record the provider decision in the next unreserved ADR at apply time.
- **R7 — Equal-generation refusal and its confirmation source. CLOSED BY THE D3 RESULT SHAPE.** The question had two halves, and each now has a component. First, the confirming component is the restore caller, not the store: a restore performs the read it already performs (`WorkspaceMeta(ctx, sessionID) (WorkspaceMeta, bool, error)`), compares the returned `Ref` and `Generation` against the checkpoint's, and issues no `SetWorkspaceMeta` call when they are equal. The equal-metadata case is therefore a silent no-write decided before any write path runs, and the store never has to report "no write needed" as an outcome. Second, if a caller does present a stored generation with a different ref, the store refuses loudly: the generation compare in the refusal precedence (step 2) matches the token, but the payload comparison fails, and the call returns `types.ErrVersionConflict` having written nothing. The generation check alone is not sufficient to admit a write.

  The result shape is unchanged from what D3 already declares and adds no symbol: `SetWorkspaceMeta(ctx, sessionID, expectedGeneration int64, m WorkspaceMeta) (int64, error)`, the same two-value compare-and-set result `SetSharedStateMeta` returns (`core/stores/session.go:121-127`; the memory implementations at `core/stores/session_memory.go:905-926` and `:994-1010` return the advanced version and wrap `types.ErrVersionConflict` on a mismatch). The ref comparison is a rule inside that method, not a new method, a new option, or a new return value.

## Risks / Trade-offs

- [Risk] A new coordinator port and a new optional store surface touch the tier the freeze watches. → Accepted: the sandbox port itself does not change, optional surfaces are the declared growth path (`SessionStateMeta`), and a build without a coordinator keeps today's `ErrPerSessionUnsupported` refusal.
- [Risk] The coordinator implementation in `std/sandbox` means core reaches a `std` behavior through one port instance. → Accepted and bounded: core holds the registered `types.SandboxHandles` value only, depguard keeps the import out (`core/drive_lifecycle.go:700-763` is the seam), and `Build` fails a `PerSession` policy with no registration.
- [Risk] Publishing after every mutating body adds a snapshot to the call's latency. → Accepted and scoped: only bodies that may mutate publish, and durability before a replayable result is the correctness requirement.
- [Risk] An atomic owner check is stricter than a compare-and-set and can reject a publication a run believes it owns. → Accepted: the lease generation is store-minted, so a rejection names a real fence, and the run records the failure rather than retrying.
- [Risk] The memory owner check holds two mutexes across a callback, so a caller-supplied extractor that re-enters the session store while the runs mutex is held deadlocks. → Accepted and constrained: the lock order is `MemorySessionLog.mu` before `MemoryRuns.mu`, the callback only assigns metadata fields, and the constraint is stated in D10 and verified by task 2.4.
- [Risk] Killed-pod cleanup is requested but not delivered. → Open blocker, not an accepted exclusion (Admission): the case needs a provider that keeps durable records and closes by resource identity. Task 6.5 stays unstarted and the lane report names the case unmet.
- [Risk] The PostgreSQL owner check adds a run row lock to every workspace publication. → Accepted and bounded: only `run_record` rows for the session's current run are locked, and `Heartbeat` and `Reclaim` already lock that row, so no new lock class appears.
- [Trade-off] The durable snapshot is a second session-scoped pointer beside shared state. → Accepted: the Contract rule names a stored ref, and generation plus owner fencing is what prevents the overwrite the audit found.

## Migration Plan

`stores.Checkpoint` gains one additive field (`WorkspaceGeneration`) at apply time; a checkpoint written before this change reads it as zero, which restores only a session with no stored snapshot (D5), so an in-flight old checkpoint resumes without clobbering a newer one. No data migration and no removal: `Checkpoint.Workspace` keeps its shape. A session with only a legacy ref is not migrated: the operator imports it through `ImportWorkspaceMeta` (D12) while it holds a live run lease. `core/types` gains the coordinator port and its owner and snapshot value types; `core/stores` gains the optional `SessionWorkspaceMeta`, its `ImportWorkspaceMeta` operation and the `SessionWorkspaceOwnerGuard` seam with the `MemorySessionLog` and `adapter/postgres` implementations; `std/sandbox` gains the coordinator implementation, the coordinator construction shared with `New`, and the `PerSession` path in place of the refusal; `core` gains `WithSandboxRegistration`, the `Build` admission, and the `suspend`, `finishRun` and `Recover` hooks. The `sandbox` main-spec correction, the `stores` optional-interface edit, the scenario registry entries, the Contract edits and the new ADR land at apply time (D8), and `docs/design/types.md` regenerates. Shared driver files (`core/build.go`, `core/build_options.go`, `core/drive_lifecycle.go`) follow the milestone's admission-order rule (`openspec/changes/m2/tasks.md:19`): the runtime-composition lane is admitted first and the lifecycle-repair lane follows on the same files. Keep the module pre-v1 and create no release tag while authoring; `m2-api-freeze` runs the port compatibility check and cuts `v1.0.0` at the M2 exit.