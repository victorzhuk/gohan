# Runtimes and stepper

Capability: `runtime` · Spec v1.5 (ADR-0134) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `runtime` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.

## Contract

### 6.14 Runtimes and backends

```go
// (illustrative) names may change during M0; see contract tiers
type AgentSpec struct {
	Name        string
	Instruction string
	Tools       []Tool
	Context     []ContextProvider
	Limits      RunLimits
}

type Status int

const (
	Continue Status = iota
	SuspendedStatus
	DoneStatus
)

type State struct {
	Turn           int
	HistoryVersion int64
	Pending     []ToolUse
	ActiveTools []string
	Flags       FlagSnapshot
	Usage       Usage
	Calibration map[string]float64
	Backend     []byte
}

type Stepper interface {
	Start(ctx context.Context, r AgentRun) (State, error)
	Step(ctx context.Context, st State) (State, []Event, Status, error) // events: runtime-originated only (Raw progress); component events go through the sink
}

type Runtime interface {
	Stepper
	Name() string
	Granularity() StepGranularity
}

type StepGranularity int

const (
	GranularityEffect StepGranularity = iota
	GranularityTurn
}

func Drive(ctx context.Context, rt Runtime, r AgentRun) iter.Seq2[Event, error]
func DriveResume(ctx context.Context, rt Runtime, r AgentRun, st State, in ResumeInput) iter.Seq2[Event, error]

type AgentRun struct {
	Model    Model
	Tools    []Tool
	Assemble func(ctx context.Context, in AssembleInput) (ModelRequest, error)
	History  History
	Input    []Message
	Save     func(ctx context.Context, cp Checkpoint) (ResumeToken, error)
	Mode     RunMode
}
```

`State.HistoryVersion` is the `SessionLog` version the state was taken at; `Replay` loads history at that version. `State` is serializable and is what a checkpoint carries for agent flows. One `Step` is one effect boundary: for `native`, one model call or one tool batch (`StepGranularity: Effect`); for `eino` and `adkgo`, one turn (`StepGranularity: Turn`, declared and printed by `Explain`). `Drive` is the only loop: it applies `RunLimits`, emits events, persists per ADR-0048, runs `Replay` as re-execution of `Step` over recorded results, and re-drives `Resuming` runs from the stored `ResumeInput`. No engine-backed adapter drives `Step` in v1 (ADR-0075); the seam exists for that mode.

`AgentRun.Model` and `Tools` are already governed. Runtime responsibilities are minimal:

1. advance one step (or one turn) of the loop or graph;
2. propagate `ctx` unchanged into component calls;
3. translate suspend signals from components into the backend's mechanism and back into `Suspended`;
4. honour cancellation; limits are applied by `Drive`.

Component-level events (`TextDelta`, `ToolStarted`, `ToolFinished`) and the turn counter come from governed decorators through a run-scoped sink in `ctx`; runtimes do not emit them.

Resume strategies:

- `Replay` (all agent runtimes): `DriveResume` rebuilds `State` from the checkpoint, appends the resume input (approved, rejected, edited, or delivered tool result) and re-executes `Step` from there; completed calls are answered from the journal, never re-executed.
- `Native` (eino ADK and compose graphs): backend checkpoint bytes stored in `State.Backend` (serialized into `Checkpoint.Data` together with the rest of `State`) with `BackendVersion`; gohan consumes the token first, then calls the backend's resume. If `BackendVersion` differs from the running adapter or the backend fails to decode, agent flows fall back to `Replay` (`gohan.resume.fallback_replay` increments); graph flows return `ErrCheckpointIncompatible`. `Explain` warns when a flow relies on `Native` with no `Replay` path.

### Run lifecycle (normative ordering)

`Drive` and the flow constructors own every store interaction; runtimes never touch stores. The order below is the contract that `recovery` and `streams` scenarios assume.

**Before `Drive`** (`Invoke`/`Send`/`Resume`):

