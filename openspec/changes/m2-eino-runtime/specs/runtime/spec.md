# Spec Delta

## MODIFIED Requirements

### Requirement: Run lifecycle ordering

`Drive` MUST be the only loop for `native`, `eino` and `adkgo` alike: it applies `RunLimits`, emits events, persists per ADR-0048, runs `Replay` as re-execution of `Step` over recorded results, and re-drives `Resuming` runs from the stored `ResumeInput`. A runtime MUST NOT emit `Done`; the harness emits it after `Runs.Finish` returned.

One `Step` is one effect boundary: for `native`, one model call or one tool batch (`StepGranularity: Effect`); for `eino` and `adkgo`, one turn (`StepGranularity: Turn`), declared through `Runtime.Granularity` and printed by `Explain`.

#### Scenario: suspend order
ID: `runtime.suspend-order`
- WHEN a run suspends for `HumanApproval`
- THEN the assistant message with the pending `ToolUse` is in `SessionLog`, the checkpoint exists, `Runs` shows `Suspended` with the token, and only then is `Suspended` emitted

#### Scenario: append before tool
ID: `runtime.append-before-tool`
- WHEN the model returns a `ToolUse`
- THEN, when the call is `Idempotent`/`SideEffect`, the assistant message with its pending calls is appended and `HistoryVersion` advanced before the gate runs; a `ReadOnly` call is appended together with its result at step end (`recovery`)

#### Scenario: done after finish
ID: `runtime.done-after-finish`
- WHEN a run completes
- THEN `Runs.Finish` has been called before `Done` is observable on the stream, and `Done` is the last event

### Requirement: Health

`Health` runs the readiness checks with a bounded ctx (default 2 s) and reports each by name: every configured store answers a ping and the Postgres schema is within the skew rule; every platform provider key validated at `Build` is still accepted (re-checked at most once per minute, cached); `shutdown` is false once `Shutdown` began. `Ready` is the conjunction of the required checks; breaker state per endpoint is reported (`breaker:<endpoint>`) but never affects `Ready`.

The checks are backend-independent: a foreign runtime MUST NOT report readiness through a channel of its own, no backend may keep `Ready` true after `Shutdown` began, and `Ready()` stays `Health(ctx).Ready` with the cached result.

#### Scenario: readiness false on schema skew
ID: `runtime.readyz-false-on-schema-skew`
- WHEN the Postgres schema is older than the release supports
- THEN `Health` reports `postgres` not OK naming `ErrSchemaTooOld`, `Ready` is false and `/readyz` answers 503

#### Scenario: readiness false during shutdown
ID: `runtime.readyz-false-during-shutdown`
- WHEN `Shutdown` has begun while runs drain
- THEN `/readyz` answers 503 and `/healthz` still answers 200 until the process exits

### Requirement: Graceful shutdown

A foreign runtime MUST reach preemption through the same wrapped runtime a native run uses, so a `Turn`-granularity backend is interrupted between two turns and never inside a graph or a tool call. `Shutdown`, `Ready` and the health report stay owned by the stack: an adapter MUST NOT implement its own lifecycle, its own run loop or its own shutdown handling.

#### Scenario: shutdown rejects new work
ID: `runtime.shutdown-rejects-new`
- WHEN `Shutdown` has been called and a client calls `Send`
- THEN `ErrShuttingDown` is returned before `Runs.Start` and `Ready()` is false

#### Scenario: preempt at safe point
ID: `runtime.shutdown-preempts-at-safe-point`
- WHEN `Shutdown` is called while a run is between two turns
- THEN a checkpoint is written, `Runs.Suspend` records the token, `Suspended{Preempted}` is the last event and `Shutdown` returns nil

#### Scenario: side effect completes before preemption
ID: `runtime.shutdown-side-effect-completes`
- WHEN `Shutdown` is called while a `SideEffect` tool is executing under the cancel shield
- THEN the tool completes, its result is journaled and appended, and preemption happens at the following safe point

#### Scenario: partial model call dropped
ID: `runtime.shutdown-partial-model-call-dropped`
- WHEN `Shutdown` is called while assistant deltas are streaming
- THEN the model call is aborted, no assistant message is appended, and the resumed run re-issues the model call for the same turn

#### Scenario: client continues a preempted run
ID: `runtime.preempted-resume-continue`
- WHEN a client receives `Suspended{Preempted, Token}` and calls `Resume(token, Continue())` on another pod
- THEN the run continues from the checkpoint with `Seq` following the last delivered event and no approver is required

#### Scenario: grace exhausted
ID: `runtime.shutdown-grace-exhausted`
- WHEN the `Shutdown` ctx expires while a `SideEffect` tool is still executing
- THEN `Shutdown` returns `ErrShutdownIncomplete` naming the run and the run is later reclaimed as stale by `Recover`

### Requirement: Tool batch protocol

