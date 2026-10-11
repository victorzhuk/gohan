# Design: m2

## 1. Handoff from M1

M1 is closed: the core runtime and its governed components, the store ports including `adapter/postgres`, redaction with residency, taint enforcement, the egress policy, provider conformance and metadata, run notifications, hedging, flags, exact-match caching, session titles, the human-handoff lane, message blobs, identity detach and the routing ordering policies all ship, their scenario ids pass on the public path, and their change packages are archived. The acceptance matrix in `openspec/changes/m1/design.md` carries the evidence.

M2 inherits three things from that record.

1. **The freeze stance.** `docs/overview.md:108-112` puts the API freeze at the M2 exit: every port that exists at the end of M2 is frozen at v1, and `m2-api-freeze` owns the compatibility check and the `v1.0.0` tag. A change that lands in M2 must therefore settle its port shapes before it closes; growth after the freeze goes through optional interfaces (`docs/design/compatibility.md`).
2. **Sealed seams.** Capability ports live in `core`, stored records carry their version in an envelope and the journal keeps a call's arguments (ADR-0157); endpoint ordering is declaration order plus a class partition (ADR-0160). A change that needs a port declares it in `core` beside the existing ones.
3. **The acceptance discipline.** A scenario id appears once in `openspec/scenarios.json`, once as a subtest name, and its row here moves to `done` only with evidence that names the test.

Boundaries the first M2 changes must not cross: no second runtime loop beside the core drive (limits, journal and lifecycle stay in the harness), no port method added to a frozen interface, no scenario rewording once registered, and no example that reaches a path the capability specs do not describe.

## 2. Acceptance matrix

63 rows: every registry id in `runtime`, `context` and `sandbox`, the four M2-deferred ids in `flow`, `guards`, `limits` and `identity`, and two inherited `stores` checkpoint-versioning ids proved on the eino flows. 36 rows carry `deferred_to: M2`; 27 are inherited checks: 25 runtime rows and two stores rows. Owners identify the changes responsible for the behavior. `m2-s4-acceptance` records the re-run evidence without taking ownership. The milestone gate checks all registry ids at or below M2 rank, including the PostgreSQL repair ids outside this selected matrix.

### 2.1 runtime (27: 2 M2-deferred, 25 inherited)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `runtime.plain-answer` | `m2-eino-runtime` | core runtime and governed components | missing | `TestConformance/runtime.plain-answer` (`adapter/eino/conformance_test.go:9`, suite `testkit/conformance/runtime.go:53`) |
| `runtime.tool-round-trip` | `m2-eino-runtime` | core runtime and governed components | missing | `TestConformance/runtime.tool-round-trip` (`adapter/eino/conformance_test.go:9`, suite `testkit/conformance/runtime.go:53`) |
| `runtime.parallel-calls-ordering` | `m2-eino-runtime` | core runtime and governed components | missing | `TestConformance/runtime.parallel-calls-ordering` (`adapter/eino/conformance_test.go:9`, suite `testkit/conformance/runtime.go:53`) |
| `runtime.max-turns` | `m2-eino-runtime` | core Drive limits | missing | `TestConformance/runtime.max-turns` (`adapter/eino/conformance_test.go:9`, suite `testkit/conformance/runtime.go:53`) |
| `runtime.cancellation` | `m2-eino-runtime` | core Drive cancellation | missing | `TestConformance/runtime.cancellation` (`adapter/eino/conformance_test.go:9`, suite `testkit/conformance/runtime.go:53`) |
| `runtime.foreign-tool-under-native` | `m2-eino-bridges` | governed tool chain | missing | |
| `runtime.governed-components-in-eino-graph` | `m2-eino-graph-native-resume` | `m2-eino-runtime`, `m2-eino-bridges` | missing | `TestGraphFlow/runtime.governed-components-in-eino-graph` (`adapter/eino/einoflow/graph_test.go:141`) |
| `runtime.message-round-trip` | `m2-eino-bridges` | message fidelity contract | missing | eino leg `TestMessageBridge/runtime.message-round-trip` (`adapter/eino/message_bridge_test.go:59`); the adk-go leg is M3's and stays open — §3 conflict 3 governs where this row's first owner differed |
| `runtime.suspend-order` | `m2-eino-runtime` | core Drive and checkpoint stores | missing | |
| `runtime.append-before-tool` | `m2-eino-runtime` | core Drive and journal | missing | |
| `runtime.done-after-finish` | `m2-eino-runtime` | core Drive and Runs | missing | |
| `runtime.shutdown-rejects-new` | `m2-eino-runtime` | core Stack shutdown | missing | |
| `runtime.shutdown-preempts-at-safe-point` | `m2-eino-runtime` | core shutdown and checkpoints | missing | |
| `runtime.shutdown-side-effect-completes` | `m2-eino-runtime` | governed tool cancellation shield | missing | |
| `runtime.shutdown-partial-model-call-dropped` | `m2-eino-runtime` | governed model cancellation and journal | missing | |
| `runtime.preempted-resume-continue` | `m2-eino-runtime` | core Resume and checkpoints | missing | |
| `runtime.shutdown-grace-exhausted` | `m2-eino-runtime` | core shutdown and recovery | missing | |
| `runtime.batch-gate-first` | `m2-eino-runtime` | core governed-turn batch path | missing | |
| `runtime.batch-limit-before-execute` | `m2-eino-runtime` | core governed-turn limits | missing | |
| `runtime.readonly-parallel` | `m2-eino-runtime` | core native batch regression suite | missing | |
| `runtime.side-effects-sequential` | `m2-eino-runtime` | core native batch regression suite | missing | |
| `runtime.batch-ask-after-allowed` | `m2-eino-runtime` | governed components, journal, suspension | missing | |
| `runtime.batch-one-result-per-call` | `m2-eino-runtime` | core governed-turn resume path | missing | |
| `runtime.parallel-tools-cap` | `m2-eino-runtime` | core native batch regression suite | missing | |
| `runtime.sequential-tools-hint` | `m2-eino-runtime` | model capability and sequential-tools contract | missing | |
| `runtime.readyz-false-on-schema-skew` | `m2-eino-runtime` | core Stack health and Postgres schema checks | missing | |
| `runtime.readyz-false-during-shutdown` | `m2-eino-runtime` | core Stack health and shutdown | missing | |

