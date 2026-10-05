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
2. `Runs.Start(run, LeaseTTL)` → lease, or `ErrRunActive`; `OperationID` dedup (`engines`) happens here.
3. `SessionLog.Load(session)` → `History{Messages, Version}`.
4. Frozen flags evaluated once, snapshotted into `RunInfo.Flags` and the run-start audit record.
5. Input message(s) appended with `expectedVersion = History.Version`; `Message.ID` assigned; `History.Version` advanced.
6. `Drive(ctx, rt, run)` with `State.HistoryVersion = History.Version`.

**Per step** (inside `Drive`, one effect boundary for `native`):

1. Build `AssembleInput` (run info, profile, system blocks, tools after `ToolFilter`, history at `State.HistoryVersion`, providers by slot); apply `ContextPolicy` projections; `Assemble`.
2. Model chain call; deltas stream through the sink; on completion pick the turn's append shape (`recovery`): a turn with no tool calls or only `ReadOnly` calls appends the assistant message and its results in one `Append` at step end; a turn carrying `Idempotent`/`SideEffect` calls appends the assistant message with its pending calls before the batch executes. Either way the `expectedVersion` check applies and `HistoryVersion` advances per `Append`.
3. The turn's `ToolUse` blocks form one **batch** executed by the batch protocol below; results for the batch are appended in one `Append` at step end, merged with the assistant message when the turn carried no or only `ReadOnly` calls; a crash before that append is recovered from the journal (`recovery.pod-dies-inside-a-side-effect`).

   **Batch protocol** (native runtime; behaviour derives from `Effect`, there is no scheduling option):

   1. *Gate all first.* Every call is gated (hard blocks, taint, policy, session grants, decider) and reserved against `MaxToolCalls` before any executes; an overrun aborts the run with nothing executed. `Deny` and `TaintDenied` produce their `Failed(Permanent)` results at once.
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


## Requirements

### Requirement: Run lifecycle ordering

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

`Health` runs the readiness checks with a bounded ctx (default 2 s) and reports each by name: every configured store answers a ping and the Postgres schema is within the skew rule; every platform provider key validated at `Build` is still accepted (re-checked at most once per minute, cached); `shutdown` is false once `Shutdown` began. `Ready` is the conjunction of the required checks; breaker state per endpoint is reported (`breaker:<endpoint>`) but never affects `Ready`, since a degraded provider is routed around. `Ready()` is `Health(ctx).Ready` with the cached result.

#### Scenario: readiness false on schema skew
ID: `runtime.readyz-false-on-schema-skew`
- WHEN the Postgres schema is older than the release supports
- THEN `Health` reports `postgres` not OK naming `ErrSchemaTooOld`, `Ready` is false and `/readyz` answers 503

#### Scenario: readiness false during shutdown
ID: `runtime.readyz-false-during-shutdown`
- WHEN `Shutdown` has begun while runs drain
- THEN `/readyz` answers 503 and `/healthz` still answers 200 until the process exits

### Requirement: Graceful shutdown

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

`conformance.Runtime(t, newRuntime)` SHALL pass for `native`, `eino`, `adkgo`.

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
