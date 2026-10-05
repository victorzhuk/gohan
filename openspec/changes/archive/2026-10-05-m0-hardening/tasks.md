# Tasks: m0-hardening

Scope: the repairs named in `proposal.md`. Decisions are sealed in `design.md`; a chunk implements them and does not re-decide them. The review that produced them is `plan.md`.

## How to run this plan

- A **chunk** is the executable unit: one file cluster, its own tests, one literal verify command. Work chunks in order within a wave.
- A chunk is done when its `verify:` command is green and `task lint` and `task test` are green for the packages it touched.
- Waves are file-disjoint: chunks in one wave may be mid-flight relative to each other, so a cross-chunk compile error is expected. Verify with the chunk's own packages and report a sibling's file, never repair it.
- A subtest that satisfies a scenario is named exactly by its ID (`t.Run("recovery.consumed-preempted-is-recoverable", …)`). Regression tests for behaviour no scenario names use ordinary `TestXxx` names.
- New regression tests go in new files unless a chunk says otherwise. An existing test that pins a defect must be replaced, and the replacement reported.
- `task spec:gate` is red while a newly registered scenario has no subtest; that is expected mid-wave and green at the end of wave 1.

## Wave 1 — authority, identity, reuse, and honest gates

Independent, file-disjoint.

1. [x] `core`: conversation identity, authorization, steer persistence and resumed-event recording. Decisions D1, D4, D5, D6. Verified: `go test -timeout 2m ./core/...` green.
  - files: `core/conversation.go`, `core/conversation_continue.go`, `core/resume.go`, `core/drive_lifecycle.go`, `core/conversation_identity_test.go`, `core/conversation_authorization_test.go`, `core/conversation_steer_persist_test.go`, `core/resume_events_test.go`
  - scenarios: keeps `flow.plain-invoke`, `identity.sessions-owner-checked`, `suspension.token-reuse`, `runtime.preempted-resume-continue`, `streams.monotonic-seq` green
  - verify: `timeout 2m go test -timeout 2m ./core/...`

2. [x] `core`: recovery authority, complete recovered execution, and consumed preempted recovery. Decisions D7, D8, D9. Verified: `go test -timeout 2m ./core/...` green, `recovery.consumed-preempted-is-recoverable` present and passing, `go vet ./core/...` clean.
  - files: `core/recover.go`, `core/recover_preempted.go`, `core/recover_authority_test.go`, `core/recover_preempted_test.go`
  - scenarios: `recovery.consumed-preempted-is-recoverable`, `recovery.pod-dies-mid-turn`, `recovery.no-double-run`, `recovery.preempted-before-stale`, `recovery.headless-recovery-impossible`, `engines.crash-after-consume`
  - verify: `timeout 2m go test -timeout 2m ./core/...`

3. [x] `std`: narrow the assembly prefix memo to unfiltered builds so a caller's filter is always evaluated per turn. Decision D3. Verified: `go test -timeout 2m ./std/...` green and `performance.prefix-build-allocation-free` back to 0 allocs/run.
  - files: `std/assembly.go`, `std/assembly_test.go`
  - scenarios: `assembly.prefix-stability`, `assembly.tool-order`, `assembly.truncate-policy`
  - verify: `timeout 2m go test -timeout 2m ./std/...`

4. [x] `tools`: gates that can fail. Decision D10. **GATE**
  - verify result: `go test -timeout 2m ./core -run '^TestPerformanceGate$'` green (was red), `./tools/apicheck` green with the rename case added, `python3 -m unittest discover -s tools` 27 tests green.
  - files: `tools/spec_coverage.py`, `tools/performance_gate.go`, `core/performance_gate_test.go`, `tools/apicheck/main.go`, `tools/apicheck/main_test.go`
  - scenarios: none
  - verify: `timeout 3m go test -timeout 2m ./core -run '^TestPerformanceGate$' && timeout 3m go test -timeout 2m ./tools/apicheck && timeout 3m python3 -m unittest discover -s tools -p '*_test.py'`

5. [x] `docs`: design the governed native construction path and `Explain`. Decisions: open decision 3. **no code in this chunk**
  - files: `openspec/changes/m0-hardening/design-native-path.md`
  - result: the design names the constructor (`NewNativeConversation` plus a pre-Build native registration), the effect step extracted from the existing governed turn implementation and reached through `DriveLifecycle`, one resolved configuration shared with `Explain`, per-run limit state, prompt accounting, and a chunk split (C01–C09) with dependencies and regressions. It records three blockers: the limits-ownership ADR, the `MaxTurns`/`StopLimit` disagreement between the native and general limit contracts, and registration ordering.

## Wave 2 — journal integrity, then the governed native path

