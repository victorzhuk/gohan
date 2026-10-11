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
| `runtime.plain-answer` | `m2-eino-runtime` | core runtime and governed components | done | `TestConformance/runtime.plain-answer` (`adapter/eino/conformance_test.go:9`, suite `testkit/conformance/runtime.go:53`) |
| `runtime.tool-round-trip` | `m2-eino-runtime` | core runtime and governed components | done | `TestConformance/runtime.tool-round-trip` (`adapter/eino/conformance_test.go:9`, suite `testkit/conformance/runtime.go:53`) |
| `runtime.parallel-calls-ordering` | `m2-eino-runtime` | core runtime and governed components | done | `TestConformance/runtime.parallel-calls-ordering` (`adapter/eino/conformance_test.go:9`, suite `testkit/conformance/runtime.go:53`) |
| `runtime.max-turns` | `m2-eino-runtime` | core Drive limits | done | `TestConformance/runtime.max-turns` (`adapter/eino/conformance_test.go:9`, suite `testkit/conformance/runtime.go:53`) |
| `runtime.cancellation` | `m2-eino-runtime` | core Drive cancellation | done | `TestConformance/runtime.cancellation` (`adapter/eino/conformance_test.go:9`, suite `testkit/conformance/runtime.go:53`) |
| `runtime.foreign-tool-under-native` | `m2-eino-bridges` | governed tool chain | done | `TestForeignToolUnderNative/runtime.foreign-tool-under-native` (`adapter/eino/foreign_tool_test.go:151`; the mixing-chain leg from `m2-eino-runtime` task 4.4 and the bridge-fidelity/public-path leg from `m2-eino-bridges` task 4.2, plus the denied-call half `denied_call_blocked`) |
| `runtime.governed-components-in-eino-graph` | `m2-eino-graph-native-resume` | `m2-eino-runtime`, `m2-eino-bridges` | done | `TestGraphFlow/runtime.governed-components-in-eino-graph` (`adapter/eino/einoflow/graph_test.go:141`) |
| `runtime.message-round-trip` | `m2-eino-bridges` | message fidelity contract | missing | eino leg `TestMessageBridge/runtime.message-round-trip` (`adapter/eino/message_bridge_test.go:59`); the adk-go leg is M3's and stays open — §3 conflict 3 governs where this row's first owner differed |
| `runtime.suspend-order` | `m2-eino-runtime` | core Drive and checkpoint stores | done | `TestSuspend/runtime.suspend-order` (`adapter/eino/suspend_test.go:49`) |
| `runtime.append-before-tool` | `m2-eino-runtime` | core Drive and journal | done | `TestRuntimeLifecycle/runtime.append-before-tool` (`core/drive_lifecycle_test.go:426`) |
| `runtime.done-after-finish` | `m2-eino-runtime` | core Drive and Runs | done | `TestRuntimeLifecycle/runtime.done-after-finish` (`core/drive_lifecycle_test.go:516`) |
| `runtime.shutdown-rejects-new` | `m2-eino-runtime` | core Stack shutdown | done | `TestPreemption/runtime.shutdown-rejects-new` (`adapter/eino/preemption_test.go:188`) |
| `runtime.shutdown-preempts-at-safe-point` | `m2-eino-runtime` | core shutdown and checkpoints | done | `TestPreemption/runtime.shutdown-preempts-at-safe-point` (`adapter/eino/preemption_test.go:212`) |
| `runtime.shutdown-side-effect-completes` | `m2-eino-runtime` | governed tool cancellation shield | done | `TestPreemption/runtime.shutdown-side-effect-completes` (`adapter/eino/preemption_test.go:277`) |
| `runtime.shutdown-partial-model-call-dropped` | `m2-eino-runtime` | governed model cancellation and journal | done | `TestPreemption/runtime.shutdown-partial-model-call-dropped` (`adapter/eino/preemption_test.go:324`) |
| `runtime.preempted-resume-continue` | `m2-eino-runtime` | core Resume and checkpoints | done | `TestPreemption/runtime.preempted-resume-continue` (`adapter/eino/preemption_test.go:409`) |
| `runtime.shutdown-grace-exhausted` | `m2-eino-runtime` | core shutdown and recovery | done | `TestPreemption/runtime.shutdown-grace-exhausted` (`adapter/eino/preemption_test.go:472`) |
| `runtime.batch-gate-first` | `m2-eino-runtime` | core governed-turn batch path | done | `TestRuntimeBatchProtocol/runtime.batch-gate-first` (`core/runtime_batch_test.go:91`) |
| `runtime.batch-limit-before-execute` | `m2-eino-runtime` | core governed-turn limits | done | `TestRuntimeBatchProtocol/runtime.batch-limit-before-execute` (`core/runtime_batch_test.go:117`) |
| `runtime.readonly-parallel` | `m2-eino-runtime` | core native batch regression suite | done | `TestRuntimeScheduling/runtime.readonly-parallel` (`core/runtime_schedule_test.go:185`) |
| `runtime.side-effects-sequential` | `m2-eino-runtime` | core native batch regression suite | done | `TestRuntimeScheduling/runtime.side-effects-sequential` (`core/runtime_schedule_test.go:248`) |
| `runtime.batch-ask-after-allowed` | `m2-eino-runtime` | governed components, journal, suspension | done | `TestResume/completed_calls_answer_from_the_journal` and `runtime.batch-ask-after-allowed` (`adapter/eino/resume_test.go`, `adapter/eino/runtime_test.go:41`) |
| `runtime.batch-one-result-per-call` | `m2-eino-runtime` | core governed-turn resume path | done | `runtime.batch-one-result-per-call` (`adapter/eino/runtime_test.go:86`, with the resume leg in `adapter/eino/resume_test.go`) |
| `runtime.parallel-tools-cap` | `m2-eino-runtime` | core native batch regression suite | done | `TestRuntimeScheduling/runtime.parallel-tools-cap` (`core/runtime_schedule_test.go:219`) |
| `runtime.sequential-tools-hint` | `m2-eino-runtime` | model capability and sequential-tools contract | done | `TestRuntimeScheduling/runtime.sequential-tools-hint` (`core/runtime_schedule_test.go:285`) |
| `runtime.readyz-false-on-schema-skew` | `m2-eino-runtime` | core Stack health and Postgres schema checks | done | `TestHealth/runtime.readyz-false-on-schema-skew` (`adapter/eino/health_test.go:15`) |
| `runtime.readyz-false-during-shutdown` | `m2-eino-runtime` | core Stack health and shutdown | done | `TestHealth/runtime.readyz-false-during-shutdown` (`adapter/eino/health_test.go:37`) |

