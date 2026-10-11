# Design: m2-s4-acceptance

## Context

The obligation is a record, not a feature: re-run the S4 graph acceptance suite and the conformance suite on eino, and bind what they prove to the milestone matrix (`openspec/changes/m2/design.md:148`, §4; `openspec/changes/m2/tasks.md:45`, item 7; `docs/overview.md:110`). Every symbol the re-run touches ships; the table separates what exists from what this change adds.

| Symbol / suite | Location | State |
|---|---|---|
| `TestGraphFlow/runtime.governed-components-in-eino-graph` | `adapter/eino/einoflow/graph_test.go:140` | ships — the S4 graph lane |
| `TestGraphFlowGoverning`, `TestFromRunnableGuards` | `adapter/eino/einoflow/graph_test.go:180,230` | ships — supporting assertions, no registry id |
| `TestNativeResume/stores.native-checkpoint-after-adapter-upgrade`, `/stores.graph-checkpoint-incompatible` | `adapter/eino/einoflow/resume_test.go:60` | ships — the `Native` resume decisions |
| `conformance.Runtime` | `testkit/conformance/runtime.go:53` | ships — the shared suite |
| `TestConformance` → `conformance.Runtime` | `adapter/eino/conformance_test.go:9` | ships — the eino binding of the suite |
| `conformance.BackendSwap` | `testkit/conformance/backend_swap.go:9` | ships — the flow acceptance suite |
| `TestBackendSwap/flow.backend-swap` | `adapter/eino/backend_swap_test.go:9` | ships |
| `TestLimits/limits.limits-under-foreign-backend` | `adapter/eino/limits_test.go:122` | ships |
| `TestMessageBridge/runtime.message-round-trip` | `adapter/eino/message_bridge_test.go:58` | ships — the eino leg only |
| adk-go bridge and graph leg | — | **missing** — M3 (`openspec/changes/m3/proposal.md:7`) |
| `Evidence` cells, §4 obligation row | `openspec/changes/m2/design.md` §2.1, §2.4, §2.6, §2.8, §4 | **empty / `missing`** — this change |
| S4 graph acceptance **record** | — | **missing** — this change |

`openspec/specs/flow/spec.md:285` (`flow.backend-swap`) states the S4 graph acceptance in the spec's own words: the acceptance suite runs against the S1 native flow and the S4 eino graph flow with the same scripted model, and both pass without changes to test code. See `proposal.md` — Why for motivation.

## Goals / Non-Goals

**Goals**

- Re-run the S4 graph acceptance suite (`adapter/eino/einoflow`) and the conformance suite (`adapter/eino`) on eino, and record each bound id's carrying subtest.
- Bind the evidence rows in `openspec/changes/m2/design.md` for the eleven ids the two suites prove, each naming its subtest.
- Record the adk-go leg of `runtime.message-round-trip` as M3-open, and never count it as covered on the eino leg.

**Non-Goals**

- No code, no test, no fixture: the suites exist and are green; this change authors no Go file.
- No spec delta: no requirement text and no scenario id changes (`skip_specs: true`).
- No new scenario id: the bound set is exactly the existing registry rows.
- `task api:check`, the port-method-set compatibility check and the `v1.0.0` tag — `m2-api-freeze`.
- The adk-go runtime, bridge and workflow flows — M3.
- Sandbox, context and example lanes — other changes own them.

## Decisions

### D1 — This change is an aggregation record; it adds no behavior and no test

This change records acceptance evidence and repairs the milestone gate's PostgreSQL test selection. The only permitted tooling edit replaces the existing PostgreSQL command in `Taskfile.yml` `spec:gate` with `go -C adapter/postgres test -json -timeout 10m -short -tags full ./...`. No Go source, test, fixture, port, stored shape, requirement, or registry entry changes.

The M2 exit criterion is *`conformance.*` green on eino; S4 (graph) green* (`docs/overview.md:110`), and the §4 row is *S4 graph acceptance and `conformance.*` green on eino*. Both name a state of the shipped suites, not new work. The eino graph suite and the `conformance.Runtime` binding already pass (graph lane 12/12; archived `2026-10-07-m2-eino-runtime`). So this change runs the suites, binds their evidence, and authors nothing else. A task in this package that would add a test, a fixture or a code path is out of scope by construction: if the re-run needed new behavior, that behavior belongs to the change that owns the row, not to the acceptance record.