1. `PrincipalFrom(ctx)` or `ErrNoPrincipal` (unless `AllowAnonymous`).
2. New `Invoke`/`Send`: `Runs.Start(run, LeaseTTL)` → lease, or `ErrRunActive`; `OperationID` dedup (`engines`) happens here. `Resume`: the store's `CheckpointRuns.ResumingCheckpoint` acquires the lease fenced to the run's current suspended token, and a store without that surface fails with `types.ErrCheckpointIncompatible` before the token is consumed.
3. `SessionLog.Load(session)` → `History{Messages, Version}`.
4. Frozen flags evaluated once, snapshotted into `RunInfo.Flags` and the run-start audit record.
5. Input message(s) appended with `expectedVersion = History.Version`; `Message.ID` assigned; `History.Version` advanced.
6. `Drive(ctx, rt, run)` with `State.HistoryVersion = History.Version`.

**Per step** (inside `Drive`, one effect boundary for `native`):

1. Build `AssembleInput` (run info, profile, system blocks, tools after `ToolFilter`, history at `State.HistoryVersion`, providers by slot); apply `ContextPolicy` projections; `Assemble`.
2. Model chain call; deltas stream through the sink; on completion pick the turn's append shape (`recovery`): a turn with no tool calls or only `ReadOnly` calls appends the assistant message and its results in one `Append` at step end; a turn carrying `Idempotent`/`SideEffect` calls appends the assistant message with its pending calls before the batch executes. Either way the `expectedVersion` check applies and `HistoryVersion` advances per `Append`.
3. The turn's `ToolUse` blocks form one **batch** executed by the batch protocol below; results for the batch are appended in one `Append` at step end, merged with the assistant message when the turn carried no or only `ReadOnly` calls; a crash before that append is recovered from the journal (`recovery.pod-dies-inside-a-side-effect`).

   **Batch protocol** (native runtime; behaviour derives from `Effect`, there is no scheduling option):

   1. *Gate all first.* Every call is gated (hard blocks, taint, policy, session grants, decider) and reserved against `MaxToolCalls` before any executes. A refused ledger reservation spends no budget and the native driver returns its error matching `types.ErrBatchOverrun` directly. If `runtime.Batch.Run` returns an error matching `types.ErrBatchOverrun`, the driver refunds the reservation and returns `*LimitExceededError{Limit: "MaxToolCalls"}`. `Deny` and `TaintDenied` produce their `Failed(Permanent)` results at once.
   2. *Execute the allowed calls.* `ReadOnly` calls run concurrently, at most `RunLimits.MaxParallelTools` at a time (`FlowAsTool` calls count against this and `MaxParallelChildren`); `Idempotent` and `SideEffect` calls run sequentially in call order after the read-only group, each with journal `Reserve` → call (cancel shield for `SideEffect`) → `Complete`. One call's failure never cancels another; every result is model-visible.
   3. *Then the asks.* Calls gated `Ask` suspend one at a time in call order with a single `ApprovalRequest` (`EditArgs` unchanged); already-executed results are in the journal and replay on resume.
   4. *Complete result set.* Exactly one `ToolResult` per `ToolUse`, in call order; a call that was not executed (denied, rejected, or the run aborted mid-batch) carries `Failed` with `Error.Kind = Permanent` and a reason beginning `not_executed:`. Core exports that prefix as `runtime.NotExecutedPrefix`; the batch path and the resume path render it, so a call rejected on resume reads `not_executed: rejected by <approver>`. The same protocol governs the driver's governed-turn path: a gate decision that suspends is never converted into an ordinary failed tool result, and a call after the suspending one is never dropped.
   5. *Provider hint.* When `Caps.ParallelTools` is false or the flow is built with `agent.SequentialTools()`, the adapter requests single-call turns (`disable_parallel_tool_use` / `parallel_tool_calls: false`); a multi-call response is still handled by rules 1–4. Foreign runtimes schedule their own calls; governed components still pass the gate per call.