The native batch scheduling rows remain native regression obligations: the runtime spec lets a foreign runtime schedule its own calls, and M2 must not require native scheduling inside eino. `runtime.message-round-trip` names the eino and the adk-go bridge; M2 owns the eino leg and M3 keeps the adk-go leg without claiming duplicate coverage (§3).

### 2.2 context (15, all deferred to M2)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `context.projection-does-not-write` | `m2-context-projections` | assembly and SessionLog contracts | done | `TestProjectionPublicPath/context.projection-does-not-write` (`core/projection_scenarios_test.go`; also `TestProjectionAssembly/context.projection-does-not-write` in `core/projection_assembly_test.go`) |
| `context.projection-deterministic` | `m2-context-projections` | history version and model profile | done | `TestProjectionPublicPath/context.projection-deterministic` (`core/projection_scenarios_test.go`; also `TestProjectionAssembly/context.projection-deterministic` in `core/projection_assembly_test.go`) |
| `context.keep-last-n` | `m2-context-projections` | tool-use/result adjacency | done | `TestProjectionPublicPath/context.keep-last-n` (`core/projection_scenarios_test.go`; unit `TestClearToolResults/context.keep-last-n` in `std/context/clear_tool_results_test.go`) |
| `context.notes-excluded` | `m2-context-projections` | working-state notes | done | `TestProjectionPublicPath/context.notes-excluded` (`core/projection_scenarios_test.go`; unit `TestClearToolResults/context.notes-excluded` in `std/context/clear_tool_results_test.go`) |
| `context.clear-at-least` | `m2-context-projections` | ContextBudget | done | `TestClearToolResults/context.clear-at-least` (`std/context/clear_tool_results_test.go`), bound on the public path in `core/projection_scenarios_test.go` |
| `context.compaction-persisted` | `m2-context-persisted-compaction` | `m2-context-projections`, SessionLog | done | `TestCompactionPublicPath/context.compaction-persisted` (`core/compaction_public_path_test.go:179`; native-flow and foreign-flow variants) |
| `context.replay-after-compaction` | `m2-context-persisted-compaction` | persisted compaction and Replay | done | `TestCompactionPublicPath/context.replay-after-compaction` (`core/compaction_public_path_test.go:179`) |
| `context.pending-call-survives` | `m2-context-persisted-compaction` | suspension and journal state | done | `TestCompactionPublicPath/context.pending-call-survives` (`core/compaction_public_path_test.go:179`) |
| `context.summary-guarded` | `m2-context-persisted-compaction` | StageContext guards and Truncate | done | `TestCompactionPublicPath/context.summary-guarded` (`core/compaction_public_path_test.go:179`) |
| `context.opaque-bound-to-model` | `m2-provider-compaction` | `m2-context-persisted-compaction`, model routing | done | `TestCompactionPublicPath/context.opaque-bound-to-model` (`core/compaction_public_path_test.go:179`; native-flow and foreign-flow variants) |
| `context.opaque-fallback-rejected` | `m2-provider-compaction` | Build, provider caps, fallback validation | done | `TestCompactionPublicPath/context.opaque-fallback-rejected` (`core/compaction_public_path_test.go:179`; the Build refusal in `core/build_fidelity_test.go:90`) |
| `context.compaction-audited` | `m2-context-persisted-compaction` | AuditLog and EventLog | done | `TestCompactionPublicPath/context.compaction-audited` (`core/compaction_public_path_test.go:179`) |
| `context.calibration-from-usage` | `m2-context-calibration-overflow` | ContextBudget and model Usage | done | `TestCompactionPublicPath/context.calibration-from-usage` (`core/compaction_public_path_test.go:179`) |
| `context.calibration-persisted-for-replay` | `m2-context-calibration-overflow` | checkpoints and compaction audit records | done | `TestCompactionPublicPath/context.calibration-persisted-for-replay` (`core/compaction_public_path_test.go:179`; resume leg on a fresh store) |
| `context.overflow-compacts-and-retries-once` | `m2-context-calibration-overflow` | `m2-context-persisted-compaction`, overflow classification | done | `TestCompactionPublicPath/context.overflow-compacts-and-retries-once` (`core/compaction_public_path_test.go:179`; both variants) |

