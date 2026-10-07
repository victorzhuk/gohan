# Tasks

Every task names the scenario ids it binds. `verify` is the command that proves it; the evidence line names the file and the symbol or subtest the evidence lands in.

## 1. Record the seam and the boundary

- [ ] 1.1 Extend the run-construction seam in `core` before any adapter work: a foreign builder beside `newRun`/`newResumeRun` on `conversation`, reached on the send, resume and recovery paths, and the native branch keeps `runtime.NewNative()`. Verify: `go test -short -timeout 2m ./core/... -run TestForeignRunConstruction` — a flow with a foreign runtime receives an `AgentRun` carrying `Model`, `Tools`, `Assemble`, `History`, `Save` on all three paths, and the native path's behaviour is unchanged.
  - evidence: `core/conversation.go` (the foreign builder field beside `newRun`/`newResumeRun`), `core/stream_lifetime.go:150-157` (the branch that forced `runtime.NewNative()` no longer does for the foreign path), `core/resume.go:330`, `core/recover.go:246`, `core/foreign_run_construction_test.go` (`send`, `resume`, `recovery` subtests).
- [ ] 1.2 Add the registration option beside `WithNativeAgent` (`core/native_options.go:48-52`) so a flow declares a foreign runtime at `Build` time, with no new method on `runtime.Runtime`. Verify: `go test -short -timeout 2m ./core/... -run TestForeignRuntimeRegistration`; `task api:check` reports no incompatible change to `runtime.Runtime`, `runtime.AgentRun` or `runtime.State`.
  - evidence: `core/native_options.go` (the option next to `WithNativeAgent`), `core/build.go` (resolution), `core/foreign_runtime_registration_test.go` (`foreign_runtime_registers_once`, `duplicate_registration_refused`).
- [ ] 1.3 Record the port method set to freeze: `runtime.Runtime`, `runtime.AgentRun`, `runtime.State.Backend`, `StepGranularity`, and `Granularity()` reach `Explain`. Verify: `design.md` §3 names all five and `task api:check` observes no method added or removed on the four ports.
  - evidence: `openspec/changes/m2-eino-runtime/design.md` §3; `core/chains/explain.go:54` (`Explanation.Granularity`).
- [ ] 1.4 Add `adapter/eino` to `go.work`, the coverage sweep in `Taskfile.yml` (test, lint, race, `-json`) and `tools/package_map.json`. Verify: `task spec:coverage` counts subtests from the adapter module; `go -C adapter/eino test -short -timeout 2m ./...` runs.
  - evidence: `go.work`, `Taskfile.yml`, `tools/package_map.json`.

## 2. The eino runtime

- [ ] 2.1 Implement `adapter/eino`'s `runtime.Runtime` over `adk.ChatModelAgent` + `adk.NewRunner`, with `Start`, `Step`, `Name` and `Granularity() StepGranularity` returning `GranularityTurn`. Verify: a compile-time assertion that the type satisfies `runtime.Runtime`, and `go test -short -timeout 2m ./...` in the module.
  - evidence: `adapter/eino/runtime.go` (`Runtime`, `Start`, `Step`, `Name`, `Granularity`), `adapter/eino/runtime_test.go`.
- [ ] 2.2 One `Step` advances one eino turn: the adapter hands the graph the governed request and returns the advanced `State`, the runtime-originated events and the `Status`. Verify: `runtime.plain-answer`, `runtime.tool-round-trip`, `runtime.parallel-calls-ordering`, `runtime.max-turns`, `runtime.cancellation` pass through the conformance harness — `go -C adapter/eino test -short -timeout 2m -run TestConformance ./...`.
  - evidence: `adapter/eino/runtime_test.go` (`runtime.plain-answer`, `runtime.tool-round-trip`, `runtime.parallel-calls-ordering`, `runtime.max-turns`, `runtime.cancellation`).
- [ ] 2.3 Propagate `ctx` unchanged into every component call, so the run-scoped sink, the cancel shield and the limits ledger all reach the model and the tools. Verify: `runtime.cancellation` observes `context.Canceled` on the stream and no component runs after it; a test that cancels mid-turn leaves no goroutine behind through the suite's `LeakCheck`.
  - evidence: `adapter/eino/runtime.go` (the ctx passed to `Runner.Run` and to every node), `adapter/eino/runtime_test.go` (`runtime.cancellation`, `run_leaves_no_goroutine`).
