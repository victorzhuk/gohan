# M0 hardening review and next plan

Date: 2026-10-05

Status: proposed. This review does not approve implementation changes.

## Decision

Repair the existing M0 public execution path before starting provider or persistent-store adapters. Do not declare M0.5 stability from scenario names alone. Retain the documented acceptance scope. Do not redefine the three acceptance processes as store-only checks.

Testing mode: existing-service-strict regression. Identity, durability, authorization, and concurrency are existing public claims. Each behavioral repair needs a failing public-path regression, a passing repair, and a bounded smoke execution.

## Scope and evidence limits

The review used six independent lenses: correctness, architecture, contracts, security/stores, concurrency/performance, and acceptance tooling. It also reviewed the README and executed targeted smoke programs.

The implementation contains 378 Go files across core, std, testkit, adapter, and examples. The workspace contains the root, adapter/otel, and examples modules. Every numbered M0 task row is checked. These observations establish repository scope, not acceptance quality.

The reviews are bounded samples, not an exhaustive audit of all 512 scenarios. Static findings below identify concrete code paths and contract conflicts. Only findings explicitly marked **reproduced** have runtime proof. Provider integrations, hosted CI, live engines, and production stores were not exercised.

No code repairs were applied during this review. README presentation and status were updated. Temporary smoke programs were removed.

## Verification observed

| Command or check | Result | Interpretation |
|---|---|---|
| `timeout 15m task lint test spec:gate examples:test api:check` | Stopped at `task test` | Lint passed with 0 issues. Root tests failed. Later targets in this command did not run. |
| `task test`, through the command above | Failed `TestPerformanceGate/overbudget_stable_advisory_then_strict` | Advisory fixture reported a bytes-per-operation increase from 0 to 3. Other listed root packages passed. This wrapper stopped before its adapter and examples legs. |
| `timeout 5m task api:check && timeout 3m task examples:test` | Stopped at `api:check` | Six differences were reported against root tag `v0.1.0`. The adapter comparison and examples target were not reached in this command. |
| `timeout 10m task spec:gate` | Passed | `registered=512 subtests=977 covered=291 missing=221 missing_in_scope=0 unregistered=0`. The 221 missing scenarios are deferred. This proves the current naming gate, not public-path correctness. |
| `timeout 3m task examples:test` | Passed | All five offline example packages passed. Several acceptance processes exercise store helpers rather than the governed public driver. |
| `timeout 2m go -C examples run ./quickstart` | Passed | Output: `Hello, quickstart!`. The example supplies a custom Stepper. This does not prove a reusable governed native agent exists. |
| Workspace LSP diagnostics | Failed workspace build | `examples/kafka-refunds`: `function main is undeclared in the main package`. Passing package tests do not prove runnable examples. |
| Temporary public Conversation smoke | Reproduced R01 | Two instances overwrote one run row, mixed events, and invalidated an in-flight lease. |
| Temporary Assemble smoke | Reproduced R02 | Different captured tool filters returned the first filter's tools. |
| Synthetic coverage events | Reproduced R19 | A `run` followed by `skip` counted as covered. No real skipped Go subtest was executed in this smoke. |

The later spec-gate success does not erase the earlier test failure. No three-run classification was performed. Do not label the performance failure flaky.

### API differences

The API tool reported these six differences against `v0.1.0`:

1. `FlowFunc` gained variadic `FlowOption` parameters.
2. `runtime.Batch` gained `Scheduler`.
3. `MemorySessionLog.Owner` changed its parameter name from `_` to `ctx`.
4. `stores.Checkpoint` gained `RunID`.
5. `stores.Lease` gained `Generation`.
6. `storetest.Lease` gained `Generation`.

The parameter-name change is not a Go compatibility break. Struct additions and variadic changes need semantic classification against the documented port/handle policy. Some additions can affect unkeyed literals or function-value assignments. Do not call all six reports true breaks. Do not move the baseline tag to hide them.

## Confirmed runtime defects

### R01 — P0: durable run identity collides across Conversation instances

Locations: `core/conversation.go:396-401`, `core/conversation.go:181-190`, `core/stores/runs.go:243-247`, `core/stores/events.go:88-101`.

Each Conversation starts its own counter at zero. Both first sends use `run-00000001`. MemoryRuns replaces the row without rejecting the existing identity.

**Reproduced:** Two instances shared stores and sent to different sessions. Both completed without initial errors. The final row belonged only to the second session. Event replay contained both sessions' Done events under one run ID. Interleaved execution caused the first run to fail with `gohan: session has no active run: run run-00000001`.

Repair: generate collision-resistant run identities using the standard library. Validate duplicate durable identities at the store boundary. Preserve operation deduplication as a separate concept.