4. `Runs.Heartbeat` at least every `HeartbeatEvery` from the run's heartbeat goroutine (`streams` *Slow consumers*), never from the step itself; `RunLimits` checked after every effect; `LimitWarning` at `SoftRatio`.
5. Events of the step written to `EventLog` in `Seq` order before the next step begins.
6. **Mailbox.** `Runs.Drain(lease)` at every safe point (after the batch results are appended, and before `Finish`). `SignalCancel` → the run stops here with `Done{Reason: cancelled}`. Each `SignalSteer` is appended to `SessionLog` as an `OriginUser` message in arrival order and `SteerApplied{MessageID}` emitted; the next step's assembly includes them. A model reply with no tool calls followed by a `Finish` that returns `ErrSignalsPending` drains again and runs one more turn, unless `MaxTurns` is already reached, in which case the run ends with `Done{Reason: StopLimit}`. Only the root run drains steers; `FlowAsTool` children and `MapReduce` items never do, and a steer posted to a child's session id is `ErrRunNotActive`.

**On suspend**:

1. Pending `ToolUse` blocks are already in the appended assistant message (step 2).
2. `Checkpoints.Put(cp)` with `State`, originator, reason, `Child`/`Workspace` where set → token.
3. `Runs.Suspend(lease, token)`.
4. `Suspended{Token, Reason, Payload}` event, then the iterator ends. A crash between 2 and 3 leaves a run the reaper marks `Suspended` from the checkpoint; between 3 and 4 the client reattaches via `EventLog`.

**On done or error**:

1. `Verify` for every `Uncertain` entry (`tools`).
2. `Runs.Finish(lease, state, uncertain, resultRef)`.
3. `Done{Reason, Usage, Cost, Uncertain}` (or the error) is emitted last.

### Graceful shutdown

```go

func (s *Stack) Shutdown(ctx context.Context) error
func (s *Stack) Ready() bool
func (s *Stack) Health(ctx context.Context) HealthReport

type HealthReport struct {
	Ready  bool
	Checks map[string]Check
}

type Check struct {
	OK      bool
	Detail  string
	Latency time.Duration
}

var (
	ErrShuttingDown       = errors.New("gohan: stack is shutting down")
	ErrShutdownIncomplete = errors.New("gohan: shutdown deadline passed with runs in flight")
)

type ShutdownIncomplete struct {
	RunIDs []string
}
```

The component-event sink is normative in shape:

```go
// Sink receives the component-level events a governed decorator produces.
// A runtime never emits these itself; it reads them from the run-scoped ctx.
type Sink interface {
	Emit(ctx context.Context, e Event)
}

func WithSink(ctx context.Context, s Sink) context.Context
func SinkFrom(ctx context.Context) (Sink, bool)
```

`Drive` installs the sink in the ctx it propagates into component calls. A component that finds no sink drops the event and carries on.

The service calls `Shutdown` on SIGTERM with the grace budget as the ctx deadline (Kubernetes: `terminationGracePeriodSeconds` minus the preStop delay). Rules:

1. From the first `Shutdown` call, `Send`, `Invoke` and `Resume` return `ErrShuttingDown` (`Transient`; HTTP transports map it to 503 with `Retry-After`) and `Ready()` returns false. `Shutdown` is idempotent; later calls join the first.
2. Every in-flight run, attached or `Detached`, is **preempted at its next safe point**, the same point `Cancel` uses: a persisted turn boundary. A `SideEffect` tool already past the gate completes under the cancel shield bounded by `ToolSpec.Timeout` first; a model call in progress is aborted (`Transient`, nothing appended) and the turn re-runs on resume. At the safe point the harness follows *On suspend* with reason `Preempted` and no payload: checkpoint → `Runs.Suspend` → `Suspended{Preempted, Token}` → lease released.
3. A `Preempted` run resumes on any pod by either path, made safe by the single-use token: the client that received `Suspended{Preempted}` calls `Resume(token, Continue())` (`suspension`; no approver, no payload), or `Recover` resumes it (`recovery` rule 3a). `Continue()` on a token whose reason is not `Preempted` fails with `ErrInputInvalid` and keeps the token unconsumed.
4. When ctx expires with runs still in flight, `Shutdown` returns `ErrShutdownIncomplete` wrapping `ShutdownIncomplete{RunIDs}`, the process exits and those runs take the crash path (`recovery`).
5. `Shutdown` returns nil only when every in-flight run has been preempted or finished and every pending `EventLog`/`AuditLog` write is flushed.

