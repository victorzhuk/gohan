# Design: m0-hardening

Sealed decisions. Each states the choice, the evidence, and the alternative that was rejected. Implementation chunks must not re-decide these.

## D1 — Durable run identity is minted per run, not per instance

`Conversation.Send` and `Conversation.Continue` mint `RunID` from `crypto/rand` as `run-` plus 32 hex characters, and return the generator error before `Runs.Start` if entropy is unavailable. The process-local counter in `core/conversation.go` (`nextRunID`) is deleted.

Evidence: two `Conversation` instances over one `MemoryRuns` both produced `run-00000001`; the second `Start` replaced the first row, both sessions' `Done` events landed in one ring, and the first run failed with `gohan: session has no active run: run run-00000001` (`core/stores/runs.go:243-247` assigns unconditionally).

Rejected: a per-instance id namespace — a restarted process or a second pod still collides. Minting in `Runs.Start` — the port takes a fully built `Run` and is frozen; changing its signature is a compatibility break for a defect that a collision-resistant value fixes.

## D2 — No store-side duplicate-run guard in this change

`Runs.Start` keeps overwriting by key. With D1 the collision source is gone, and no caller can supply a `RunID`. Adding a rejection now would need a new catalogued sentinel and a store-port contract sentence for a case no scenario requires.

Revisit if a future public seam accepts a caller-supplied run identity.

## D3 — The prefix memo is narrowed to unfiltered builds

`std/assembly.go` keeps the process-global prefix memo, because `performance.prefix-build-allocation-free` requires a repeated build to allocate zero. The memo applies only when `AssembleInput.Filter` is nil; a non-nil filter always rebuilds, and the memo key carries no filter field at all.

Evidence for the defect: three closures from one factory shared a code pointer; with every other key field identical, the second and third `Assemble` calls returned the first call's tool set (`reflect.Value.Pointer()` on a func kind is the code pointer, not the closure). The hit also returned the memoized `ModelRequest`, so callers shared tool backing arrays.

Rule: no filter identity is derived at all. A Go func value exposes none — `reflect.Value.Pointer` and `reflect.Value.UnsafePointer` both return the code pointer for kind `Func`, so any filter key would be a guess. A filtered build is per-turn anyway, so it rebuilds. The memo's precondition is recorded in its comment: a hit requires the caller's slices and provider map to be unchanged since the miss, so callers treat the request and the inputs as read-only for the run's life.

Evidence that the narrowed memo still satisfies the scenario: `core/assembly_benchmark_test.go` asserts `repeat prefix build = 0 allocs/run` and its fixture sets no filter.

Rejected: deleting the memo — it fails a normative performance scenario (measured 9 allocs/run). Keying on the closure data block — no such accessor exists for func values. Memoizing filtered builds — the filtered set is per-turn, and its identity is exactly what cannot be observed.

Rejected: caching only the slot-static prefix — it does not satisfy the scenario, which measures the whole unfiltered build.

## D4 — Cancel authorizes the session owner before it signals

`Conversation.Cancel` performs the same owner check every other session-mutating seam performs (owner tenant, and subject or the session-write scope) before it resolves the run and before `Runs.Signal`. A foreign session returns the forbidden error; no signal is posted.

Evidence: `Cancel` called only `requirePrincipal`, then `find` (which resolves through the shared live map or `SessionRunFinder`) and `Runs.Signal` (`core/conversation.go:247-256`); `Steer` has an ownership check and `Cancel` did not.

Rejected: relying on `SessionRunFinder` — it answers by session id, not by owner, so it cannot distinguish a caller.

## D5 — A steer is persisted before it is acknowledged

`Conversation.stream` binds the session appender, exactly as `Conversation.Resume` already does, and signal processing returns the advanced `runtime.State` so the step that follows sees the incremented `HistoryVersion`. `SteerApplied` is emitted only after a successful append. With no appender bound the steer is still applied to the state and included in the next assembly, no `SteerApplied` is emitted, and the step continues: a steer is never lost, and an acknowledgement never claims a persistence that did not happen.

Evidence for the revision: failing the step when no appender is bound broke `TestRuntimeLifecycle/max_turns_reached_ends_with_stop_limit`, a drive-only path that has no session to persist to.

Evidence: `stream` built its `Lifecycle` without `WithLifecycleAppender` while `Resume` included it; `applySignals` emitted `SteerApplied` with a nil appender and updated a `State` that was passed by value.

Rejected: acknowledging optimistically — the flow contract states a steer is never lost.

## D6 — Resumed events are recorded before delivery

`Conversation.Resume` records every event through the same `relay` path the initial stream uses, preserving run id and sequence, and marks the run ended so `Attach` waiters wake.

