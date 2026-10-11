# Design

## Context

The eino runtime and its checkpoint carrier ship (archived `2026-10-07-m2-eino-runtime`). The table separates what ships from what this change adds, each bound to a symbol read for this package.

| Symbol | Location | State |
|---|---|---|
| `runtime.Runtime` / `AgentRun` / `State.Backend` / `StepGranularity` | `core/runtime/runtime.go` | ships — `GranularityTurn` is the eino value; `State.Backend` is the opaque checkpoint carrier |
| `stores.PlanResume` / `FlowKind` / `ResumePlan` | `core/stores/checkpoint_compatibility.go:35` | ships — but **no caller**: no resume path reads `BackendVersion` |
| `stores.Checkpoint.Backend` / `BackendVersion` / `Flow` | `core/stores/checkpoint.go` | ships — `Flow` is a string, `BackendVersion` records the writer's adapter version |
| `types.ErrCheckpointIncompatible` | `core/types/errors.go:39` | ships |
| `backendCheckpoint` / `backendCheckpointVersion` / `Runtime.restore` | `adapter/eino/runtime.go:33,82-107` | ships — the versioned opaque envelope an eino run checkpoints |
| `chains.ComposeToolChain` | `core/chains/chain.go:97` | ships — the governed chain a tool call MUST traverse |
| `einoflow.ModelNode` / `ToolsNode` / `FromRunnable` / `Check` | — | **missing** — this change |
| `Resume` native branch | `core/resume.go:221` | ships — decodes the backend record without consulting `BackendVersion` |

The runtime spec fixes the contract this change implements: `Native` resume keeps backend bytes in `State.Backend` with `BackendVersion`; a differing version makes an agent flow fall back to `Replay` (`gohan.resume.fallback_replay`) and a graph flow return `ErrCheckpointIncompatible` (`openspec/specs/runtime/spec.md:90`). `Component mixing` requires the same chains for a foreign graph as for the native runtime (`openspec/specs/runtime/spec.md:350`). See `proposal.md` — Why for motivation.

## Goals / Non-Goals

**Goals**

- Ship the `einoflow` node builders and the graph flow, and prove `runtime.governed-components-in-eino-graph` on the public path.
- Wire the `Native` resume decision so `BackendVersion` mismatch is honoured: agent flows replay, graph flows refuse, and the token is untouched on refusal.
- Prove `stores.native-checkpoint-after-adapter-upgrade` and `stores.graph-checkpoint-incompatible` on the eino flows.

**Non-Goals**

- The adk-go graph leg and the adk-go bridge — M3 (`openspec/changes/m3/proposal.md:7`).
- Rebuilding the eino runtime, the model/tool/message bridges or the checkpoint envelope — all shipped.
- A second limit loop, an adapter-side lifecycle or adapter-emitted component events; `Drive` keeps every one of them (archived `m2-eino-runtime` design, §4).
- The whole S4 acceptance suite — `m2-s4-acceptance` closes it on this backend.
- A spec delta: runtime §6.14 and stores R5n already carry the behavior normatively.

## Decisions

**D1 — `einoflow` lives inside the eino module, not `core`.** The package path is `adapter/eino/einoflow` (module `github.com/victorzhuk/gohan/adapter/eino/einoflow`). It depends on eino `compose` and `schema`, and `core` MUST NOT import eino, so the builders cannot live beside the governed components they wrap. *Alternative considered:* a fifth top-level module — rejected: one package does not justify a module, and it would duplicate the eino version pin.

**D2 — A graph runs as a `runtime.Runtime`, not beside the loop.** `FromRunnable[In, Out](stack, r, opts...)` presents an eino `compose.Runnable[In, Out]` as a gohan `Flow` whose `runtime.Runtime` declares `GranularityTurn`. One `Step` is one graph run, so `Drive` still owns limits, events, the journal, checkpoint publication, preemption and `Done`-after-`Finish`. An adapter-side turn counter, retry loop or lifecycle is refused, exactly as for the turn runtime (archived design, §4). *Alternative considered:* drive the graph directly from the flow constructor — rejected: it creates the second loop the milestone boundary forbids.

