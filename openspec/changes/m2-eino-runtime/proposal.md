# Proposal

## Why

M2 promises the first foreign backend (`docs/overview.md:110` — *adapter/eino: runtime, bridges, graph flow, Native resume*, exit *`conformance.*` green on eino*), and no eino code exists: `adapter/` holds `anthropic`, `openai`, `otel`, `postgres` and nothing else. The seam a backend enters through does exist — `core/runtime.Runtime` (`core/runtime/runtime.go:49-61`), `runtime.AgentRun` (`core/runtime/runtime.go:70-82`), the `preemptableRT` wrapper (`core/preemption.go:62-76`) and `WithNativeAgent` (`core/native_options.go:48-52`) — but nothing ships behind it.

The conformance suite already declares the obligation in normative text: `openspec/specs/runtime/spec.md:300-302` says `conformance.Runtime(t, newRuntime)` SHALL pass for `native`, `eino`, `adkgo`. Two ids in the registry say the same about the wider path: `flow.backend-swap` (one acceptance suite, both backends, unchanged test code) and `limits.limits-under-foreign-backend` (the same limits, the same scenarios, on an eino graph flow). Neither has a test; both are assigned here by `m2/design.md` §2.4 and §2.6.

The gap is not only a missing adapter. The seam as shipped cannot carry an adapter yet: a foreign runtime is installed through `NewConversation(stack, spec, rt, …)` (`core/conversation.go:171`), and every run path that does not take the native branch builds a bare `runtime.AgentRun` with no governed model and no tools — `core/stream_lifetime.go:152`, `core/resume.go:330`, `core/recover.go:246`. The governed wiring is reached only through `newRun`/`newResumeRun` (`core/native_conversation.go:47,82`), and both force `runtime.NewNative()` (`core/stream_lifetime.go:157`). Until that construction seam is extended, an adapter receives an empty `AgentRun` and an inert runtime, and every conformance scenario fails for a reason that has nothing to do with eino.

## What Changes

- **Added** `adapter/eino` as its own module: a `runtime.Runtime` with `StepGranularity: Turn`, so one `Step` is one eino turn and the model's own tool scheduling stays inside the graph.
- **Added** the run-construction seam in `core`: a foreign backend receives the same governed `Model`, `Tools`, `Assemble`, `History`, `Save` and driver-supplied effects a native run receives, on the send, resume and recovery paths alike. The native branch keeps `runtime.NewNative()`; the fixed foreign runtime is replaced, not supplemented.
- **Added** the conformance harness in `adapter/eino` that drives the suite `testkit/conformance.Runtime` exposes, plus the eino leg of the message round trip (`AgenticMessage`).
- **Added** `adapter/eino` to the workspace, the coverage sweep and the test/lint/race targets in `Taskfile.yml`, so `task spec:coverage` sees its subtests.

The runtime capability delta (`specs/runtime/spec.md`) is `## MODIFIED Requirements` over six existing blocks. Each body gains only the foreign-runtime clauses the contract already implies — `Drive` as the only loop, one `Step` as one effect boundary with `Turn` granularity for `eino` and `adkgo`, component events and the turn counter from the governed decorators through the run-scoped sink, `DriveResume` rebuilding state and re-executing `Step`, backend checkpoint bytes in `State.Backend` with `BackendVersion` for eino ADK and compose graphs — and every registered scenario keeps its ID and its WHEN/THEN lines byte-identically.

**BREAKING**: the run-construction seam is a new way to build a run; a flow that installed a foreign runtime through `NewConversation` before now received an empty `AgentRun`. The port stays `v1-candidate` until the freeze, so the new option is added as an optional hook whose absence keeps the current behaviour.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `runtime`: six requirements modified — *Run lifecycle ordering*, *Health*, *Graceful shutdown*, *Tool batch protocol*, *Runtime conformance*, *Component mixing* — carrying 27 `runtime.*` ids.

### Scenarios bound by this change

29 ids bound: the 27 `runtime.*` scenario blocks carried byte-identically in `specs/runtime/spec.md`, plus `flow.backend-swap` and `limits.limits-under-foreign-backend`, which stay defined in the `flow` and `limits` specs and are bound by subtest name only — this delta does not restate them. The 27 blocks fall into three evidence sets (`design.md` §7): six native scheduling rows (`runtime.batch-gate-first`, `runtime.batch-limit-before-execute`, `runtime.readonly-parallel`, `runtime.side-effects-sequential`, `runtime.parallel-tools-cap`, `runtime.sequential-tools-hint`) stay native regressions proved in `core`; the rest are eino acceptance rows proved in `adapter/eino`; `runtime.message-round-trip` is proved for the eino leg only and does not claim the id closed (`m2-eino-bridges` owns the adk-go leg).

`runtime.governed-components-in-eino-graph` names the graph flow and needs `einoflow.ModelNode`/`ToolsNode`, both outside this change's scope. Its acceptance row belongs to `m2-eino-graph-native-resume`, as `m2/design.md` §2.1 assigns; this change carries no acceptance task for it and restates the Component mixing requirement only so the scenario block stays byte-identical. This change owes that row the run-construction seam and the `runtime.Runtime` its graph runs on (`design.md` §4).

## Impact

- Code: new `adapter/eino` (module, runtime, conformance harness, `AgenticMessage` bridge); `core` gains the foreign run-construction hook on `conversation` and the registration option beside `WithNativeAgent`; `Taskfile.yml` and `go.work` gain the module.
- Contract: `runtime.Runtime` keeps its method set; `State.Backend` and `Checkpoint.BackendVersion` (`core/stores/checkpoint.go:44-45`) carry the eino checkpoint. No existing struct changes shape.
- Metrics: none new; `gohan.max_turns.reached` and the shutdown metrics stay owned by the harness.
- Dependency: `m2-eino-bridges` owns the model, tool and message bridges and owns the adk-go leg of `runtime.message-round-trip`; `m2-eino-graph-native-resume` owns the graph flow and Native resume; `m2-s4-acceptance` closes the M2 exit on this backend. `m2-api-freeze` freezes the port method set this change settles.
- Not in scope: the eino graph flow, `Native` resume and `BackendVersion` mismatch behaviour (`m2-eino-graph-native-resume`); the adk-go leg of the round trip (`m2-eino-bridges` → M3); `einoflow` node builders, which the bridge change ships.