Projections land before persisted compaction. Every stage preserves pending calls, tool-use/result adjacency, the full-log taint window and journal fingerprints. Provider compaction depends on persisted compaction and explicit provider caps; calibration persists the ratio it used so replay stays deterministic.

### 2.3 sandbox (15, all deferred to M2)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `sandbox.effect-derived` | `m2-sandbox-governance` | Sandbox port and shared EgressPolicy | done | `TestSandboxEffectDerived/sandbox.effect-derived` (`std/sandbox/scenarios_test.go`) |
| `sandbox.egress-denied` | `m2-sandbox-adapter-conformance` | `m2-sandbox-governance`, Docker and E2B adapters | done | `sandboxtest.Sandbox/sandbox.egress-denied` (`testkit/sandboxtest/egress_test.go`); Docker leg green against the live daemon (`adapter/docker/sandbox_conformance_test.go`, observed in `m2-docker-sandbox` task 7.2); the E2B leg stays with the open `m2-e2b-sandbox` |
| `sandbox.egress-allowlisted` | `m2-sandbox-adapter-conformance` | shared egress policy and adapter network enforcement | done | `sandboxtest.Sandbox/sandbox.egress-allowlisted` incl. the `private-range` case (`testkit/sandboxtest/egress_test.go`); Docker leg green against the live daemon; the E2B leg stays with the open `m2-e2b-sandbox` |
| `sandbox.secret-injected` | `m2-sandbox-governance` | CredentialSource and originator identity | done | `TestSandboxSecrets/sandbox.secret-injected` (`std/sandbox/scenarios_test.go`) |
| `sandbox.secret-leak-redacted` | `m2-sandbox-governance` | secret resolution and output scanning | done | `TestSandboxSecrets/sandbox.secret-leak-redacted` (`std/sandbox/scenarios_test.go`) |
| `sandbox.per-session-persists` | `m2-sandbox-lifecycle-resume` | `m2-sandbox-governance`, session metadata | done | `TestSandboxPerSessionPersists/sandbox.per-session-persists` (`std/sandbox/scenarios_test.go`) |
| `sandbox.snapshot-on-suspend` | `m2-sandbox-lifecycle-resume` | Workspace.Snapshot and checkpoint lifecycle | done | `TestSandboxSnapshotOnSuspend/sandbox.snapshot-on-suspend` (`core/drive_workspace_test.go`) |
| `sandbox.resume-restores` | `m2-sandbox-lifecycle-resume` | workspace snapshots and core Resume | done | `TestSandboxResumeRestores/sandbox.resume-restores` (`core/resume_workspace_test.go`) |
| `sandbox.replay-no-reexec` | `m2-sandbox-lifecycle-resume` | journal and Replay | done | `TestSandboxReplayNoReexec/sandbox.replay-no-reexec` (`core/sandbox_replay_test.go`) |
| `sandbox.per-call-closes` | `m2-sandbox-governance` | Workspace lifecycle | done | `TestSandboxPerCallCloses/sandbox.per-call-closes` (`std/sandbox/scenarios_test.go`) |
| `sandbox.seconds-budget` | `m2-sandbox-governance` | tree usage and RunLimits | done | `TestSandboxSecondsBudget/sandbox.seconds-budget` (`std/sandbox/scenarios_test.go`) |
| `sandbox.exec-timeout` | `m2-sandbox-adapter-conformance` | adapter process termination and effect classification | done | `sandboxtest.Sandbox/sandbox.exec-timeout` incl. the `outcome` case (`testkit/sandboxtest/timeout_test.go`); Docker leg green against the live daemon; the E2B leg stays with the open `m2-e2b-sandbox` |
| `sandbox.output-to-store` | `m2-sandbox-governance` | OutputStore and tool output caps | done | `TestSandboxOutputToStore/sandbox.output-to-store` (`std/sandbox/scenarios_test.go`) |
| `sandbox.host-exec-warning` | `m2-sandbox-governance` | existing tool/exec Build warning | done | `TestBuildHostExecWarning/sandbox.host-exec-warning` (`core/build_host_exec_test.go`) |
| `sandbox.egress-shares-policy-type` | `m2-sandbox-adapter-conformance` | shared EgressPolicy, Explain, adapter enforcement | done | `sandboxtest.Sandbox/sandbox.egress-shares-policy-type` incl. `shared-type` and `rendered-policy` (`testkit/sandboxtest/policy_test.go`, `policy.go`); Docker leg green against the live daemon; the E2B leg stays with the open `m2-e2b-sandbox` |