The native batch scheduling rows remain native regression obligations: the runtime spec lets a foreign runtime schedule its own calls, and M2 must not require native scheduling inside eino. `runtime.message-round-trip` names the eino and the adk-go bridge; M2 owns the eino leg and M3 keeps the adk-go leg without claiming duplicate coverage (§3).

### 2.2 context (15, all deferred to M2)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `context.projection-does-not-write` | `m2-context-projections` | assembly and SessionLog contracts | missing | |
| `context.projection-deterministic` | `m2-context-projections` | history version and model profile | missing | |
| `context.keep-last-n` | `m2-context-projections` | tool-use/result adjacency | missing | |
| `context.notes-excluded` | `m2-context-projections` | working-state notes | missing | |
| `context.clear-at-least` | `m2-context-projections` | ContextBudget | missing | |
| `context.compaction-persisted` | `m2-context-persisted-compaction` | `m2-context-projections`, SessionLog | missing | |
| `context.replay-after-compaction` | `m2-context-persisted-compaction` | persisted compaction and Replay | missing | |
| `context.pending-call-survives` | `m2-context-persisted-compaction` | suspension and journal state | missing | |
| `context.summary-guarded` | `m2-context-persisted-compaction` | StageContext guards and Truncate | missing | |
| `context.opaque-bound-to-model` | `m2-provider-compaction` | `m2-context-persisted-compaction`, model routing | missing | |
| `context.opaque-fallback-rejected` | `m2-provider-compaction` | Build, provider caps, fallback validation | missing | |
| `context.compaction-audited` | `m2-context-persisted-compaction` | AuditLog and EventLog | missing | |
| `context.calibration-from-usage` | `m2-context-calibration-overflow` | ContextBudget and model Usage | missing | |
| `context.calibration-persisted-for-replay` | `m2-context-calibration-overflow` | checkpoints and compaction audit records | missing | |
| `context.overflow-compacts-and-retries-once` | `m2-context-calibration-overflow` | `m2-context-persisted-compaction`, overflow classification | missing | |

Projections land before persisted compaction. Every stage preserves pending calls, tool-use/result adjacency, the full-log taint window and journal fingerprints. Provider compaction depends on persisted compaction and explicit provider caps; calibration persists the ratio it used so replay stays deterministic.