### R02 — P0: assembly memo reuses the wrong filtered tools

Locations: `std/assembly.go:33-110`, `std/assembly.go:123-127`.

The process-global memo uses a function code pointer as filter identity. Closures from the same factory share code but can capture different selections. Assemble returns the cached request before evaluating the current filter.

**Reproduced:** With otherwise identical inputs, filter A selected `[alpha]`. Filter B selected `[beta]` on a forced miss but returned `[alpha]` on the memo hit. A third filter also returned `[alpha]`. The returned tool slices shared backing storage.

Repair: remove the unsound identity memo or replace it with an explicitly owned, semantically correct immutable cache. Do not use unsafe closure-layout assumptions. Benchmark any replacement after correctness is established.

### R19 — P1: skipped scenario tests satisfy coverage

Locations: `tools/spec_coverage.py:24-37`, `tools/spec_coverage.py:115-143`.

The parser records only `Action == "run"`. It ignores the final status.

**Reproduced with synthetic input:** A registered scenario's run event followed by a skip event still appeared in the covered set. A run event without any completion also counted.

Repair: track package-qualified test executions and accept only completed passing scenario subtests. Define duplicate bindings explicitly. Preserve pipeline failure propagation.

## Static code and architecture findings

| ID | Priority | Finding and impact | Evidence |
|---|---|---|---|
| R03 | P0 | Running recovery does not restore originator authority. Credential errors are discarded on checkpoint recovery. A reaper's ambient authority can reach execution. | `core/recover.go:244-290`; recovery and identity contracts |
| R04 | P0 | Cancel checks principal presence but not session ownership before resolving and signaling a run. | `core/conversation.go:247-256`; flow contract requires owner-checked Cancel |
| R05 | P0 | Steer can emit SteerApplied without an appender. Updated HistoryVersion remains in a copied state. Acknowledged input can be lost or omitted from the next step. | `core/drive_lifecycle.go:520-544`; `core/conversation.go:294-314`; runtime and flow contracts |
| R06 | P0 | Resume bypasses the initial stream's durable event relay and completion notification. Attach cannot reconstruct resumed execution reliably. | `core/resume.go:342-351`; compare `core/conversation.go:340-346` |
| R07 | P0 | Preempted recovery does not consume the single-use token. Consumed preempted checkpoints are skipped, leaving a consume-to-Resuming crash gap. | `core/recover_preempted.go:32-44`; `core/recover.go:74-78`; recovery/runtime token ownership rules |
| R08 | P0 | Recovered execution passes an empty AgentRun and omits checkpoint Save and identity metadata. A second suspension can call nil Save. | `core/recover.go:151-164`; `core/drive_lifecycle.go:401` |
| R09 | P1 | Build stores models and middleware without connecting them to ordinary public execution. Strategy/fidelity helpers and the governed turn helper have test callers only. No reusable native agent implementation was found. | `core/build.go:74-76,127-149`; `core/drive_turn.go:63`; LSP references; quickstart discards Build result |
| R10 | P1 | A preset captures one LimitsState for all uses. Independent runs can accumulate each other's turns, tools, cost, and elapsed time. | `std/presets.go:59-67,104-108`; `core/chains/limits.go:128-143,220-226` |
| R11 | P1 | Preset.Options does not register its PromptSet. Prompt changes can leave the manifest unchanged. Recipe repair text is a private literal. | `std/presets.go:116-121`; `core/release_manifest.go:59-74`; `std/flow/extract.go:139-140` |
| R12 | P1 | Build logs an empty resolution matrix. Its scenario tests call the formatter directly. Public Explain is absent, and documented vocabulary aliases are incomplete. | `core/build.go:147-149`; `core/build_matrix_test.go:61-68`; `core/chains/explain.go`; `core/aliases.go`; ADR-0139 |
| R13 | P1 | Stream buffering and ConsumerStall protections remain test-invoked helpers rather than public Conversation integration. | `core/conversation.go:201-209,304-352`; LSP references for NewModelStream and StallGuard.Watch |
| R14 | P1 | StallDetach increments a counter without transferring consumption. Early exit does not join the producer. | `core/stream_stall.go:104-119,145-208` |
| R15 | P1 | sinkRelay retains all component events until Step returns. Text deltas do not reach consumers during generation, and memory grows with the step's events. | `core/drive.go:14-29`; `core/drive_lifecycle.go:279-305` |
| R16 | P1 | Model timeout races with closed-buffer EOF. Selecting EOF can hide a pending timeout error. | `core/model_stream.go:105-133,156-166` |
| R17 | P2 | Conversation retains completed-run identities indefinitely. Unsubscribe can leave empty waiter maps. | `core/conversation.go:64-86`; `core/attach.go:74-87` |
| R18 | P1 | Performance parsing treats missing B/op or allocs/op as measured zero. Malformed measurements can pass. | `tools/performance_gate.go:384-413,458-473` |
| R20 | P1 | The advisory performance fixture measures exact allocation axes with only five iterations, contrary to its own 1000-iteration rule. | `core/performance_gate_test.go:14-17,24,47,65,70`; `tools/performance_gate.go:418-426,463-473` |
| R21 | P2 | Executable limit middleware and concrete preset/default values live in core despite the core budget rule. The limits contract also contains conflicting ownership intent. | `core/chains/limits.go`; `core/types/limits.go:27-63`; `core/limits.go:12-18`; architecture §4.2a |

