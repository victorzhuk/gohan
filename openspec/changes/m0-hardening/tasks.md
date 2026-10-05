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
  - verify result: `task api:check` exits 0 and still prints the five differences plus "module is pre-v1, reporting incompatible change(s) without failing"; `./tools/apicheck` green with the v0/v1 cases added.
  - files: `tools/apicheck/main.go`, `tools/apicheck/main_test.go`, `docs/design/api-review-m0-5.md`
  - scenarios: none
  - verify: `timeout 3m go test -timeout 2m ./tools/apicheck && timeout 3m task api:check`

7. [x] `core`, `std`: journal key identity, and the decorator that consumes it. Both refusals are sealed by ADR-0149: `Reserve` refuses a key presented with a different fingerprint (`ErrJournalFingerprintMismatch`), `Complete` refuses a key with no live reservation (`ErrJournalCompleteMissed`), the store conformance suite holds every implementation to both, and `std.Journal` stops discarding them — a `Reserve` error fails the call, a `Complete` error marks the result `Outcome: Unknown`. The trace that justified the `Complete` change: `execOne` (`core/runtime/runtime_schedule.go:105-120`) is the only production caller, and it always Reserves first, so only a call outliving the 24 h TTL reaches the miss — where the old no-op reported a clean run over an unrecorded write. Decisions D11, D12.
  - verify result: `go test -timeout 2m ./core/stores/... ./testkit/storetest/... ./std/... ./core/...` green; `stores.reserve-fingerprint-mismatch` and `stores.complete-without-reservation` registered and covered.
  - files: `core/stores/journal.go`, `core/stores/journal_memory.go`, `core/stores/journal_memory_test.go`, `testkit/storetest/`
  - scenarios: keeps `stores.fingerprint-after-compaction`, `tools.effect-once` and the journal suite green
  - verify: `timeout 3m go test -timeout 2m ./core/stores/... ./testkit/storetest/...`

8. [ ] `core`, `std`: the governed native path from `design-native-path.md` (chunks C01–C09). C01 is sealed: limit termination is ADR-0147, the core budget exception is ADR-0148, and the remaining C01 work is the flow/runtime/build spec statements the native definition needs.
  - files: per `design-native-path.md`
  - scenarios: `build.resolved-matrix`, `chains.prompt-strings-accounted-for`, `runtime.max-turns`, `limits.hard-cost-abort`, `flow.plain-invoke`
  - verify: `timeout 5m go test -timeout 2m ./core/... ./std/...`

9. [ ] `examples`: the three offline acceptance processes drive the public governed path while keeping all nine `engines.*` assertions; the refund example gains a runnable `main`. The three directories are independent. Depends on 8.
  - files: `examples/{camunda-invoice,temporal-travel,kafka-refunds}/`
  - scenarios: `engines.*` (nine)
  - verify: `timeout 3m go -C examples test -timeout 2m ./... && timeout 2m go -C examples run ./kafka-refunds`

10. [ ] `core`: stream ownership and ordering before the protections are enabled on the public path. Decisions: none sealed; design first.
  - files: `core/drive.go`, `core/drive_lifecycle.go`, `core/model_stream.go`, `core/stream_stall.go`, `core/attach.go`
  - scenarios: `streams.stream-buffer-bound`, `streams.consumer-stall-preempts`, `streams.consumer-stall-detaches-with-log`, `streams.terminal-error-event`
  - verify: `timeout 5m go test -timeout 2m ./core/...`

11. [ ] `docs`: publish completion evidence after 6–10 land: the API-review verdict records the resolved policy and the exercised surface, the changelog carries the wave-2 fixes, and the tasks tally is recomputed.
  - files: `docs/design/api-review-m0-5.md`, `CHANGELOG.md`, `openspec/changes/m0-hardening/tasks.md`
  - scenarios: none
  - verify: `timeout 3m task spec && timeout 3m task api:check`