### 2.3 sandbox (15, all deferred to M2)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `sandbox.effect-derived` | `m2-sandbox-governance` | Sandbox port and shared EgressPolicy | missing | |
| `sandbox.egress-denied` | `m2-sandbox-adapter-conformance` | `m2-sandbox-governance`, Docker and E2B adapters | missing | |
| `sandbox.egress-allowlisted` | `m2-sandbox-adapter-conformance` | shared egress policy and adapter network enforcement | missing | |
| `sandbox.secret-injected` | `m2-sandbox-governance` | CredentialSource and originator identity | missing | |
| `sandbox.secret-leak-redacted` | `m2-sandbox-governance` | secret resolution and output scanning | missing | |
| `sandbox.per-session-persists` | `m2-sandbox-lifecycle-resume` | `m2-sandbox-governance`, session metadata | missing | |
| `sandbox.snapshot-on-suspend` | `m2-sandbox-lifecycle-resume` | Workspace.Snapshot and checkpoint lifecycle | missing | |
| `sandbox.resume-restores` | `m2-sandbox-lifecycle-resume` | workspace snapshots and core Resume | missing | |
| `sandbox.replay-no-reexec` | `m2-sandbox-lifecycle-resume` | journal and Replay | missing | |
| `sandbox.per-call-closes` | `m2-sandbox-governance` | Workspace lifecycle | missing | |
| `sandbox.seconds-budget` | `m2-sandbox-governance` | tree usage and RunLimits | missing | |
| `sandbox.exec-timeout` | `m2-sandbox-adapter-conformance` | adapter process termination and effect classification | missing | |
| `sandbox.output-to-store` | `m2-sandbox-governance` | OutputStore and tool output caps | missing | |
| `sandbox.host-exec-warning` | `m2-sandbox-governance` | existing tool/exec Build warning | missing | |
| `sandbox.egress-shares-policy-type` | `m2-sandbox-adapter-conformance` | shared EgressPolicy, Explain, adapter enforcement | missing | |

The port and its governance land before adapter conformance and the persisted workspace lifecycle. One adapter suite runs against both Docker and E2B and owns the shared ids once; adapters implement the suite contract and claim no duplicate coverage. `sandbox.host-exec-warning` is an inherited acceptance check: the host runner stays, and sandbox execution does not replace it.

### 2.4 flow (1, deferred to M2)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `flow.backend-swap` | `m2-eino-runtime` | runtime registration and the common Flow contract | missing | `TestBackendSwap/flow.backend-swap` (`adapter/eino/backend_swap_test.go:10`, suite `testkit/conformance/backend_swap.go:9`) |

### 2.5 guards (1, deferred to M2)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `guards.origin-survives-conversion` | `m2-eino-bridges` | message origins and governed guards | missing | |

Message conversion preserves provenance before the graph and the runtime acceptance can establish governance.

### 2.6 limits (1, deferred to M2)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `limits.limits-under-foreign-backend` | `m2-eino-runtime` | core Drive and governed limit chains | missing | `TestLimits/limits.limits-under-foreign-backend` (`adapter/eino/limits_test.go:122`) |

Limits stay in the harness; there is no second limit loop in an adapter.

### 2.7 identity (1, deferred to M2)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `identity.credential-not-in-sandbox` | `m2-sandbox-governance` | CredentialSource, sandbox secret resolution, output scanning | missing | |

Harness credentials and explicitly authorised `SecretRef` injection are separate paths: the sandbox secret contract permits the latter and forbids arbitrary credential export.

### 2.8 stores (2 inherited checkpoint-versioning checks)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `stores.native-checkpoint-after-adapter-upgrade` | `m2-eino-graph-native-resume` | `m2-eino-runtime`, core Resume and checkpoint stores | missing | `TestNativeResume/stores.native-checkpoint-after-adapter-upgrade` (`adapter/eino/einoflow/resume_test.go:63`; eino agent version-mismatch decision) |
| `stores.graph-checkpoint-incompatible` | `m2-eino-graph-native-resume` | `m2-eino-runtime`, core Resume and checkpoint stores | missing | `TestNativeResume/stores.graph-checkpoint-incompatible` (`adapter/eino/einoflow/resume_test.go:78`; eino graph version-mismatch decision) |

These are existing R5n ids, not new scenarios or deferrals. The archived graph change owns the behavior. `m2-s4-acceptance` binds the re-run evidence. Mark a row `done` only after its evidence includes the accepted public-path proof. Decision-level eino subtests alone do not establish Replay telemetry or token preservation.

## 3. Milestone conflicts

Three conflicts are recorded here rather than resolved silently.