R20 explains why tiny measurements can amplify a one-off allocation. The exact allocation source was not traced. A nominally identical fixture is not proof that every measured axis must be identical. Match the existing iteration rule without weakening allocation regression checks.

## Documentation and acceptance inconsistencies

- **D01 — P1:** The M0.5 review records a no-tag skip transcript and also claims a successful baseline comparison. Current `api:check` fails. Source: `docs/design/api-review-m0-5.md`.
- **D02 — P1:** The invoice, travel, and refund acceptance processes mainly exercise local store/process helpers. They do not exercise Build, the governed native driver, and std chains as ADR-0083/ADR-0136 require. Passing these tests does not justify a driver API freeze.
- **D03 — P1:** Compatibility documentation promises a port method-set freeze check through `spec:types`. The generator scans specs and does not implement that check. Source: `docs/design/compatibility.md`; `tools/gen_types_index.py`.
- **D04 — P1:** The types index describes specification intent, not a complete inventory of shipped interfaces. The freeze registry and implementation surface differ. Keep these inventories distinct.
- **D05 — P2:** The M0 tally says 280 active and 232 deferred scenarios. Registry inspection found 279 without deferral and 233 deferred, including 37 at M2 rather than 36. Some implemented test IDs still carry later milestone deferrals. Source: `openspec/changes/m0-core/tasks.md`; `openspec/scenarios.json`.
- **D06 — P2:** Overview ADR count is stale. The canonical quickstart imports nonexistent `core/memory`. Testing documentation contains superseded suite signatures. Source: `docs/overview.md`; `docs/design/scenarios.md`; `docs/design/testing.md`.
- **D07 — P1:** Kafka refund examples pass tests but do not build as a runnable main package. Source: workspace diagnostics and `examples/kafka-refunds`.

## Security and store candidates requiring contract validation

These candidates need a focused source/contract trace before implementation. The security lens did not read every relevant capability contract or every production call path.

1. Journal Reserve accepts an existing CallKey without comparing fingerprints: `core/stores/journal_memory.go:40-42`.
2. Journal Complete returns success for expired or absent reservations: `core/stores/journal_memory.go:59-71`.
3. Approval grants use the approver's subject rather than the suspended invocation subject: `std/permission/grant.go:78-91`.
4. Grant expiry uses request expiry plus TTL. Invalid TTL becomes the maximum: `std/permission/grant.go:73-88`.
5. Reserved approval receipt rejection and prior-approved argument lookup have no driver callers: `core/approval_receipt.go:36,75`; `core/permission/approval.go:83`.
6. Audit reads search all tenants by session ID: `core/stores/audit.go:145-163`. Establish the intended session-ID uniqueness and authorization contract before declaring cross-tenant exposure.
7. Unknown key profiles fall back to tenant/platform selection: `std/keys/keys.go:61-92`. Establish the configured default contract before changing selection semantics.
8. ExpiryQueue has an unguarded map and reschedules escalation at the old deadline: `std/permission/expiry.go:30-71`. Establish concurrency and tier-window contracts.
9. Send relies on session-read authorization and starts a run before loading session history. Check the stricter write-authorization and refusal-order requirements: `core/conversation.go:181-225`.

Do not introduce new sentinel errors or store methods from these suggestions without checking the existing vocabulary and optional interfaces.

## Proposed implementation sequence

These are workstreams, not sealed executable chunks. Before dispatch, split each workstream into one vertical seam with at most three or four implementation sites. Bind existing scenario IDs and verify actual test function names. Seal shared API and ownership decisions first.

### Wave 0 — Restore trustworthy evidence

Independent work:

- Repair semantic API comparison, including parameter-name normalization. Classify genuine pre-v1 breaks without retagging `v0.1.0`.
- Repair passing-only scenario coverage and fail-closed performance parsing.
- Align the advisory fixture with its documented iteration floor.
- Correct stale completion evidence and registry arithmetic. Do not publish a new stability verdict.