6. [x] `tools`: implement the documented `api:check` exit policy. `docs/design/compatibility.md` states it: CI fails on an incompatible change in any module at `v1` or later and only reports for `v0` modules. The tool currently exits 1 for every module, which is why the floor is red on five genuine pre-v1 changes. Report and exit 0 for a `v0` module, keep failing for `v1`, and keep printing every difference.
  - verify result: `task api:check` exits 0 and still prints the thirteen differences plus "module is pre-v1, reporting incompatible change(s) without failing"; `./tools/apicheck` green with the v0/v1 cases added.
  - files: `tools/apicheck/main.go`, `tools/apicheck/main_test.go`, `docs/design/api-review-m0-5.md`
  - scenarios: none
  - verify: `timeout 3m go test -timeout 2m ./tools/apicheck && timeout 3m task api:check`

7. [x] `core`, `std`: journal key identity, and the decorator that consumes it. Both refusals are sealed by ADR-0149: `Reserve` refuses a key presented with a different fingerprint (`ErrJournalFingerprintMismatch`), `Complete` refuses a key with no live reservation (`ErrJournalCompleteMissed`), the store conformance suite holds every implementation to both, and `std.Journal` stops discarding them — a `Reserve` error fails the call, a `Complete` error marks the result `Outcome: Unknown`. The trace that justified the `Complete` change: `execOne` (`core/runtime/runtime_schedule.go:105-120`) is the only production caller, and it always Reserves first, so only a call outliving the 24 h TTL reaches the miss — where the old no-op reported a clean run over an unrecorded write. Decisions D11, D12.
  - verify result: `go test -timeout 2m ./core/stores/... ./testkit/storetest/... ./std/... ./core/...` green; `stores.reserve-fingerprint-mismatch` and `stores.complete-without-reservation` registered and covered.
  - files: `core/stores/journal.go`, `core/stores/journal_memory.go`, `core/stores/journal_memory_test.go`, `testkit/storetest/`
  - scenarios: keeps `stores.fingerprint-after-compaction`, `tools.effect-once` and the journal suite green
  - verify: `timeout 3m go test -timeout 2m ./core/stores/... ./testkit/storetest/...`

8. [x] `core`, `std`: the governed native path from `design-native-path.md`, landed in waves:
  - **Wave 2a (dispatchable now, C01 sealed by ADR-0150):** C02 extract `modelEffect`/`batchEffect` from `core/drive_turn.go` without changing parsing, repair or truncation; C03 new `core/runtime/native.go` with serializable phase advancement and driver callbacks; C04 `core/chains/limits.go` per-run ledger reached from context, inert until C10.
  - **Wave 2b:** C05 **done** — `NativeSpec` registration and the immutable resolved configuration in `core/build*.go`, `core/native_options.go`, `core/native_resolve.go`; C06 **done** — `core/native_effects.go` binds the extracted effects to the resolved configuration (scheduler, original call identity, control errors, resolved prompt insertion); C18 **done** — one run ledger in `core/chains` reached from the run context, with the `std/limit` policy charging it (the earlier fork of the ledger into `std/limit` would have been inert in production).
  - **Wave 2c:** C07 **done** — `NewNativeConversation` and the invocation factories; C08 **done** — post-batch safe point, restored accounting context and `Done.Cost` in `core/drive_lifecycle.go`; C09 **done** — fresh governed runtimes and ledgers seeded from the persisted record for resume and recovery, after three resume defects surfaced on the way (pending calls were never persisted, the batch effect read calls only from a live turn, and `Resume` snapshotted state before the approval was applied).
  - **Wave 2d:** C10 **done** — executable limit policy in `std/limit` (landed early: it depended only on C04); C11 **done** — captured ledger and captured prompts removed from `std/presets.go`, the set declared through `WithPrompts`, the renderer parameterized in `std/prompts.go`; C12 **done** — recipes read the governed model and the final prompt set from the stack and the private repair literal is gone; C13 **done** — `Explain` projects the resolution execution uses, the startup matrix reports every registered definition, and the release identity hashes every prompt field.
  - **Wave 3:** C14 **done** — the shipped quickstart runs `Build` and the governed constructor; C15 **done** — the excursions example runs the governed path with its own decider, so a denied booking executes nothing and its ordered `not_executed` result reaches the persisted history; C16 **done** — architecture, scenarios and the changelog describe the shipped entry.
  - **Follow-ups:** C19 **done** — the batch-overrun sentinel moved to `core/types` so `core/chains` stops depending on `core/runtime`; C20 **done** — one send now emits one assistant message and one `Done` and appends the reply to the session log; C21 **done** — a native definition carries the tool decider, so a service's verdicts reach the batch gate instead of every side effect asking; C22 **done** — the gate honors a recorded approval on the next drive, so an approved call is not asked again after a resume or a recovery; C23 **done** — a resumed or recovered drive runs with its restored run identity, so the gate's scope check sees a real run; C24 **done** — restoring that identity fails closed when the checkpoint originator carries no scope, and the examples' stale-resume and token-expiry expectations were separated.
  - **C17 dropped:** the `core/chains` limit middleware is the charging primitive the `std/limit` policy calls, not a legacy duplicate. What remains of the ADR-0148 exception is narrower and is recorded there: the default values (`types.InteractiveLimits`, `types.AgenticLimits`, `types.BatchLimits`) are still declared in `core/types`.
  - Gaps C01 still owes before wave 2b/2d: the `ErrBatchOverrun`→`LimitExceededError` translation at the driver boundary (sealed by ADR-0150), what `Explain` may claim once arbitrary caller middleware is in the chain, where the durable accounting snapshot stops, and the sequential-tools option that has no `ModelOptions` field yet.
  - verify: `timeout 5m go test -timeout 2m ./core/... ./std/...`