1. **E2B and the two examples.** The M2 proposal includes `adapter/e2b`, `research-report` and `data-analyst`; the overview's M2 row omits them and its M4 row lists both examples. **Resolution:** the proposal is the change's scope and the matrix above assigns them; the overview's example rows describe when an example is expected to be complete for the milestone narrative, not a second owner. `m2-e2b-sandbox`, `m2-research-report-example` and `m2-data-analyst-example` own the work; the overview is corrected when the change packages land.
2. **Guarded Summarize.** The context spec requires guarded summarization for several M2-deferred scenarios, and the overview assigns the Summarize policy deliverable to M4. **Resolution:** M2 implements the guarded behaviour those scenarios name — truncation and summarization behind the StageContext guards, with the summary itself audited; the broader policy surface (prompt, tuning, policy source) stays the M4 deliverable. A change that needs the policy surface must say so instead of importing M4 scope.
3. **`runtime.message-round-trip`.** One id names the eino and the adk-go bridge. **Resolution:** M2 proves the eino leg and records evidence for it; M3 proves the adk-go leg under the same id's requirement, without M2 claiming the whole id closed on one bridge.

## 4. Obligations without a scenario ID

| Obligation | Owner | Status | Evidence |
|---|---|---|---|
| adapter/eino runtime and conformance suites | `m2-eino-runtime` | missing | |
| eino model/tool/message bridges | `m2-eino-bridges` | missing | |
| graph flow, Native checkpoint/resume, version mismatch behaviour | `m2-eino-graph-native-resume` | missing | |
| sandbox port and `std/sandbox` governed tool set | `m2-sandbox-governance` | missing | |
| adapter/docker | `m2-docker-sandbox` | missing | |
| adapter/e2b | `m2-e2b-sandbox` | missing | |
| `ProviderCompact` implementation and capability declarations | `m2-provider-compaction` | missing | |
| research-report example | `m2-research-report-example` | missing | |
| data-analyst example | `m2-data-analyst-example` | missing | |
| S4 graph acceptance and conformance.* green on eino | `m2-s4-acceptance` | partial (eino green; adk-go legs M3-open) | eino suites re-run green: `adapter/eino/einoflow` `TestGraphFlow`, `TestGraphFlowGoverning`, `TestFromRunnableGuards`, `TestNativeResume` and `adapter/eino` `TestConformance` → `conformance.Runtime`, `TestBackendSwap/flow.backend-swap`, `TestLimits/limits.limits-under-foreign-backend`, `TestMessageBridge/runtime.message-round-trip`; `task spec:gate GATE=M2` still reports `missing_in_scope=19`, all of them the `stores.pg-*` ids of the archived `repair-postgres-store-contracts` whose tests sit behind `//go:build full`; all 19 pass under `go -C adapter/postgres test -tags full -run 'TestJournalReserveAtomic|TestSessions|TestRunsRemediation|TestOutputClaimsLedger|TestNoticesRemediation|TestSessionCascadeRemediation'`, so the `-short` sweep in `Taskfile.yml` `spec:gate` is blind to them and the gate cannot report 0 without a `-tags full` leg; the adk-go graph leg and the adk-go bridge leg stay M3-open (`openspec/changes/m3/proposal.md:7`) |
| port-method-set compatibility check before the v1 freeze | `m2-api-freeze` | missing | |
| `v1.0.0` at the M2 exit | `m2-api-freeze` | missing | |

The examples accept composed public paths after their features exist; their precise feature dependencies are settled in their own change designs. The two compatibility obligations come from the overview's M2 row and M1's handoff; they are not inferred scope.

## 5. Lane status

The first-wave packages are archived. m2-eino-bridges and m2-context-persisted-compaction are archived (2026-10-11 batch). m2-eino-graph-native-resume stands at 12/12, m2-provider-compaction at 13/13, m2-context-calibration-overflow at 13/13; m2-sandbox-adapter-conformance at 15/17 with the Docker/E2B legs open (O2 closed: `chains.ToolInfo.Egress` renders the declared policy); m2-sandbox-lifecycle-resume is unstarted at 0/37 with R6 parked. Every second-wave package is authored: m2-eino-graph-native-resume, m2-provider-compaction, m2-context-calibration-overflow, m2-sandbox-adapter-conformance and m2-sandbox-lifecycle-resume. m2-sandbox-adapter-conformance carries no spec delta (it verifies behavior the `sandbox` capability already states) and its Docker and E2B acceptance legs stay blocked until `m2-docker-sandbox` and `m2-e2b-sandbox` land with provider evidence; m2-sandbox-lifecycle-resume stays blocked by R6. Author each remaining third-wave package (`m2-docker-sandbox`, `m2-e2b-sandbox`, `m2-research-report-example`, `m2-data-analyst-example`, `m2-s4-acceptance`, `m2-api-freeze`) after its dependencies and shared contracts settle. `m2-s4-acceptance` records nine ids in the existing §2 subsections and the two checkpoint-versioning stores ids in §2.8. The eino `TestNativeResume` subtests provide decision-level evidence. Row closure requires the accepted public-path evidence. The adk-go bridge and graph legs remain M3-open.