The port and its governance land before adapter conformance and the persisted workspace lifecycle. One adapter suite runs against both Docker and E2B and owns the shared ids once; adapters implement the suite contract and claim no duplicate coverage. `sandbox.host-exec-warning` is an inherited acceptance check: the host runner stays, and sandbox execution does not replace it.

### 2.4 flow (1, deferred to M2)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `flow.backend-swap` | `m2-eino-runtime` | runtime registration and the common Flow contract | done | `TestBackendSwap/flow.backend-swap` (`adapter/eino/backend_swap_test.go:10`, suite `testkit/conformance/backend_swap.go:9`) |

### 2.5 guards (1, deferred to M2)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `guards.origin-survives-conversion` | `m2-eino-bridges` | message origins and governed guards | done | `TestMessageBridge/guards.origin-survives-conversion` (`adapter/eino/message_bridge_test.go`; the adk-go leg of the conversion stays M3's) |

Message conversion preserves provenance before the graph and the runtime acceptance can establish governance.

### 2.6 limits (1, deferred to M2)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `limits.limits-under-foreign-backend` | `m2-eino-runtime` | core Drive and governed limit chains | done | `TestLimits/limits.limits-under-foreign-backend` (`adapter/eino/limits_test.go:122`) |

Limits stay in the harness; there is no second limit loop in an adapter.

### 2.7 identity (1, deferred to M2)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `identity.credential-not-in-sandbox` | `m2-sandbox-governance` | CredentialSource, sandbox secret resolution, output scanning | done | `TestCredentialNotInSandbox/identity.credential-not-in-sandbox` (`std/sandbox/credential_scenarios_test.go`) |

