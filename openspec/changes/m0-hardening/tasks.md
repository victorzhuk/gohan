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

6. [ ] `core`: journal fingerprint identity and outcome uncertainty. Decision D11. Validation first: trace `Reserve`/`Complete` through the governed tool path and the uncertainty contract before changing a signature, a sentinel or a store contract.
  - files: `core/stores/journal.go`, `core/stores/journal_memory.go`, `core/stores/journal_memory_test.go`, `testkit/storetest/`
  - scenarios: `stores.journal-*` (whatever the capability registers), `tools.effect-once`
  - verify: `timeout 2m go test -timeout 2m ./core/stores/... ./testkit/storetest/...`

7. [ ] `core`, `std`: the governed native path from chunk 5's design: `Build` resolves profiles, strategies, fidelity and middleware into the public execution path; `Explain` reports the same resolved configuration; preset and recipe prompts reach the manifest; per-run limit state.
  - files: per chunk 5's design
  - scenarios: `build.resolved-matrix`, `chains.prompt-strings-accounted-for`, `limits.*`, `flow.plain-invoke`
  - verify: `timeout 3m go test -timeout 2m ./core/... ./std/... && timeout 2m go -C examples run ./quickstart`

8. [ ] `examples`: the three offline acceptance processes drive the public governed path (`Build`, native agent entry, `std` chain, memory stores) while keeping all nine `engines.*` assertions; the refund example gains a runnable `main`. The three directories are independent.
  - files: `examples/{camunda-invoice,temporal-travel,kafka-refunds}/`
  - scenarios: `engines.*` (nine)
  - verify: `timeout 3m go -C examples test -timeout 2m ./...`

## Wave 3 — streaming, then evidence

9. [ ] `core`: stream ownership and ordering before the protections are enabled on the public path: deterministic terminal outcome on timeout, live bounded delivery instead of step-wide retention, joined producers on early exit, detached continuation, subscription bookkeeping. Decisions: none sealed; design first.
  - files: `core/drive.go`, `core/drive_lifecycle.go`, `core/model_stream.go`, `core/stream_stall.go`, `core/attach.go`
  - scenarios: `streams.stream-buffer-bound`, `streams.consumer-stall-preempts`, `streams.consumer-stall-detaches-with-log`, `streams.terminal-error-event`
  - verify: `timeout 5m go test -timeout 2m ./core/...`

10. [ ] `docs`: completion evidence. `docs/design/api-review-m0-5.md` drops the claims the gates cannot show, names the public surface the offline processes exercise, and records the pre-v1 breaking changes; `docs/design/compatibility.md` moves the unimplemented port-freeze clause to the recorded gaps; `openspec/changes/m0-core/tasks.md` reconciles the scenario tally against the registry; `docs/overview.md`, `docs/design/scenarios.md` and `docs/design/testing.md` lose stale counts, a nonexistent import path and superseded signatures.
  - files: the five documents plus `CHANGELOG.md`
  - scenarios: none
  - verify: `timeout 3m task spec && timeout 3m task api:check`