Metrics: `gohan.shutdown.preempted`, `gohan.shutdown.incomplete`, `gohan.shutdown.duration` histogram (`telemetry`).

Governed components in foreign graphs: `einoflow` provides node builders from governed components (`einoflow.ModelNode(m gohan.Model)`, `einoflow.ToolsNode(tools...)`). Components not built this way are ungoverned; `einoflow.Check` reports them where graph introspection allows (*verify*).

### Step granularity

One `Step` is one effect boundary: for `native`, one model call or one tool batch (`StepGranularity: Effect`); for `eino` and `adkgo`, one turn (`StepGranularity: Turn`), declared through `Runtime.Granularity` and printed by `Explain`.

### Readiness

`Ready` is the conjunction of the required checks; breaker state per endpoint is reported (`breaker:<endpoint>`) but never affects `Ready`.

The checks are backend-independent: a foreign runtime MUST NOT report readiness through a channel of its own, no backend may keep `Ready` true after `Shutdown` began, and `Ready()` stays `Health(ctx).Ready` with the cached result.

### Preemption safe point

Checkpoint publication, `Runs.Suspend`, the `Suspended{Preempted}` event and the handoff to a resumed run wait for the next safe point, which a `Turn`-granularity backend reaches when the cancelled turn has returned.

### Shielded side effect preemption

Only a shielded side effect outlives cancellation: it runs to completion, is journaled and appended, and preemption follows it.

### Stack lifecycle ownership

`Shutdown`, `Ready` and the health report stay owned by the stack: an adapter MUST NOT implement its own lifecycle, its own run loop or its own shutdown handling.

### Component events and turn counting

Component-level events (`TextDelta`, `ToolStarted`, `ToolFinished`) and the turn counter MUST come from the governed decorators through the run-scoped sink in `ctx`; a runtime MUST NOT emit them, and MUST NOT count turns itself: `Drive` owns the limit.

### Foreign tool bridge validation

A foreign tool bridge MUST validate its declared spec before execution and MUST NOT infer a governance field the upstream tool info omits.

### Message bridge content

A message bridge MUST carry each block's `Origin` and `Seq` and any blob the source held.

## Requirements

### Requirement: Run lifecycle ordering

`Drive` MUST be the only loop for `native`, `eino` and `adkgo` alike: it applies `RunLimits`, emits events, persists per ADR-0048, runs `Replay` as re-execution of `Step` over recorded results, and re-drives `Resuming` runs from the stored `ResumeInput`. A runtime MUST NOT emit `Done`; the harness emits it after `Runs.Finish` returned.

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

`Health` MUST run readiness checks with a bounded ctx (default 2 s) and report each by name. Every configured store MUST answer a ping and the Postgres schema MUST be within the skew rule. Every platform provider key validated at `Build` MUST still be accepted, re-checked at most once per minute and cached. `shutdown` MUST be false once `Shutdown` began.

#### Scenario: readiness false on schema skew
ID: `runtime.readyz-false-on-schema-skew`
- WHEN the Postgres schema is older than the release supports
- THEN `Health` reports `postgres` not OK naming `ErrSchemaTooOld`, `Ready` is false and `/readyz` answers 503

#### Scenario: readiness false during shutdown
ID: `runtime.readyz-false-during-shutdown`
- WHEN `Shutdown` has begun while runs drain
- THEN `/readyz` answers 503 and `/healthz` still answers 200 until the process exits

### Requirement: Graceful shutdown

A foreign runtime MUST reach preemption through the same wrapped runtime a native run uses, and MUST pass the run ctx unchanged into every model and tool call, so cancellation stops the turn's own work at once. A cancelled turn aborts an in-flight model call, drops the partial assistant deltas it had not appended, and runs no further component of that turn; a turn MUST NOT continue its graph after its ctx is cancelled.

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
- WHEN `runtime.Batch.Run` receives two calls with `MaxToolCalls` set to 2 and one call already used
- THEN it returns an error matching `types.ErrBatchOverrun` and no tool executes

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