*Alternative considered:* fold the record into `m2-api-freeze` — rejected: the freeze is a compatibility check against the frozen ports (`task api:check`), a different artifact and a different failure mode; the m2 plan keeps them as separate items (`openspec/changes/m2/tasks.md:45`).

### D2 - Restore the missing stores rows without changing behavior ownership

The runtime, flow, and limits rows retain their existing owners and dependencies. Add §2.8 for `stores.native-checkpoint-after-adapter-upgrade` and `stores.graph-checkpoint-incompatible`, owned by `m2-eino-graph-native-resume`. Both depend on `m2-eino-runtime`, core Resume, and checkpoint stores. This repairs the omitted matrix entries. It adds no registry id or requirement. `m2-s4-acceptance` records the carrying subtests and accepted public-path evidence without taking ownership.

### D3 — The re-run is the whole `adapter/eino` module, not an ad-hoc `-run` list

A `-run` pattern that matches no test exits 0, so a narrow pattern read as acceptance can certify nothing. Each verify below runs with `-v` and names the top-level test and subtests it expects in the output, and the coverage task runs the whole module through `tools/spec_coverage.py`, which counts each id's matches and reports a zero-match id rather than treating it as covered. The full-module run is also what `task spec:coverage` and `task spec:gate GATE=M2` already do (`Taskfile.yml`), so the record and the gate read the same evidence.

### D4 — The adk-go leg is recorded open, never inferred

`runtime.message-round-trip` names two bridges (`openspec/scenarios.json`: *cross-backend: eino `AgenticMessage` and adk-go `genai.Content`*), and §3 conflict 3 resolves the ownership: M2 proves the eino leg and records evidence for it; M3 proves the adk-go leg under the same id's requirement. This change binds the eino leg and records the adk-go leg as open in the §5 lane-status line and the change record, so no reader takes the green eino `TestMessageBridge/runtime.message-round-trip` for whole-id closure. M3 keeps the leg without M2 claiming duplicate coverage.

### D5 — No spec delta

The behavior is normative in the `runtime`, `flow`, `limits` and `stores` capability specs; this change re-runs it and binds ids that already exist. Adding a requirement would restate normative text and would need a scenario id this change is not permitted to invent, so `skip_specs: true` — the pattern the archived first-wave packages and `m2-sandbox-adapter-conformance` use.

### D6 - The M2 gate includes the PostgreSQL full-tag leg

`task spec:gate GATE=M2` aggregates every module's test JSON into `tools/spec_coverage.py --gate M2`. The gate checks all registry ids at or below M2 rank, not only the rows selected for the milestone matrix. The PostgreSQL leg uses `-short -tags full -timeout 10m`. The `full` tag includes the PostgreSQL integration tests. Short mode retains reduced workloads without excluding their scenario subtests. Docker must be available for the PostgreSQL test container. Separate PostgreSQL evidence supports diagnosis but does not replace the combined gate result. Task 4.1 requires exit 0, `missing_in_scope=0`, and `unregistered=0`. The adk-go bridge and graph legs remain M3-open.

## Risks / Trade-offs

- [A green module run is read as adk-go coverage] → §3 conflict 3 and D4: the adk-go leg is named open in the change record and the lane-status line, and the bound set carries no adk-go subtest.
- [A `-run` pattern matching zero tests passes as acceptance] → D3: every verify runs with `-v` and names its expected subtests; the coverage task reports a zero-match id instead of counting it covered.
- [The re-run silently claims an owner's row] → D2: only the `Evidence` cell is written; `Owner` and `Depends on` stay as §2 states them.
- [The evidence cell drifts from the test name] → each cell is written from the observed `-v` output, so the subtest name in the cell is the one the run printed, not a guess.
- [The record is taken for new behavior] → D1 and the proposal's *No implementation and no new test* bullet: no file under `core`, `std`, `adapter` or `examples` changes; a diff outside `openspec/changes/` is out of scope.
- [The record expands into implementation work] → Only acceptance artifacts and the PostgreSQL `spec:gate` command may change. No runtime implementation or test changes are admitted.

## Migration Plan

None. No store schema, no port, no code and no data changes. The change writes documentation and runs tests; rollback is reverting the edited artifacts, and no run needs a data migration.

## Open Questions

None.