Harness credentials and explicitly authorised `SecretRef` injection are separate paths: the sandbox secret contract permits the latter and forbids arbitrary credential export.

### 2.8 stores (2 inherited checkpoint-versioning checks)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `stores.native-checkpoint-after-adapter-upgrade` | `m2-eino-graph-native-resume` | `m2-eino-runtime`, core Resume and checkpoint stores | done | `TestNativeResume/stores.native-checkpoint-after-adapter-upgrade` (`adapter/eino/einoflow/resume_test.go:63`; eino agent version-mismatch decision) |
| `stores.graph-checkpoint-incompatible` | `m2-eino-graph-native-resume` | `m2-eino-runtime`, core Resume and checkpoint stores | done | `TestNativeResume/stores.graph-checkpoint-incompatible` (`adapter/eino/einoflow/resume_test.go:78`; eino graph version-mismatch decision) |

These are existing R5n ids, not new scenarios or deferrals. The archived graph change owns the behavior. `m2-s4-acceptance` binds the re-run evidence. Mark a row `done` only after its evidence includes the accepted public-path proof. Decision-level eino subtests alone do not establish Replay telemetry or token preservation.

## 3. Milestone conflicts

Three conflicts are recorded here rather than resolved silently.

1. **E2B and the two examples.** The M2 proposal includes `adapter/e2b`, `research-report` and `data-analyst`; the overview's M2 row omits them and its M4 row lists both examples. **Resolution:** the proposal is the change's scope and the matrix above assigns them; the overview's example rows describe when an example is expected to be complete for the milestone narrative, not a second owner. `m2-e2b-sandbox`, `m2-research-report-example` and `m2-data-analyst-example` own the work; the overview is corrected when the change packages land.
2. **Guarded Summarize.** The context spec requires guarded summarization for several M2-deferred scenarios, and the overview assigns the Summarize policy deliverable to M4. **Resolution:** M2 implements the guarded behaviour those scenarios name — truncation and summarization behind the StageContext guards, with the summary itself audited; the broader policy surface (prompt, tuning, policy source) stays the M4 deliverable. A change that needs the policy surface must say so instead of importing M4 scope.
3. **`runtime.message-round-trip`.** One id names the eino and the adk-go bridge. **Resolution:** M2 proves the eino leg and records evidence for it; M3 proves the adk-go leg under the same id's requirement, without M2 claiming the whole id closed on one bridge.

## 4. Obligations without a scenario ID