- [ ] 2.4 No second loop: the adapter counts no turns and checks no limit. Verify: `runtime.max-turns` observes `Done(StopLimit)` after three model calls and `gohan.max_turns.reached` increments once; a grep of `adapter/eino` for a limit check finds none.
  - evidence: `adapter/eino/runtime_test.go` (`runtime.max-turns`, `gohan.max_turns.reached`), the absence of any `MaxTurns` read in `adapter/eino`.
- [ ] 2.5 No adapter-emitted component events: `TextDelta`, `ToolStarted` and `ToolFinished` arrive from the governed decorators through the sink `Drive` installs. Verify: the `runtime.plain-answer` event stream carries exactly one `TextDelta` per model delta and no duplicate, and the adapter's returned events are runtime-originated only.
  - evidence: `adapter/eino/runtime_test.go` (`component_events_come_from_the_sink`, `step_returns_runtime_events_only`).
- [ ] 2.6 Make `Explain` print the backend granularity. Verify: `Stack.Explain` on an eino flow reports `turn` where the native flow reports `effect`; `go test -short -timeout 2m ./core/... -run TestExplainGranularity`.
  - evidence: `core/explain.go:47` (the granularity the explanation reports), `adapter/eino/explain_test.go` (`eino_reports_turn_granularity`).

## 3. Suspension, preemption and health

- [ ] 3.1 Translate a component's suspend signal into the backend's mechanism and back into `Suspended`, and prove the ordering `runtime.suspend-order` names. Verify: `runtime.suspend-order` observes the assistant message with the pending `ToolUse` in `SessionLog`, the checkpoint, `Runs` showing `Suspended` with the token, and only then the `Suspended` event.
  - evidence: `adapter/eino/runtime.go` (`adk.StatefulInterrupt` mapped to the harness suspend), `adapter/eino/runtime_test.go` (`runtime.suspend-order`).
- [ ] 3.2 `DriveResume` on the eino path: rebuild `State` from the checkpoint, append the resume input, re-execute `Step` from there, and answer completed calls from the journal. Verify: `runtime.batch-ask-after-allowed` and `runtime.batch-one-result-per-call` pass on the eino path — the approved call executes, the rejected one reads `not_executed: rejected by <approver>`, and no completed call re-executes.
  - evidence: `adapter/eino/runtime.go` (`Step` re-entry from restored state), `adapter/eino/runtime_test.go` (`runtime.batch-ask-after-allowed`, `runtime.batch-one-result-per-call`, `completed_calls_answer_from_the_journal`).
- [ ] 3.3 Carry the backend checkpoint bytes in `State.Backend` and let gohan consume the token before calling the backend's resume. Verify: a suspend persists the eino checkpoint bytes under `Checkpoint.BackendVersion` and a resume consumes the token first; `Runtime.Granularity` stays `Turn` across the round trip.
  - evidence: `adapter/eino/runtime.go`, `adapter/eino/runtime_test.go` (`backend_checkpoint_bytes_survive_the_round_trip`).
- [ ] 3.4 Reach preemption only through the wrapped runtime, on the send, resume and recovery paths. Verify: `runtime.shutdown-rejects-new`, `runtime.shutdown-preempts-at-safe-point`, `runtime.shutdown-side-effect-completes`, `runtime.shutdown-partial-model-call-dropped`, `runtime.preempted-resume-continue`, `runtime.shutdown-grace-exhausted` pass with an eino-driven run — the shielded side effect completes and appends, the partial model call is dropped with nothing appended, and the resumed run re-issues it for the same turn.
  - evidence: `adapter/eino/preemption_test.go` (`runtime.shutdown-rejects-new`, `runtime.shutdown-preempts-at-safe-point`, `runtime.shutdown-side-effect-completes`, `runtime.shutdown-partial-model-call-dropped`, `runtime.preempted-resume-continue`, `runtime.shutdown-grace-exhausted`), `core/preemption.go:62-76` (the wrapper the foreign runtime runs through).