Evidence: `Resume` yielded `DriveLifecycle` events directly while `stream` relayed each event through `c.relay` (`core/conversation.go`), so a resumed run's events never reached `EventLog`.

Rejected: a separate recorder for the resume path — two ordering paths recreate the defect at the next change.

## D7 — Recovery derives authority from stored state, never from the reaper

Recovery takes the driver principal from the run's stored state: the checkpoint `Originator` when a checkpoint exists, otherwise the session log owner (tenant and subject) when the session has a row. When neither is available, no principal is set at all: governance then denies authority-sensitive work instead of inheriting the reaper's. The reaper's ambient principal is never used, and the credential source is consulted only once a principal has been resolved. Credential resolution happens before any model or tool work, and its error abandons the run as `Failed`; it is not discarded. `RunInfo.Principal` is populated from the same value.

Evidence: `recoveryContext` restored the principal only when a checkpoint was present, discarded `CredentialSource` errors, and left `RunInfo` without the principal.

Rejected: the ambient principal (this is the defect); restoring limits only (authority, not just budget, is what the recovery contract requires).

## D8 — A recovered run is fully wired and can suspend again

`driveRecovered` builds the complete `runtime.AgentRun` (with `Save` bound to `Checkpoints.Put`) and the `Lifecycle` options the initial path uses — session, flow, originator, approval policy, tool specs. A missing required dependency abandons the run with an explicit error instead of driving it.

Evidence: `driveRecovered` passed `runtime.AgentRun{}` with no `Save`, while `Lifecycle.suspend` calls `r.Save` unconditionally.

## D9 — A consumed preempted checkpoint is recoverable (ADR-0146)

`recoverConsumed` no longer skips `Preempted` unconditionally. The run lease is the arbiter: `Runs.Resuming` decides between client and reaper, so a preempted checkpoint whose client died between `Consume` and `Resuming` is recovered with its recorded input, while a live client resume still wins.

Evidence: `recoverPreemptedRun` returned early when a client decision was recorded, and `recoverConsumed` skipped every consumed `Preempted` checkpoint, so a run in that state was reachable by neither pass.

This is the one decision in this change that alters a contract; it is recorded in ADR-0146 and in the recovery spec, and it adds the scenario `recovery.consumed-preempted-is-recoverable`.

Rejected: leaving it unrecoverable and documenting a manual repair — recovery is the only path back for a headless run.

## D10 — The gates that certify M0 must be able to fail

- `tools/spec_coverage.py` counts a scenario covered only when its subtest ran and did not skip or fail; a `run` followed by a `skip` no longer counts.
- `tools/performance_gate.go` requires both `B/op` and `allocs/op` on a gated line; a missing measurement is a failure, not a zero.
- The advisory performance fixture measures at the file's documented 1000-iteration floor, where a one-off allocation rounds to zero.
- `tools/apicheck` normalizes parameter names while continuing to report type, arity, variadic, result and method-set changes.

Evidence: `parse_test_events` yielded only `Action == "run"`; `benchLine` made both allocation fields optional and `parseBench` defaulted them to zero; the advisory subtest ran at `-benchtime 5x` while its own contract says 1000 iterations; the parameter rename `_` → `ctx` was reported as incompatible.

Rejected: moving the `v0.1.0` baseline tag, and relaxing the allocation assertion — both hide real signal.

## D11 — Journal fingerprint mismatch fails the call (validation first)

`MemoryJournal.Reserve` must return the existing entry only when the entry's fingerprint matches the one passed; a mismatch fails the call rather than inheriting another call's outcome. Whether `Complete` may keep its documented drop-on-expire no-op depends on the uncertainty contract and is decided after the tool path is traced; the sentinel used must come from the existing error catalog or be added with a regenerated index.

Evidence: `Reserve` returned the stored entry on key hit without comparing fingerprints (`core/stores/journal_memory.go:37-39`), while the port comment requires the retry to inherit the key only for the same fingerprint.

## Open decisions for the owner

1. **`api:check` policy before v1.** Five of the six reported differences are genuine source-visible changes against `v0.1.0`. Pre-v1 breaking changes are permitted by the compatibility policy, but the gate is wired as a required CI check. Either it becomes advisory until the freeze tag, or the baseline moves.
2. **Core policy ownership.** Executable limit middleware and concrete preset values live in `core` while the core budget rule assigns them to `std`. Moving them changes package ownership and needs its own ADR.
3. **The governed native constructor.** `Build` records middleware that no public execution path reads. Restoring the documented path is a new-pattern design, not a wiring fix, and is designed before it is dispatched.
