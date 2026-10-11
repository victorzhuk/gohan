# Proposal

## Why

M2 promises the eino graph flow and `Native` resume (`docs/overview.md:110` — *adapter/eino: runtime, bridges, graph flow, Native resume*). The first wave shipped the eino runtime and its opaque checkpoint carrier — `backendCheckpoint`, `backendCheckpointVersion` and `Runtime.restore` (`adapter/eino/runtime.go:33,82-107`) — but the two remaining pieces do not ship. No `einoflow` package exists, so `runtime.governed-components-in-eino-graph` has no implementation, and `stores.PlanResume` (`core/stores/checkpoint_compatibility.go:35`) has no caller: no resume path reads `Checkpoint.BackendVersion`, so an adapter upgrade silently re-drives a graph flow instead of refusing it. M2 is the freeze point, so the graph flow and the resume policy must settle before `m2-api-freeze`.

## What Changes

- **Added** `adapter/eino/einoflow` — the governed node builders `ModelNode(m gohan.Model)` and `ToolsNode(tools ...types.Tool)` (`openspec/specs/runtime/spec.md:189`) and `FromRunnable[In, Out]` (`openspec/specs/flow/spec.md:93`), which runs an eino `compose.Runnable` as a gohan `Flow` under a `runtime.Runtime` with `StepGranularity: Turn` and `Native` resume. `einoflow.Check` reports the components a graph carries ungoverned, where graph introspection allows.
- **Added** the `Native` resume decision to the harness: `core`'s resume path consults `stores.PlanResume` against the running adapter's `BackendVersion` before `Checkpoints.Consume`. An agent flow whose `BackendVersion` differs falls back to `Replay` and increments `gohan.resume.fallback_replay` (`openspec/specs/runtime/spec.md:90`); a graph flow refuses with `types.ErrCheckpointIncompatible` and leaves the token unconsumed.
- **Proved** `runtime.governed-components-in-eino-graph`: a graph built from `einoflow.ModelNode`/`ToolsNode` passes R3 (canonical chain) and R7 (guards) for its model and tool calls, because the node builders accept only governed components and route tool calls through `chains.ComposeToolChain`.
- **Proved** `stores.native-checkpoint-after-adapter-upgrade` and `stores.graph-checkpoint-incompatible` on the eino agent and graph flows driven through `Resume`.
- **No spec delta.** Every behavior this change lands is already normative — the `Native` resume contract (`openspec/specs/runtime/spec.md:90`), the checkpoint-compatibility scenarios (`openspec/specs/stores/spec.md:698-708`) and *Component mixing* (`openspec/specs/runtime/spec.md:350-362`). The change adds no requirement and rewrites none; `.openspec.yaml` sets `skip_specs: true`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. This change implements behavior the capability specs already describe, so no requirement text changes.

## Impact

- Code: new `adapter/eino/einoflow` package (node builders, graph flow, `Native` resume); `core/resume.go` gains the `PlanResume` consult and the fallback metric on the agent path.
- Contract: `runtime.Runtime`, `runtime.AgentRun`, `runtime.State.Backend` and `StepGranularity` keep their method sets; `stores.PlanResume`, `Checkpoint.BackendVersion` and `types.ErrCheckpointIncompatible` already ship. No port method and no stored shape is added.
- Registry: no new scenario id. The change binds `runtime.governed-components-in-eino-graph` (`openspec/changes/m2/design.md` §2.1), `stores.native-checkpoint-after-adapter-upgrade` and `stores.graph-checkpoint-incompatible`.
- Dependency: consumes `m2-eino-runtime` (archived) for the runtime and checkpoint carrier. `restore-runtime-governance` lands first because it edits `adapter/eino/runtime.go` and `core`'s governed call path. `m2-eino-bridges` owns the model/tool/message bridges and its own *Component mixing* delta; this change adds no delta, so the two do not contend over the runtime requirement.
- Not in scope: the adk-go graph leg and the adk-go bridge (M3, `openspec/changes/m3/proposal.md:7`); sandbox execution; `m2-s4-acceptance`, which closes the whole S4 graph suite; a second limit loop or an adapter-side lifecycle.