| Obligation | Owner | Status | Evidence |
|---|---|---|---|
| adapter/eino runtime and conformance suites | `m2-eino-runtime` | done | `adapter/eino` suites green (`TestConformance`, `TestSuspend`, `TestResume`, `TestPreemption`, `TestHealth`); archived `m2-eino-runtime` |
| eino model/tool/message bridges | `m2-eino-bridges` | done | `TestForeignToolBridge` and `TestMessageBridge` (`adapter/eino/tool_bridge_test.go`, `adapter/eino/message_bridge_test.go`); archived `m2-eino-bridges`; the adk-go bridge leg stays M3's |
| graph flow, Native checkpoint/resume, version mismatch behaviour | `m2-eino-graph-native-resume` | done | `TestGraphFlow`, `TestGraphFlowGoverning`, `TestFromRunnableGuards`, `TestNativeResume` (`adapter/eino/einoflow`); archived `m2-eino-graph-native-resume` |
| sandbox port and `std/sandbox` governed tool set | `m2-sandbox-governance` | done | `std/sandbox` suites (`TestSandboxEffectDerived`, `TestSandboxSecrets`, `TestSandboxPerCallCloses`, `TestSandboxSecondsBudget`); archived `m2-sandbox-governance`; the `PerSession` coordinator, lifecycle and restore suites green under archived `m2-sandbox-lifecycle-resume` |
| adapter/docker | `m2-docker-sandbox` | done | `TestSandboxConformance` green against the live Docker daemon naming the four owned ids (`adapter/docker/sandbox_conformance_test.go`); tagged `adapter/docker/v1.0.0`; archived `m2-docker-sandbox` |
| adapter/e2b | `m2-e2b-sandbox` | missing | the lane is open: no module, no conformance binding and no tag; the E2B leg of the adapter suite stays blocked (`m2-sandbox-adapter-conformance` task 5.4) |
| `ProviderCompact` implementation and capability declarations | `m2-provider-compaction` | done | `TestProviderCompact` (`adapter/anthropic`, `adapter/openai`) and `TestCompactionPublicPath/context.opaque-bound-to-model`; archived `m2-provider-compaction` |
| research-report example | `m2-research-report-example` | missing | the example lane is open |
| data-analyst example | `m2-data-analyst-example` | missing | the example lane is open |
| S4 graph acceptance and conformance.* green on eino | `m2-s4-acceptance` | partial (eino green; adk-go legs M3-open) | eino suites re-run green: `adapter/eino/einoflow` `TestGraphFlow`, `TestGraphFlowGoverning`, `TestFromRunnableGuards`, `TestNativeResume` and `adapter/eino` `TestConformance` → `conformance.Runtime`, `TestBackendSwap/flow.backend-swap`, `TestLimits/limits.limits-under-foreign-backend`, `TestMessageBridge/runtime.message-round-trip`; `task spec:gate GATE=M2` now reports `missing_in_scope=0` (previously 19, all `stores.pg-*` ids of the archived `repair-postgres-store-contracts` behind `//go:build full`, all passing under `go -C adapter/postgres test -tags full -run 'TestJournalReserveAtomic|TestSessions|TestRunsRemediation|TestOutputClaimsLedger|TestNoticesRemediation|TestSessionCascadeRemediation'`); the adk-go graph leg and the adk-go bridge leg stay M3-open (`openspec/changes/m3/proposal.md:7`) |
| port-method-set compatibility check before the v1 freeze | `m2-api-freeze` | done | `task api:check` reports seven `exported API compatible` lines diffing HEAD against each module's `v1.0.0` tag, zero `no tag yet, skipped` lines; positive control exits 201 naming `types.Workspace.Close` and a reverted run is green (`m2-api-freeze` tasks 4.1-4.2; `docs/design/compatibility.md` M2 freeze section) |
| `v1.0.0` at the M2 exit | `m2-api-freeze` | done | `v1.0.0` cut first at `00075cd` after `missing_in_scope=0`, then `adapter/{anthropic,docker,eino,openai,otel,postgres}/v1.0.0`; `adapter/e2b/v1.0.0` stays conditional until the module lands; `CHANGELOG.md` carries the `## [1.0.0] - 2026-10-12` entry (`m2-api-freeze` tasks 3.2-3.4) |

The examples accept composed public paths after their features exist; their precise feature dependencies are settled in their own change designs. The two compatibility obligations come from the overview's M2 row and M1's handoff; they are not inferred scope.

## 5. Lane status

The first-wave packages are archived. m2-eino-bridges and m2-context-persisted-compaction are archived (2026-10-11 batch). The second wave is archived too: m2-eino-graph-native-resume at 12/12, m2-provider-compaction at 13/13, m2-context-calibration-overflow at 13/13, m2-sandbox-lifecycle-resume archived at 36/37 (task 6.5, the killed-pod remote cleanup case, unmet pending a durable-enumeration provider contract; the Docker contract is supplied by `types.SandboxResources` in `adapter/docker`), m2-docker-sandbox archived with the Docker conformance leg green against the live daemon, and m2-sandbox-adapter-conformance archived with the Docker leg green and the E2B leg (task 5.4) still open — O2 is closed (`chains.ToolInfo.Egress` renders the declared policy) and the change carries no spec delta because it verifies behavior the `sandbox` capability already states. m2-s4-acceptance is archived with the S4 graph and conformance suites re-run green on eino and the gate at `missing_in_scope=0`; m2-api-freeze cut `v1.0.0` and the six adapter `v1.0.0` tags at `00075cd` with `api:check` clean, `adapter/e2b` carrying no tag by the recorded conditional row. Open at the M2 exit: m2-e2b-sandbox and the two examples (`m2-research-report-example`, `m2-data-analyst-example`). `m2-s4-acceptance` records nine ids in the existing §2 subsections and the two checkpoint-versioning stores ids in §2.8; the adk-go bridge and graph legs remain M3-open, so `runtime.message-round-trip` stays open on its adk-go leg.