9. [x] `examples`: the three offline acceptance processes drive the public governed path while keeping all nine `engines.*` assertions; the refund example gains a runnable `main`. The three directories are independent. Depends on 8.
  - landed: all three directories build a Stack, register their flow as a native definition (with the tool decider where the flow denies or asks) and drive a conversation; `kafka-refunds` gained a runnable `main` that exits non-zero on failure. Nine `engines.*` subtests keep their names and intent.
  - files: `examples/{camunda-invoice,temporal-travel,kafka-refunds}/`
  - scenarios: `engines.*` (nine)
  - verify: `timeout 3m go -C examples test -timeout 2m ./... && timeout 2m go -C examples run ./kafka-refunds`

10. [x] `core`: stream ownership and ordering before the protections are enabled on the public path. Decisions sealed by ADR-0152, which also split the row:
  - **10a, ownership and ordering (T1, done):** the lifecycle owns the effect-to-consumer seam - component events are delivered in emission order while the effect runs, step events follow the component emissions that preceded them, every event is recorded before it is delivered, and the model effect no longer emits its own `Done`.
  - **10a, bounded buffering (T2, done):** one FIFO per model call with the sealed default of 64 chunks; a full queue blocks the provider read, nothing is dropped or reordered, the idle deadline is measured on the provider read and never while blocked, a terminal provider error does not wait behind queued chunks, and supervision sits inside the model chain around the provider. Scenario `streams.stream-buffer-bound`.
  - **10a, stall (T3/T4, done):** a run-owned guard fires when no event is taken for `RunLimits.ConsumerStall`; preemption suspends at the safe point with no `Done` and no failure (`streams.consumer-stall-preempts`), detach keeps the run's identity, lease and accounting under a harness-owned context (`streams.consumer-stall-detaches-with-log`). Both run on the single harness-owned run worker of ADR-0152 decision 3.
  - **10b, failure terminal (T5/T6, done):** one terminal indication per channel - a single `(zero, error)` tuple in-process, a recorded `TerminalError` payload durably, `Done` never carrying an error; an ordinary mid-stream failure appends its partial assistant message with `FinishError` while preemption appends nothing; `Attach` replay stops at `Suspended` and at the failure terminal, and a bounded event log refuses a cursor older than its retention instead of serving an overlapping window.
  - **10c, deferred to its own change:** exposing the `EventMeta`/`Seq` envelope through the stream surface and an optional `EventLog` interface, because `stores.EventLog` is frozen at v1 and the transport assertion cannot be proven without it (ADR-0152 decision 7).
  - files: `core/drive.go`, `core/drive_lifecycle.go`, `core/model_stream.go`, `core/stream_stall.go`, `core/attach.go`
  - scenarios: `streams.stream-buffer-bound`, `streams.consumer-stall-preempts`, `streams.consumer-stall-detaches-with-log`, `streams.terminal-error-event`
  - verify: `timeout 5m go test -timeout 2m ./core/...`

11. [x] `docs`: publish completion evidence after 6–10 land: the API-review verdict records the resolved policy and the exercised surface, the changelog carries the wave-2 fixes, and the tasks tally is recomputed.
  - landed: the API-review document records the policy `docs/design/compatibility.md` settled on (report pre-v1, fail from v1), the real `task api:check` verdict with its pre-v1 difference count, and the surface exercised; the changelog carries the native path, the stream protections and the defects this work fixed under Added/Fixed; the tally was recomputed against what the repository contains.
  - files: `docs/design/api-review-m0-5.md`, `CHANGELOG.md`, `openspec/changes/m0-hardening/tasks.md`
  - scenarios: none
  - verify: `timeout 3m task spec && timeout 3m task api:check`
