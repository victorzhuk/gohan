# Proposal: m2-s4-acceptance

## Why

`openspec/changes/m2/design.md:148` carries one obligation without a scenario id: *S4 graph acceptance and `conformance.*` green on eino*, owner `m2-s4-acceptance`. `docs/overview.md:110` puts the same thing at the M2 exit — *`conformance.*` green on eino; S4 (graph) green*. Every piece that satisfies it ships: the eino graph flow and its `Native` resume prove `runtime.governed-components-in-eino-graph`, `stores.native-checkpoint-after-adapter-upgrade` and `stores.graph-checkpoint-incompatible` in `adapter/eino/einoflow` (archived `2026-10-11` lanes, graph lane 12/12), and the eino runtime proves the `conformance.Runtime` rows through `adapter/eino/conformance_test.go` (archived `2026-10-07-m2-eino-runtime`).

What is missing is the record. No change re-runs the two suites on one backend and binds their evidence to the rows they prove, so the milestone matrix Evidence column stays empty, the §4 obligation row stays `missing`, and the M2 exit has nothing naming the subtest that carries each id. The milestone definition of done requires exactly that naming (`openspec/changes/m2/tasks.md`, *Milestone definition of done*), and `m2-api-freeze` and `v1.0.0` land on top of it.

This is why now: the freeze is last in the implementation order (`openspec/changes/m2/tasks.md:45`), and it must freeze a backend whose acceptance is recorded rather than asserted.

## What Changes

- **Re-run the S4 graph acceptance suite on eino** — `adapter/eino/einoflow` graph and resume tests — and record the graph flow's run and `Native` resume decisions.
- **Re-run the conformance suite on eino** — `adapter/eino/conformance_test.go` (`conformance.Runtime` from `testkit/conformance`), plus the sibling acceptance legs in the same module: `flow.backend-swap`, `limits.limits-under-foreign-backend` and the eino leg of `runtime.message-round-trip`.
- **Bind the evidence rows in the milestone matrix** — fill the `Evidence` cells of the `openspec/changes/m2/design.md` rows this re-run proves (§2.1 runtime, §2.4 flow, §2.6 limits), each naming the subtest that carries its id, and move the §4 obligation row off `missing`.
- **Record the adk-go leg as M3-open** — `runtime.message-round-trip` names the eino and the adk-go bridge (`openspec/changes/m2/design.md` §3 conflict 3). M2 proves and records the eino leg only; the adk-go leg stays open for M3 and is counted as open, never as covered.
- **No spec delta.** No behavior lands, so no requirement text and no scenario id changes; `.openspec.yaml` sets `skip_specs: true`. Every id this change binds already exists in `openspec/scenarios.json`.
- **No implementation and no new test.** The suites exist and are green; this change aggregates their evidence and authors nothing under `core`, `std`, `adapter` or `examples`.

## Capabilities

### New Capabilities

- (none)

### Modified Capabilities

- (none)

`skip_specs: true`: the change adds no requirement and rewrites none. It re-runs behavior the `runtime`, `flow`, `limits` and `stores` capabilities already state normatively and binds the existing scenario ids to it.

## Impact

- Artifacts: `openspec/changes/m2-s4-acceptance/{proposal,design,tasks}.md`; the `Evidence` cells of `openspec/changes/m2/design.md` §2.1 (the runtime rows the eino acceptance suites carry), §2.4 (`flow.backend-swap`), §2.6 (`limits.limits-under-foreign-backend`), the §4 obligation row, and the §5 lane-status line.
- Code: none. No file under `core`, `std`, `adapter` or `examples` changes.
- Registry: no new scenario id. The bound set is eleven existing ids — three in the graph lane, seven in the conformance lane, one recorded open for M3.
- Ports: `runtime.Runtime`, `runtime.AgentRun`, `runtime.State.Backend`, `StepGranularity`, `stores.PlanResume` and the `einoflow` builders keep their shapes; this change reads them and adds nothing, so the `m2-api-freeze` surface is untouched.
- Dependencies: `m2-eino-graph-native-resume` (the graph lane it re-runs) and `m2-eino-runtime` (archived; the conformance rows it re-runs). Both ship, so nothing blocks the re-run.
- Not in scope: `m2-api-freeze`'s `task api:check` and the `v1.0.0` tag; the adk-go graph and bridge legs (`openspec/changes/m3/proposal.md:7`); any sandbox, context or example lane.