`conformance.Runtime(t, newRuntime)` SHALL pass for `native`, `eino`, `adkgo`. The `adkgo` leg MUST run against the `google.golang.org/adk/v2` tag pinned in `adapter/adkgo/go.mod`. A backend MUST propagate `ctx` unchanged into every component call, translate suspend into its own mechanism and back into `Suspended`, honour cancellation, and MUST NOT emit component events or count turns.

#### Scenario: conformance suite passes for the native runtime
ID: `runtime.conformance-native`
- WHEN `conformance.Runtime` runs against the native runtime
- THEN the runtime passes the shared conformance suite

#### Scenario: conformance suite passes for the eino runtime
ID: `runtime.conformance-eino`
- WHEN `conformance.Runtime` runs against the eino runtime
- THEN the runtime passes the shared conformance suite

#### Scenario: conformance suite passes for the adk-go runtime
ID: `runtime.conformance-adkgo`
- WHEN `conformance.Runtime` runs against the adk-go runtime
- THEN the runtime passes the shared conformance suite against the `google.golang.org/adk/v2` tag pinned in `adapter/adkgo/go.mod`, with no scenario skipped and no per-backend branch inside the shared suite

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

### Requirement: Foreign per-call governed path

A foreign flow MUST run from the resolved native configuration. Every tool call a foreign run makes MUST take the same governed path the native batch protocol uses per call: the approval gate and the native budget reservation MUST run before the tool implementation. The composed tool chain MUST remain the only ordinary-tool journal wrapper, so a call is journaled exactly once.

#### Scenario: mixed stack builds and invokes
ID: `runtime.foreign-mixed-stack-invoke`
- WHEN a stack registers a native flow and a foreign flow over the same driver and a foreign run executes a governed tool call
- THEN the call passes the gate and the effect journal exactly once and the tool result reaches the model

#### Scenario: allowed foreign call
ID: `runtime.foreign-call-allowed`
- WHEN a foreign run calls an `Idempotent` tool the policy allows
- THEN the gate returns `Allow`, the journal records one `Completed` entry for the call key, and the tool runs once

#### Scenario: denied foreign call
ID: `runtime.foreign-call-denied`
- WHEN the gate denies a foreign run's `SideEffect` call
- THEN the model receives a `Failed(Permanent)` result reading `not_executed:` and the tool implementation never runs

### Requirement: Foreign call identity is required

A foreign call that carries no stable `ToolUse` identity MUST fail closed with a `Failed(Permanent)` result before the gate, the reservation and any side effect, and MUST NOT be journaled.

#### Scenario: missing call identity fails closed
ID: `runtime.foreign-call-identity-required`
- WHEN a foreign backend delivers a tool call with no stable call id
- THEN the call fails closed before the gate and before any side effect, and no journal entry is written

### Requirement: Foreign call identity

A foreign backend MUST supply a stable call id per tool call, and the adapter MUST carry it into the governed call unchanged. The id on the governed `ToolUse`, the journal call key and the appended `ToolResult` MUST agree, so a resumed or recovered turn matches the completed call instead of re-executing it. Two calls to one tool in one turn MUST stay distinct under their own ids.

#### Scenario: call id round trip
ID: `runtime.foreign-call-id-round-trip`
- WHEN a foreign backend calls a tool with id `call-1`
- THEN the governed `ToolUse`, the journal call key and the appended `ToolResult` all carry `call-1`

#### Scenario: two calls in one turn
ID: `runtime.foreign-call-id-distinct`
- WHEN one foreign turn carries two calls to the same tool with ids `call-1` and `call-2`
- THEN each is gated and journaled under its own id and neither result is attached to the other call

### Requirement: Foreign invocation context

The caller's identity, deadline and cancellation lineage MUST reach every foreign model and tool call. A foreign run started by `Send`, resumed by `Resume` and reclaimed by `Recover` MUST observe the caller's principal, credential, run identity and deadline. A derived context MUST preserve that lineage; it does not promise the same context object inside a shielded call.