**D3 — Governed node builders are the only supported entry.** `ModelNode(m gohan.Model)` and `ToolsNode(tools ...types.Tool)` accept governed components (`openspec/specs/runtime/spec.md:189`), and every tool node routes its call through `chains.ComposeToolChain` (`core/chains/chain.go:97`). A node built any other way is ungoverned, and `einoflow.Check` reports it where graph introspection allows. This is what makes the graph scenario's THEN true: the graph's tool round trip runs R3 (canonical chain) and R7 (guards), the same chains the native runtime runs (`openspec/specs/runtime/spec.md:362`).

**D4 — The `Native` resume decision is a harness decision, made before the token is spent.** `core`'s resume path resolves the checkpoint's flow kind, computes the running adapter's `BackendVersion`, and calls `stores.PlanResume(cp, kind, version)` before `Checkpoints.Consume`. `PlanResume` reads nothing mutable and consumes no token (`core/stores/checkpoint_compatibility.go:32-34`), so the harness applies the plan only after it returns no error:

- `ResumeNative` — resume in place as today; an eino graph decodes `State.Backend`.
- `ResumeReplay` — re-drive via `Replay` and increment `gohan.resume.fallback_replay` once.
- On `ErrCheckpointIncompatible` — return the error, consume nothing, so the token stays usable.

*Alternative considered:* let the adapter refuse a mismatched version itself — rejected: the adapter never sees the token, and two refusal sites would double-count.

**D5 — The flow kind is declared at registration, not stored.** `Checkpoint` carries no kind field, and adding one would be a stored-shape change (a spec change). The kind resolves from the flow registration that owns the checkpoint — `FlowAgent` for an agent flow, `FlowGraph` for an `einoflow.FromRunnable` graph — through the same registry that built the run. An unknown flow is an error, never a silent default, because defaulting a graph to `FlowAgent` would replay it instead of refusing it, which the spec forbids.

**D6 — A graph flow refuses rather than decodes on a version mismatch.** The `einoflow` runtime consumes the token only after `PlanResume` returns `ResumeNative`; a mismatch is refused before `Runtime.restore` sees the bytes. An agent flow keeps the shipped fallback: it replays from the journal when the backend bytes do not decode, and `gohan.resume.fallback_replay` counts that once.

**D7 — No spec delta.** The behavior is normative in runtime §6.14, stores R5n and *Component mixing*; this change implements it and binds the existing scenario ids. Adding a requirement would restate normative text and require a scenario id this change is told not to invent.

## Risks / Trade-offs

- [A second loop creeps in through convenience] → the graph flow is a `runtime.Runtime`; `Drive` owns limits, turns and events. The tell is a metric or event only the adapter can emit; it is refused.
- [A graph node bypasses the gate] → node builders accept only governed components, tool nodes compose `chains.ComposeToolChain`, and `einoflow.Check` reports ungoverned components where introspection allows.
- [A version mismatch silently re-drives a graph] → `PlanResume` runs before `Consume`, and a `FlowGraph` mismatch returns `ErrCheckpointIncompatible` with the token unconsumed (D4, D6).
- [The fallback metric double-counts] → exactly one increment per `ResumeReplay` plan application; the plan is computed once per resume.
- [The flow kind is guessed] → the kind resolves from the flow registration; an unknown flow errors instead of defaulting (D5).
- [eino compose surface drift] → Q4 (`docs/overview.md:122`) flags graph introspection as unverified, so resume stays on opaque bytes plus a version rather than a typed graph handle; `einoflow.Check` degrades where introspection is unavailable.

## Migration Plan

No store schema change and no port change. The resume path gains one decision that is a no-op when `BackendVersion` matches or is empty; `PlanResume` returns `ResumeNative` in that case. Rollback = stop consulting `PlanResume`: a refusal leaves the token unconsumed, so a run refused under the new path resumes under the old one unchanged. The agent fallback path already exists via `DriveResume`/`Replay`, so no run needs a data migration.

## Open Questions

None.