Verification:

```sh
timeout 3m go test -timeout 2m ./tools/apicheck
timeout 3m python3 -m unittest discover -s tools -p '*_test.py'
timeout 3m go test -timeout 2m ./core -run '^TestPerformanceGate$'
timeout 5m task api:check
```

API success requires a documented compatibility decision for genuine changes. Normalizing one false positive does not resolve the remaining reports.

### Wave 1 — Authority, identity, and filter isolation

Independent owners:

- Recovery authority and Cancel authorization: R03/R04. Coordinate Send authorization validation in the same conversation owner.
- Assembly isolation: R02. Remove incorrect reuse and prove captured filters execute correctly.
- Journal candidates: validate items 1/2, then repair proven identity and uncertainty violations.

Next dependent seam: R01 run identity and duplicate Start handling. It shares Conversation files with authorization, so do not edit those files concurrently.

Acceptance:

- A distinct reaper principal never becomes execution authority.
- Credential exchange failure prevents runtime/model/tool work.
- A non-owner cannot signal another session.
- Two Conversation instances preserve separate runs and event histories.
- Different captured filters produce their own tool sets.
- A failed journal completion cannot claim certain replayability.

Targeted tests use `go test -timeout 2m` over the affected packages. Retain the run-ID and filter smoke cases as public-behavior regressions.

### Wave 2 — Durable transitions

Ordered seams:

1. Persist steering and propagate HistoryVersion: R05.
2. Record resumed events and terminal notifications: R06.
3. Close client/reaper consume-to-resume gaps: R07.
4. Restore complete recovered AgentRun metadata and checkpoint saving: R08.

Shared lifecycle/resume files need one integration owner. File-disjoint regression tests may run in parallel.

Acceptance:

- SteerApplied follows a successful append, and the next request includes the steer.
- Attach reconstructs resumed events with continuous sequence numbers.
- Exactly one client/reaper executes a checkpoint.
- A crash after Consume and before Resuming remains recoverable.
- Recovered execution can suspend and resume again without losing authority or panicking.

### Wave 3 — Governed native construction

Resolve the missing native constructor and effect-step design against the existing flow/runtime contracts before code dispatch. This requires a focused design review because the current implementation lacks the documented reference path.

Reuse Build, Stack, AgentRun, the governed turn implementation, and DriveLifecycle. Do not add a second loop or new vendor adapter.

Ordered seams:

1. Connect models, strategies, fidelity validation, and middleware to public native execution: R09.
2. Allocate accounting state per independent run, with explicit tree sharing: R10.
3. Derive Explain, startup resolution records, and release identity from the same execution configuration: R11/R12.
4. Resolve core policy ownership with a cited ADR where contracts change: R21.
5. Migrate quickstart and excursions from custom agent runtimes.

Acceptance: configured middleware changes observed execution. Unsupported capabilities fail before provider calls. Independent runs do not share budgets. Used prompt changes alter manifests. Explain describes the actual request and chain.

### Wave 4 — Streaming and acceptance processes

Repair stream ownership and ordering before enabling wrappers on the public path: R13–R17. The bounded live handoff crosses the driver/runtime boundary and needs a sealed ownership design.

Acceptance:

- First TextDelta arrives before provider completion.
- Buffers obey the existing configured limits.
- Timeout always yields the required terminal error.
- Attached early exit cancels and joins helpers.
- A stalled detached consumer does not stop the run. EventLog remains attachable.

After the governed path exists, migrate the three process examples in parallel. Their directories are independent. Keep all nine existing engines scenarios and their business assertions. Exercise real public invocation, approval, resume, recovery, and std governance. Add a runnable refund entry using the same offline implementation.

### Wave 5 — Compatibility and release evidence

Update existing architecture, testing, compatibility, reference scenarios, and changelog files after repaired behavior is proven. Correct completion evidence without erasing earlier failures.

Run the project floor:

```sh
timeout 5m task lint
timeout 15m task test
timeout 15m task spec
timeout 5m task api:check
timeout 35m task test:race
timeout 5m task examples:test
timeout 2m go -C examples run ./quickstart
timeout 2m go -C examples run ./kafka-refunds
```

Also require clean workspace LSP diagnostics. Run the performance gate on its intended reference runner before making absolute latency claims. Report any unavailable runner or provider evidence explicitly.

## Approval gate

Approve this remediation direction before changing architecture, public contracts, compatibility enforcement, or multi-file runtime behavior. Each workstream still needs a bounded executable brief. No adapter expansion should precede closure of the authority, durable-transition, and public-governance gaps.