#### Scenario: context preserved across entry paths
ID: `runtime.foreign-ctx-preserved`
- WHEN a foreign run is started by `Send`, resumed by `Resume` and reclaimed by `Recover`
- THEN each path carries the caller's identity, deadline and cancellation lineage into every model and tool call

### Requirement: Foreign cancellation stops the turn by effect

Cancellation MUST behave by effect. A `ReadOnly` call is not shielded, so cancellation reaches the call, the call aborts, and no later component of that turn runs. After a non-`ReadOnly` call begins its committed execution, the shield settles the call, the journal completes its result, and cancellation stops the components after settlement. Cancellation MUST NOT discard a committed result before journal settlement, and a `ReadOnly` call MUST NOT gain a journal requirement it did not have.

#### Scenario: cancellation stops the turn
ID: `runtime.foreign-cancellation`
- WHEN the caller's ctx is cancelled during a foreign turn
- THEN an unshielded `ReadOnly` call aborts and no further component of that turn runs, while a committed non-`ReadOnly` call settles and journals its result before the run stops

### Requirement: Foreign recovery replays a completed call

`Recover` MUST answer a completed non-`ReadOnly` call from the journal and MUST NOT re-execute it.

#### Scenario: completed call recovered without re-execution
ID: `runtime.foreign-completed-call-replayed`
- WHEN a foreign run crashes after a non-`ReadOnly` call completed and was journaled
- THEN `Recover` replays the result from the journal, does not re-execute the call, and finishes the run

### Requirement: Atomic shutdown admission

The shutdown admission decision and the registration of a starting run in
the in-flight set MUST happen as one atomic operation. `Shutdown` MUST NOT
be able to begin between the decision and the registration and leave the
run unregistered. Registration MUST happen before `Runs.Start` on every
constructor path. A run that races `Shutdown` MUST either be registered
before the shutdown set is armed or be refused with
`types.ErrShuttingDown`.

#### Scenario: registration is not lost to shutdown
ID: `runtime.shutdown-admission-atomic`
- WHEN a `Send` registers a run concurrently with `Shutdown`
- THEN the run is either registered before the preemption set is armed, or refused with `types.ErrShuttingDown`; no run starts unregistered

#### Scenario: every constructor admits before start
ID: `runtime.shutdown-admission-every-constructor`
- WHEN a run starts through the native constructor or the foreign constructor
- THEN it is registered in the in-flight set before `Runs.Start`

#### Scenario: resume admits before start
ID: `runtime.shutdown-admission-resume`
- WHEN `Resume` starts a run
- THEN it is registered in the in-flight set before `Runs.Start`

### Requirement: Terminal stop reason preservation

The lifecycle MUST pass the run's terminal `StopReason` to both the final
`Done` event and the durable run record. It MUST NOT substitute
`StopCompleted` for a terminal reason. A run that ends because a limit was
reached, because a cancel signal arrived, or because the model requested a
human MUST report that reason.

#### Scenario: done carries the limit reason
ID: `runtime.done-carries-limit-reason`
- WHEN a run ends because `MaxTurns` was reached
- THEN `Done.Reason` is `StopLimit`

#### Scenario: durable record carries the terminal reason
ID: `runtime.finish-carries-terminal-reason`
- WHEN a run ends with a non-completed reason and the runs store implements `FinishReasonRecorder`
- THEN the stored reason is that terminal reason, not `StopCompleted`

#### Scenario: cancelled run keeps its reason
ID: `runtime.cancelled-run-keeps-reason`
- WHEN a pending cancel signal ends the run at a safe point
- THEN `Done.Reason` is `StopCancelled` and the durable reason matches

#### Scenario: handed-off run keeps its reason
ID: `runtime.handed-off-run-keeps-reason`
- WHEN the model requested a human and the run closes
- THEN `Done.Reason` is `StopHandedOff` and the durable reason matches

### Requirement: Foreign tool governance

A foreign tool bridge MUST present an imported framework tool as `types.Tool` and MUST take its governance metadata explicitly. It MUST NOT infer `Effect`, `Risk` or `Capabilities` from a field the upstream tool info omits. A zero `Trust` MUST resolve to `Untrusted`. The bridge MUST validate the declared spec before the tool can execute, MUST preserve argument, result and error identity so `errors.Is` holds, and MUST route every call through the governed tool chain.

