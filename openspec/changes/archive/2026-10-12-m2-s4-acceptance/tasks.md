# Tasks: m2-s4-acceptance

A task is done when its listed scenario ids have a passing subtest named exactly the id and its `verify:` command is green. Every id below exists in `openspec/scenarios.json`; no id is proposed and none is invented. Every `verify` runs with `-v`, because a `-run` pattern that matches no test exits 0 and would read as acceptance.

This change authors no Go file. The two suites ship and are green; the tasks re-run them on eino and bind the result. The bound set is eleven existing ids in three sets: **(A)** the S4 graph acceptance suite in `adapter/eino/einoflow` — three ids; **(B)** the conformance suite in `adapter/eino` — seven ids; **(C)** the eino leg of `runtime.message-round-trip`, recorded with the adk-go leg left open for M3 (`design.md` D4; `openspec/changes/m2/design.md` §3 conflict 3).

Rows keep their §2 owners (`openspec/changes/m2/design.md` §2.1, §2.4, §2.6, §2.8); this change writes only the `Evidence` cell of each row it proves (`design.md` D2).

## 1. The S4 graph acceptance suite on eino (evidence set A)

- [x] 1.1 Run the S4 graph acceptance suite in `adapter/eino/einoflow` and confirm the graph flow runs under the harness with `turn` granularity. Verify `timeout 300s go -C adapter/eino test -short -timeout 2m -v -run 'TestGraphFlow|TestFromRunnableGuards' ./einoflow/...` — `TestGraphFlow`, `TestGraphFlowGoverning` and `TestFromRunnableGuards` appear in the output and pass.
  - evidence: `adapter/eino/einoflow/graph_test.go:140,180,230` (the three top-level tests); the observed `-v` output.
- [x] 1.2 Bind `runtime.governed-components-in-eino-graph` and write its row's `Evidence` cell. The row's owner is `m2-eino-graph-native-resume` (`openspec/changes/m2/design.md` §2.1); this change re-runs and records, it does not rebind. Verify `timeout 300s go -C adapter/eino test -short -timeout 2m -v -run 'TestGraphFlow/runtime.governed-components-in-eino-graph' ./einoflow/...` — the subtest is named in the output and passes.
  - evidence: `adapter/eino/einoflow/graph_test.go:141` (`TestGraphFlow/runtime.governed-components-in-eino-graph`); `Evidence` cell of the `runtime.governed-components-in-eino-graph` row; `openspec/scenarios.json` (id `runtime.governed-components-in-eino-graph`, capability `runtime`, origin `R13`).
- [x] 1.3 Bind the two `Native` resume rows on the eino flows and write their `Evidence` cells: `stores.native-checkpoint-after-adapter-upgrade` and `stores.graph-checkpoint-incompatible`. Verify `timeout 300s go -C adapter/eino test -short -timeout 2m -v -run 'TestNativeResume' ./einoflow/...` — both subtests are named in the output and pass, alongside `same_version_resumes_natively`.
  - evidence: `adapter/eino/einoflow/resume_test.go:60` (`TestNativeResume/`, the two named subtests); `Evidence` cells of the two `stores` rows; `openspec/scenarios.json` (both ids, capability `stores`, origin `R5n`).

## 2. The conformance suite on eino (evidence set B)

- [x] 2.1 Run `conformance.Runtime` on eino and bind its five rows. Verify `timeout 300s go -C adapter/eino test -short -timeout 2m -v -run TestConformance ./...` — `TestConformance` runs `conformance.Runtime` and the subtests `runtime.plain-answer`, `runtime.tool-round-trip`, `runtime.parallel-calls-ordering`, `runtime.max-turns` and `runtime.cancellation` are named in the output and pass.
  - evidence: `adapter/eino/conformance_test.go:9` (`TestConformance` → `conformance.Runtime`); `testkit/conformance/runtime.go:53` (the shared suite); `Evidence` cells of the five `runtime` rows; `openspec/scenarios.json` (five ids, capability `runtime`, origin `R2`).
