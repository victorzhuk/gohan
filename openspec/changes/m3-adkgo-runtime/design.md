# Design: adk-go runtime backend

## Context

ADR-0002 fixes three runtimes; ADR-0061 binds the adk-go adapter to `google.golang.org/adk/v2` (`agent.Context`, graph engine, native pause/resume). The conformance clause in `openspec/specs/runtime/spec.md` (Runtime conformance) names `native`, `eino`, `adkgo`; the m2-eino-bridges change recorded the leg table with `adkgo` pending. Q3 in `docs/overview.md` 13 leaves the exact v2 API surface open and resolves the gap policy: target v2, `Replay` fallback, verify against the tag pinned in `adapter/adkgo/go.mod`. `adapter/eino` is the reference backend for structure: `runtime.go` (backend), `bridge.go` and `tool_bridge.go` (bridges), `conformance_test.go` and the flow package.

## Goals / Non-Goals

Goals:

- A third backend that passes the same conformance families as eino, with no change to existing suites or native/eino code.
- An explicit, tested fallback decision for backend capabilities adk-go v2 lacks.
- Non-goals: new `runtime` port members, a third `StepGranularity` value, changes to frozen method sets, adk-go workflow features beyond what the flows need.

## Decisions

1. **Module shape mirrors eino.** `adapter/adkgo` is its own module (`github.com/victorzhuk/gohan/adapter/adkgo`) with `runtime.go`, `bridge.go`, `tool_bridge.go` and a flow file. The backend implements the frozen `runtime.Runtime`/`runtime.AgentRun` surface with `StepGranularity: Turn`, declared through `Runtime.Granularity` and printed by `Explain`.
2. **Conformance through the shared suite, not a private copy.** The backend plugs into `testkit/conformance` (`conformance.Runtime`, message-fidelity leg table, foreign-tool codecs) exactly as eino does. `go.mod` pins the adk-go v2 tag named by Q3; the tasks verify behavior against that tag, not against docs.
3. **Q3 fallback rule: bridge what the backend owns, Replay what it lacks.** adk-go v2 owns graph execution and native pause/resume, so suspension and resume cross into the backend's own mechanism (ADR-0012). Where the backend carries no mechanism for a gohan capability (for example journal fingerprinting or run-notices on a backend step), the runtime records the step result and `Replay` re-executes `Step` over recorded results per `engines.replay-is-step-re-execution`. The fallback is declared per capability in the adapter and proven by a scenario, never silently.
4. **Bridge contract takes codecs as values.** As in m2-eino-bridges D6, the shared fidelity contract consumes `EncodeMessages`/`DecodeMessages` as function values and names no adk-go type, keeping `testkit/conformance` free of the adk-go import.
5. **No port growth.** If the backend seems to need a new `runtime.AgentRun` member, that is the two-shipped-backends rule from the freeze: `adkgo` is the third, so a field may be added only if `native` and `eino` both need it too; otherwise it is an optional interface and out of scope here.
6. **Runtime conformance body carries the rule, design keeps the names.** The compressed requirement names no concrete event kinds; the governed component-level events are `TextDelta`, `ToolStarted` and `ToolFinished`, all sourced from the decorators through the run-scoped sink, with the turn counter owned by `Drive`.
7. **Relocated spec detail (body compression).** The rationale moved here from the `Runtime conformance` body: backends MUST NOT emit component events or count turns because the decorators source events through the run-scoped sink in `ctx` and `Drive` owns the turn limit; no obligation is dropped.

## Risks / Trade-offs

- The pinned v2 tag may differ from published examples (Q3 notes disagreeing sources); all verification cites the tag in `adapter/adkgo/go.mod`, and a tag bump is a one-line change with the conformance suite as the arbiter.
- `Replay` fallback can mask a missing backend mechanism; the declaration-per-capability rule and the fallback scenario keep it observable.
- Two children (this and `m3-redis-stores`) are independent; no shared-file serialization applies except the task-1.1 registration of scenario ids, which each child performs at admission.

## Migration Plan

Authoring-only change. Implementation is additive: a new module, new conformance legs, no existing test or spec text rewritten beyond the delta. At admission, task 1.1 registers the scenario ids in `openspec/scenarios.json` (proposed until admission, ADR-0171 pattern) and regenerates `docs/design/types.md`; shared-file serialization with sibling M3 children applies then.