#### Scenario: foreign tool spec declared
ID: `runtime.foreign-tool-spec-declared`
- WHEN a bridge is constructed with an explicit `types.ToolSpec`
- THEN the returned value reports that spec and a mis-declared upstream tool info fails construction before any call runs

#### Scenario: foreign tool trust untrusted
ID: `runtime.foreign-tool-trust-untrusted`
- WHEN a bridge is constructed with the zero `Trust` value
- THEN the reported spec carries `types.Untrusted`, whatever the upstream tool info declares

#### Scenario: foreign tool error identity
ID: `runtime.foreign-tool-error-identity`
- WHEN an imported tool returns a wrapped sentinel error
- THEN `errors.Is` holds for that sentinel at the caller of the governed chain

### Requirement: Foreign tool result mapping

A foreign tool bridge MUST take its result mode as an explicit construction choice. The default constructor MUST carry every result string verbatim as one `Text` block and MUST NOT parse it. The envelope constructor MUST parse only a JSON object whose single key is `gohan_tool_result` and whose value is a `types.ToolResult` with content blocks or an explicit outcome. It MUST keep every other string as text and MUST NOT fail a call on it. No argument schema selects the mode.

#### Scenario: foreign tool result mapped
ID: `runtime.foreign-tool-result-mapped`
- WHEN the default constructor is used and the imported tool returns any string
- THEN the result holds one `Text` block with that string verbatim, and the string is never parsed as JSON

#### Scenario: foreign tool envelope result parsed
ID: `runtime.foreign-tool-envelope-parsed`
- WHEN the envelope constructor is used and the imported tool returns a JSON object whose only key is `gohan_tool_result`
- THEN the envelope value is parsed into the reported `ToolResult` blocks, and any other string stays one `Text` block

### Requirement: Message bridge fidelity

A message bridge MUST carry each block's `Origin` and `Seq` and any blob the source held, whether stored as `Data`/`URL` or as `Blob`, for every block kind it declares. It MUST NOT drop a URL that accompanies data. A block kind the framework carries without a channel of its own MUST round-trip through the reserved opaque channel.

#### Scenario: message block base carried
ID: `runtime.message-block-base-carried`
- WHEN a message carries `Origin` and `Seq` on every block kind the adapter declares
- THEN the converted and re-decoded blocks keep the same origin and sequence

#### Scenario: message blob carried
ID: `runtime.message-blob-carried`
- WHEN an `Image`, `Audio` or `File` block carries `Data`, a `URL` and a `Blob`
- THEN all three survive the conversion and no `URL` is discarded

### Requirement: Terminal success follows successful persistence

A runtime-carried stop reason MUST be preserved only after the terminal store transition succeeds. A failed terminal transition MUST emit an error or terminal failure and MUST NOT emit `Done`.

#### Scenario: A limit stop encounters a failed finish write
ID: `runtime.finish-error-no-done`
- WHEN a step carries a limit stop and terminal persistence fails
- THEN no `Done` is delivered and the failure remains visible

### Requirement: Native tool admissions remain cumulative

Native suspension and recovery MUST preserve the cumulative admitted tool count. A pending prepaid call MUST execute without another charge. A subsequent new batch MUST be checked against all earlier admissions in the same run.

#### Scenario: A later batch suspends and resumes
ID: `runtime.resume-tool-budget-cumulative`
- WHEN earlier batches and a pending approved call have consumed the run's tool limit
- THEN resume executes the prepaid call but admits no additional call beyond the limit

### Requirement: Foreign runtime construction installs call identity

A shipped foreign runtime MUST carry each backend-generated tool call ID through its public conversation path before the governed call boundary. A valid call MUST reach permission, accounting and journal processing with that identity.

#### Scenario: Eino executes a governed tool through the shipped constructor
ID: `runtime.eino-governed-call-identity`
- WHEN a registered Eino flow invokes a tool through its foreign conversation
- THEN its backend call ID reaches the gate and journal and the tool executes once when authorized