- [x] 2.2 Run the sibling acceptance legs in the same module and bind `flow.backend-swap` and `limits.limits-under-foreign-backend`. Verify `timeout 300s go -C adapter/eino test -short -timeout 2m -v -run 'TestBackendSwap|TestLimits' ./...` — `TestBackendSwap/flow.backend-swap` and `TestLimits/limits.limits-under-foreign-backend` are named in the output and pass.
  - evidence: `adapter/eino/backend_swap_test.go:9` (`TestBackendSwap/flow.backend-swap`); `adapter/eino/limits_test.go:122` (`TestLimits/limits.limits-under-foreign-backend`); `Evidence` cells of the §2.4 and §2.6 rows; `openspec/scenarios.json` (both ids, `deferred_to: M2`).
- [x] 2.3 Run the eino leg of `runtime.message-round-trip` and record it as leg-only evidence (set C), never as whole-id closure. Verify `timeout 300s go -C adapter/eino test -short -timeout 2m -v -run 'TestMessageBridge/runtime.message-round-trip' ./...` — the subtest is named in the output and passes.
  - evidence: `adapter/eino/message_bridge_test.go:58` (`TestMessageBridge/runtime.message-round-trip` — eino leg); `design.md` D4 and §3 conflict 3 (the split); `openspec/scenarios.json` (id `runtime.message-round-trip`, cross-backend).
- [x] 2.4 Confirm every bound id matches a non-zero subtest count. Verify `timeout 600s go -C adapter/eino test -json -timeout 2m -short ./... | python3 tools/spec_coverage.py --registry openspec/scenarios.json` — each of the eleven bound ids is reported covered with a non-zero count, and no bound id is reported as a zero match.
  - evidence: the per-id coverage output; `tools/spec_coverage.py`.

## 3. Bind the record in the milestone matrix

- [x] 3.1 Fill the `Evidence` cells of the rows the re-run proves in `openspec/changes/m2/design.md`: the five `runtime` rows and `flow.backend-swap` (§2.1, §2.4), `limits.limits-under-foreign-backend` (§2.6), `runtime.governed-components-in-eino-graph` (§2.1) and the two `stores` rows (§2.8). Each cell names the file and the subtest that carries the id; `Owner` and `Depends on` stay as §2 states them. Verify the cell text for each of the eleven ids names a subtest that appears in the §1–§2 run output.
  - evidence: `openspec/changes/m2/design.md` §2.1, §2.4, §2.6, §2.8 `Evidence` cells; `design.md` D2.
- [x] 3.2 Move the §4 obligation row *S4 graph acceptance and `conformance.*` green on eino* off `missing` and record the adk-go leg as open: the §5 lane-status line names the S4 graph and conformance suites green on eino, and names the adk-go leg of `runtime.message-round-trip` (and the adk-go graph leg) M3-open. Do not mark `runtime.message-round-trip` closed. Verify the §4 row carries a non-empty `Evidence` cell naming the two suites and the §5 line names the adk-go leg open.
  - evidence: `openspec/changes/m2/design.md:148` (§4 row) and §5 lane status; `openspec/changes/m3/proposal.md:7` (the adk-go owner); `design.md` D4.

## 4. Gate and package validation

- [x] 4.1 Report the milestone gate: `timeout 1800s task spec:gate GATE=M2` reports `missing_in_scope=0`. Record the S4 half of the M2 exit as the two suites this change re-runs; `task api:check` and the `v1.0.0` tag stay `m2-api-freeze`'s. Verify the gate output names `missing_in_scope=0`.
  - evidence: `timeout 1500s task spec:gate GATE=M2` → `summary registered=663 subtests=2286 covered=568 missing=95 missing_in_scope=0 unregistered=0`; the `task spec:gate GATE=M2` output; `Taskfile.yml` (`spec:gate`); `docs/overview.md:110`; `design.md` D6.
- [x] 4.2 Validate the change package. Verify `timeout 60s openspec validate m2-s4-acceptance --strict --no-interactive` exits 0, and `.openspec.yaml` carries `skip_specs: true` because no requirement text changes.
  - evidence: `Change 'm2-s4-acceptance' is valid`.

## Workflow follow-up

- Archive the change after the project's review requirements are satisfied. No delta syncs: `skip_specs` is set and no requirement changes.
- `m2-api-freeze` follows: it diffs every module's exported surface against its last tag (`task api:check`) and tags `v1.0.0`. This change touches no port, so the freeze surface is unchanged by it.
- The adk-go leg of `runtime.message-round-trip` and the adk-go graph flow stay M3's (`openspec/changes/m3/proposal.md:7`); the leg is recorded open and is never counted as covered here.