- [ ] 3.5 Prove the backend cannot keep the stack ready. Verify: `runtime.readyz-false-on-schema-skew` and `runtime.readyz-false-during-shutdown` pass with an eino-driven run draining; `/readyz` answers 503 and `/healthz` 200 until exit.
  - evidence: `adapter/eino/health_test.go` (`runtime.readyz-false-on-schema-skew`, `runtime.readyz-false-during-shutdown`), the `Ready` assertion in `core/shutdown_test.go` the adapter test reuses.

## 4. Cross-backend parity and mixing

- [ ] 4.1 Run the same acceptance suite against both backends with one scripted model and no test-code change. Verify: `flow.backend-swap` passes — `go -C adapter/eino test -short -timeout 2m -run TestBackendSwap ./...` and the native run of the same suite file.
  - evidence: `adapter/eino/backend_swap_test.go` (`flow.backend-swap`), the shared suite file it and the native flow both run.
- [ ] 4.2 Run the limits scenarios on an eino graph flow. Verify: `limits.limits-under-foreign-backend` passes — the hard-cost abort, the wall clock, the provider-call count and the monotonic wall clock behave as they do on the native flow.
  - evidence: `adapter/eino/limits_test.go` (`limits.limits-under-foreign-backend`, and the underlying `limits.*` assertions it runs).
- [ ] 4.3 Keep the native batch scheduling rows as native regressions. Verify: `runtime.batch-gate-first`, `runtime.batch-limit-before-execute`, `runtime.readonly-parallel`, `runtime.side-effects-sequential`, `runtime.parallel-tools-cap`, `runtime.sequential-tools-hint` stay green in `core` on the native path, and no adapter test asserts native scheduling inside eino.
  - evidence: `core/runtime_batch_test.go`, `core/runtime_schedule_test.go` (the six native subtests), `design.md` §4 (the rule that forbids the adapter assertion).
- [ ] 4.4 Prove the mixing directions. Verify: `runtime.foreign-tool-under-native` and `runtime.governed-components-in-eino-graph` pass — an imported eino `InvokableTool` runs the full chain under the native runtime, and a graph built with `einoflow.ModelNode`/`ToolsNode` runs the plain-answer and tool-round-trip scenarios for that flow.
  - evidence: `testkit/conformance/runtime_test.go` (`runtime.foreign-tool-under-native`), `adapter/eino/graph_mixing_test.go` (`runtime.governed-components-in-eino-graph`).
- [ ] 4.5 Prove the eino leg of the message bridge, and do not claim the id. Verify: `runtime.message-round-trip` passes for eino — a `Message` with reasoning, text, image, document, tool use and `Raw` blocks survives `*schema.AgenticMessage` and back in block order at the adapter's declared fidelity, `Reasoning` only through its own provider. The adk-go leg stays with `m2-eino-bridges`.
  - evidence: `adapter/eino/message_bridge_test.go` (`runtime.message-round-trip` — eino leg), `design.md` §5 (the split).

## 5. Integration verification

- [ ] 5.1 Run the whole bound set through the public path. Verify: `task lint` clean, `task test` (`-short`) green across the workspace including `adapter/eino`, and `task spec:coverage` reports no missing id among the 29.
  - evidence: `task lint`; `task test`; `task spec:coverage` (`missing_in_scope=0`, `unregistered=0`).
- [ ] 5.2 Validate the change package and the delta. Verify: `openspec validate m2-eino-runtime --strict --no-interactive` exits 0, the delta keeps all 29 ids byte-identical to `openspec/specs/{runtime,flow,limits}/spec.md`, and every scenario id exists in `openspec/scenarios.json`.
  - evidence: `Change 'm2-eino-runtime' is valid`; a diff of each scenario block against the main spec showing no changed WHEN/THEN line.

## Workflow follow-up

- Archive the change after the project's review requirements are satisfied; the delta syncs into `openspec/specs/runtime/spec.md` and the types index regenerates.
- Hand the two owned-but-unclaimed rows back to `m2/design.md`: `runtime.message-round-trip` stays open until the adk-go leg lands, and `runtime.governed-components-in-eino-graph` closes with `m2-eino-graph-native-resume`.