The batch protocol is the native runtime's governed turn path. A foreign runtime schedules its own calls, and this change MUST NOT require native scheduling inside `eino`; every governed component a foreign runtime invokes MUST still pass the gate per call, and `Drive` MUST still apply `RunLimits` after every effect.

#### Scenario: gate all before execution
ID: `runtime.batch-gate-first`
- WHEN a turn carries three calls and the policy denies the third
- THEN the third has a `Failed(Permanent)` result before the first executes and the other two execute

#### Scenario: batch limit before execution
ID: `runtime.batch-limit-before-execute`
- WHEN `MaxToolCalls` leaves room for two calls and the turn carries three
- THEN the run aborts with `*LimitExceededError` and no tool executes

#### Scenario: read-only calls run concurrently
ID: `runtime.readonly-parallel`
- WHEN a turn carries three `ReadOnly` calls that each take 100 ms
- THEN the batch completes in about 100 ms and results reach the model in call order

#### Scenario: side effects run in order
ID: `runtime.side-effects-sequential`
- WHEN a turn carries two `SideEffect` calls and one `ReadOnly` call
- THEN the read-only call completes first and the side effects execute one after another in call order, each journaled before the next starts

#### Scenario: ask after allowed calls
ID: `runtime.batch-ask-after-allowed`
- WHEN a turn carries a `ReadOnly` call and an `Ask` call
- THEN the read-only call executes, the run suspends with an `ApprovalRequest` for the other, and on resume only the approved call executes

#### Scenario: one result per call
ID: `runtime.batch-one-result-per-call`
- WHEN a call in the batch is rejected on resume
- THEN the appended message carries a result for every call and the rejected one reads `not_executed: rejected by <approver>`

#### Scenario: parallel tools cap
ID: `runtime.parallel-tools-cap`
- WHEN `MaxParallelTools` is 2 and a turn carries five `ReadOnly` calls
- THEN at most two execute at any moment

#### Scenario: sequential tools hint
ID: `runtime.sequential-tools-hint`
- WHEN a flow is built with `SequentialTools()` or the profile has `ParallelTools: false`
- THEN the model request asks for single-call turns and a multi-call response is still executed by the batch protocol

### Requirement: Runtime conformance

`conformance.Runtime(t, newRuntime)` SHALL pass for `native`, `eino`, `adkgo`. A backend propagates `ctx` unchanged into every component call, translates a component's suspend signal into its own mechanism and back into `Suspended`, and honours cancellation.

Component-level events (`TextDelta`, `ToolStarted`, `ToolFinished`) and the turn counter MUST come from the governed decorators through the run-scoped sink in `ctx`; a runtime MUST NOT emit them, and MUST NOT count turns itself: `Drive` owns the limit.

#### Scenario: plain answer
ID: `runtime.plain-answer`
- WHEN the scripted model returns "hi"
- THEN events are `TextDelta*`, `AssistantMessage("hi")`, `Done(StopCompleted)`; `SessionLog` holds user + assistant

#### Scenario: tool round trip
ID: `runtime.tool-round-trip`
- WHEN the model calls `echo` then answers
- THEN `ToolStarted`, `ToolFinished`, second `AssistantMessage`, `Done`

#### Scenario: parallel calls ordering
ID: `runtime.parallel-calls-ordering`
- WHEN one message contains two tool calls
- THEN results reach the next model call in call order

#### Scenario: max turns
ID: `runtime.max-turns`
- WHEN the model calls a tool every turn and `MaxTurns=3`
- THEN `Done(StopLimit)` after 3 model calls and `gohan.max_turns.reached` increments

#### Scenario: cancellation
ID: `runtime.cancellation`
- WHEN ctx is cancelled mid-stream
- THEN the iterator yields `context.Canceled` and no further components run

### Requirement: Component mixing

A backend MUST reach every governed model, tool and message conversion through the exported seams, so mixing a foreign component under the native runtime and mixing governed components inside a foreign graph run the same chains. A message bridge MUST declare the fidelity it keeps, and MUST NOT drop a block the source carried.

#### Scenario: foreign tool under native
ID: `runtime.foreign-tool-under-native`
- WHEN an imported eino `InvokableTool` runs under the native runtime
- THEN R2 "tool round trip" passes and the call goes through the full tool chain

#### Scenario: governed components in eino graph
ID: `runtime.governed-components-in-eino-graph`
- WHEN a graph is built with `einoflow.ModelNode` and `einoflow.ToolsNode`
- THEN R3 and R7 scenarios pass for that graph flow

#### Scenario: message round trip
ID: `runtime.message-round-trip`
- WHEN a `Message` with reasoning, text, image, document, tool use and `Raw` blocks is converted to eino `AgenticMessage` / adk-go `genai.Content` and back
- THEN block order is preserved and each block matches the adapter's declared fidelity (`Reasoning` preserved only through its own provider)
