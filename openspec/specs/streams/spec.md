# Events, cancellation and streams

Capability: `streams` · Spec v1.7 (ADR-0133) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `streams` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.15 Events

```go
type StopReason string

const (
	StopCompleted       StopReason = "completed"
	StopSuspended       StopReason = "suspended"
	StopLimit           StopReason = "limit"
	StopGuardBlocked    StopReason = "guard_blocked"
	StopCancelled       StopReason = "cancelled"
	StopFailed          StopReason = "failed"
	StopShadowSuspended StopReason = "shadow_suspended"
	StopHandedOff       StopReason = "handed_off"
)

type Event interface{ isEvent() }

type TextDelta struct{ Turn int; MessageID string; Delta string }
type ReasoningDelta struct{ Turn int; MessageID string; Delta string }
type AssistantMessage struct{ Turn int; Message Message; Usage *Usage; Operator bool }
type ToolArgsDelta struct{ Turn int; CallID string; Name string; Delta string }
type ResultDelta struct{ Turn int; MessageID string; Delta string }
type ToolStarted struct{ Turn int; Call ToolUse }
type ToolFinished struct{ Turn int; Result ToolResult; Replayed bool }
type Suspended struct{ Token ResumeToken; Reason SuspendReason; Payload any; WakeAt time.Time }
type GuardBlocked struct{ Stage GuardStage; Reason string }
type LimitWarning struct{ Limit string; Ratio float64 }
type Compacted struct{ FromVersion, ToVersion int64; TokensBefore, TokensAfter int; Policy string }
type StateChanged struct{ Version int64; Patch []PatchOp }
type FeedbackRecorded struct{ Target FeedbackTarget; Name string; Value any; Source FeedbackSource }
type SteerApplied struct{ MessageID string }

type NoticeKind int

// String renders the notice body field `kind`: "finished" | "failed" |
// "cancelled" | "suspended", in the order of the constants below.

const (
	NoticeFinished NoticeKind = iota
	NoticeFailed
	NoticeCancelled
	NoticeSuspended
)

type RunNotice struct {
	ID        string
	Kind      NoticeKind
	Tenant    string
	SessionID string
	RunID     string
	Reason    string
	At        time.Time
}

// JSON body field names: ID -> "id", Kind -> "kind" (String()), Tenant ->
// "tenant", SessionID -> "session_id", RunID -> "run_id", Reason -> "reason",
// At -> "at". No other field is serialized.

type Notifier interface {
	Notify(ctx context.Context, n RunNotice) error
}
type Done struct{ Reason StopReason; Seq int64; Usage Usage; Cost float64; Uncertain []CallKey; Result json.RawMessage }

func (Done) isEvent()
```

Every event travels with `EventMeta{SessionID, RunID, RootRunID, ParentRunID, Depth, Flow, Seq, Time}`: the harness pairs a payload with its meta when it appends the event to the `EventLog` and when it delivers it on a stream, so the payload structs above stay bare and carry no run identity of their own. `Seq` is monotonic per run, starting at 1, assigned by the harness; transports expose it as the SSE `id:` field. A `FeedbackRecorded` written for a finished run extends that run's `Seq` in its `EventLog` only, so `Done` stays the last event of the run's stream. `TextDelta` is advisory; `AssistantMessage` is authoritative. With `Windowed` output, deltas are released only after their window passes the output guard. `ReasoningDelta` is emitted only when `Caps.ReasoningVisible` is set (`agui`). `Message.ID` is assigned by `SessionLog.Append`.

### Error-tuple protocol (normative for every `iter.Seq2[T, error]` seam)

```go
func Collect[T any](seq iter.Seq2[T, error]) ([]T, error)
func Last(seq iter.Seq2[Event, error]) (Done, error)
func Drain[T any](seq iter.Seq2[T, error]) error
```

1. A tuple with `err != nil` carries the zero `T`, is always the last tuple, and `yield` is never called again after it.
2. Pre-flight failures (`ErrNoPrincipal`, `ErrSessionForbidden`, `ErrRunActive`, `ErrTokenConsumed`, `ErrTokenMismatch`, `ErrResumeInsideRun`, `Build`-time errors surfaced lazily) arrive as the sole tuple before any event.
3. `ctx` cancellation surfaces as a final `context.Canceled` (or `DeadlineExceeded`) tuple after the producer has stopped at its safe point; the resources rule of the iterator contract (`model`) applies.
4. Breaking early loses nothing the producer knew: everything already yielded stands, and a producer never buffers an error behind events.
5. `Last` drains the sequence and returns the `Done` event or the terminal error; `Collect` returns everything yielded before a terminal error together with that error; `Drain` returns only the terminal error. Transports that do not stream use these instead of hand-written loops.
6. Core and `std` never call `iter.Pull` on the request path; middleware wraps a sequence by re-yielding inside its own `iter.Seq2`. `iter.Pull` is for tests.

### 6.15a Run lifetime, cancellation and event streams

Default (**attached**): a run lives under the caller's `ctx`. Cancellation stops the loop at the next safe point. Two invariants hold regardless:

1. A `SideEffect` tool call already past the gate executes under `context.WithoutCancel(ctx)` bounded by `ToolSpec.Timeout`; its result is journaled and appended to `SessionLog` before the run observes cancellation. A client disconnect therefore never produces an `Unknown` outcome by itself.
2. Every event carries `Seq`; the last delivered `Seq` is reported in `Done`/`Suspended` and in the `Runs` record.

**Previews are not inputs.** `ToolArgsDelta` (from `DeltaToolArgs`) and `ResultDelta` (the JSON text of a typed result, `structured-output`) exist for clients only: they are never journaled, gated, tainted, appended to `SessionLog` or passed to a tool. Execution starts only after the complete `ToolUse` passes `jsontext` validation and the batch protocol (`runtime`); `ToolStarted` keeps its meaning of "gate passed, call starting". In `Detached` runs deltas are coalesced per `EventLogCoalesce` (default 200 ms) before `EventLog.Append`; `Seq` stays gapless.

**Slow consumers.** The iterator body runs on the consumer's goroutine (`model` iterator contract), so a consumer that stops taking events stops the loop. Three run-owned protections make that safe:

1. *Heartbeat is never a consumer duty.* `Drive` starts a lease-heartbeat helper goroutine for the run's lifetime; it terminates before the iterator returns. A stalled consumer cannot let the lease expire.
2. *Provider reads are decoupled.* The model chain reads the provider stream on a helper goroutine into a per-call buffer of `StreamBuffer` chunks (default 64) that `yield` drains; `ModelProfile.Timeout.Idle` is measured on the provider read, never on the consumer, so a slow client cannot cause an idle-timeout retry. When the buffer is full the provider read blocks; a provider that then closes the stream is not retried (the turn re-runs under rule 3), and `gohan.stream.buffer_full` increments.
3. *A stalled consumer is a preemption.* When no event is taken for `RunLimits.ConsumerStall` (interactive 30 s, agentic 120 s, batch 0 = disabled), the run is preempted at its next safe point exactly as under `Stack.Shutdown` (`runtime`): `Suspended{Preempted, Token}` is the last event the consumer receives, the lease is released, and the client resumes with `Resume(token, Continue())`. A flow built with `agent.OnStall(Detach)` and an `EventLog` instead continues as `Detached` and the client reattaches from its last `Seq`. `gohan.stream.consumer_stalled{action}` counts both.

Transports set a per-event write deadline equal to `ConsumerStall` when it is non-zero, so a stall surfaces as a failed write rather than a hung goroutine; with `ConsumerStall` 0 (stall-preemption disabled, e.g. `BatchLimits`) no per-event deadline is set (`examples/` SSE recipe).

**Detached** (`agent.Detached()` flow option): the run executes under a harness-owned ctx bounded by `RunLimits.MaxWallClock`; `Send` returns once the run is started, and clients consume events through:

```go
type EventLog interface {
	Append(ctx context.Context, runID string, e Event) error
	Read(ctx context.Context, runID string, afterSeq int64) iter.Seq2[Event, error]
	Expire(ctx context.Context, olderThan time.Time) error
}

func (c Conversation) Attach(ctx context.Context, runID string, afterSeq int64) iter.Seq2[Event, error]
```

`Read` delivers persisted events in order, then live ones, with no gap or duplicate (single writer per run, guaranteed by the `Runs` lease). Implementations: memory ring buffer (core), Redis streams (`adapter/redis`). `Build` rejects `Detached` without an `EventLog`. Attached flows may also set an `EventLog` to enable `Attach` after a reconnect while the original run is still alive.


## Requirements

### Requirement: Run notices

A `RunNotice` is the out-of-band signal that a run reached a terminal or waiting state: `NoticeFinished`, `NoticeFailed`, `NoticeCancelled` with `Reason` = the `StopReason` constant name (e.g. `"StopCompleted"`), `NoticeSuspended` with `Reason` = the `SuspendReason` constant name (e.g. `"HumanApproval"`). It carries ids and a reason word only — never a message, a result, an approval payload or an error detail; consumers read those through `Inspect`, `Attach` or the stores. Notices are written by `Runs.Finish` and `Runs.Suspend` in the same transaction as the state change (`stores`) and delivered by `std/notify` to the stack's `Notifier` (`agent.WithNotifier`) at least once, in no guaranteed order: `ID` is the consumer's idempotency key. Delivery retries with exponential backoff (1 s doubling to 1 h) for up to 72 h, then the notice is dropped with an audit record `notice_dead`; `gohan.notice.delivered{kind}`, `gohan.notice.retried`, `gohan.notice.dead`. `std/notify.Only(kinds...)` filters. `std/notify/webhook` is the HTTP `Notifier`: `Endpoint func(tenant string) (url, secret string, ok bool)` gives each tenant its own destination (no endpoint → the notice is acknowledged and `gohan.notice.unrouted` increments); requests follow Standard Webhooks — JSON body of the notice, `webhook-id`, `webhook-timestamp`, `webhook-signature` (HMAC-SHA256, `v1,` prefix), receivers reject timestamps older than five minutes; only a 2xx acknowledges, 3xx/4xx/5xx retry; the URL passes the stack's `EgressPolicy` (`tools`). A Go host passes any `Notifier`. `Detached()` without a `Notifier` is allowed (`Attach` polling stays valid) and logs a warning at `Build`.

#### Scenario: notice is thin
ID: `streams.notice-thin-no-content`
- WHEN a run finishes with a two-paragraph reply and a tenant webhook is configured
- THEN the delivered body has `id`, `kind: "finished"`, `tenant`, `session_id`, `run_id`, `reason`, `at` and nothing else

#### Scenario: notice retried then dead-lettered
ID: `streams.notice-retry-and-dead-letter`
- WHEN the tenant endpoint answers 503 for 72 hours
- THEN deliveries back off from 1 s to 1 h, the notice is then dropped, `notice_dead` is audited with the notice id and `gohan.notice.dead` increments

#### Scenario: webhook signed per Standard Webhooks
ID: `streams.webhook-standard-signed`
- WHEN a notice is delivered
- THEN the request carries `webhook-id` = notice id, `webhook-timestamp` and `webhook-signature` computed with the tenant's secret over `id.timestamp.body`, and a receiver with the secret verifies it

### Requirement: Error-tuple protocol

#### Scenario: error tuple is terminal
ID: `streams.error-tuple-terminal`
- WHEN a run fails after three events
- THEN the fourth tuple carries a nil event and the error, and no fifth tuple is yielded

#### Scenario: pre-flight error is the sole tuple
ID: `streams.preflight-error-sole-tuple`
- WHEN `Send` is called on a session with a live lease
- THEN the sequence yields exactly one tuple `(nil, ErrRunActive)` and no run starts

#### Scenario: collect helpers
ID: `streams.collect-helpers`
- WHEN `Last` consumes a successful stream and `Collect` consumes a failing one
- THEN `Last` returns the `Done` event and `Collect` returns the events before the failure together with the error

### Requirement: Previews

#### Scenario: terminal error event
ID: `streams.terminal-error-event`
- WHEN a model call fails `Permanent` after ten events were streamed over SSE
- THEN the client receives one `error` event with `code`, `kind`, `instance` and the next `Seq`, then the stream closes

#### Scenario: close without done is interrupted
ID: `streams.close-without-done-is-interrupted`
- WHEN the connection drops before `done` or `error`
- THEN the client library reports `gohan.stream_interrupted` and `Attach(runID, lastSeq)` resumes without duplicates

#### Scenario: tool args delta preview only
ID: `streams.tool-args-delta-preview-only`
- WHEN a model streams arguments for a `SideEffect` tool in three fragments
- THEN the client receives three `ToolArgsDelta` events before `ToolStarted`, no journal entry or gate decision exists until the complete block arrives, and the tool sees only the complete arguments

#### Scenario: tool args deltas coalesced in log
ID: `streams.tool-args-delta-coalesced-in-log`
- WHEN a `Detached` run streams forty argument fragments within 200 ms
- THEN `EventLog` holds one coalesced `ToolArgsDelta` with consecutive `Seq` and `Attach` replays it

### Requirement: Cancellation and streams

#### Scenario: disconnect during side effect
ID: `streams.disconnect-during-side-effect`
- WHEN the client ctx is cancelled while `create_booking` is executing
- THEN the tool completes under the shield, its result is journaled and appended, `gohan.run.cancelled_shielded` increments, and only then does the run stop with `context.Canceled`

#### Scenario: disconnect during model call
ID: `streams.disconnect-during-model-call`
- WHEN the client ctx is cancelled mid-stream on a `ReadOnly` turn
- THEN the model call is cancelled immediately and no tool runs

#### Scenario: monotonic seq
ID: `streams.monotonic-seq`
- WHEN a run emits N events
- THEN `Seq` values are exactly 1..N in delivery order and `Done.Seq == N`

#### Scenario: feedback recorded event
ID: `streams.feedback-recorded-event`
- WHEN feedback is recorded for a message of a run that has an `EventLog`
- THEN `FeedbackRecorded` is appended to the run's `EventLog` with the next `Seq` after `Done`, outside the finished run's event stream, and carries no comment or correction text

#### Scenario: heartbeat independent of consumer
ID: `streams.heartbeat-independent-of-consumer`
- WHEN a consumer blocks inside the iterator for twice `LeaseTTL`
- THEN the run's lease stays fresh and `Recover` on another pod reclaims nothing

#### Scenario: slow consumer causes no idle retry
ID: `streams.slow-consumer-no-idle-retry`
- WHEN the provider streams at full speed and the consumer takes one event per `Timeout.Idle`
- THEN the model call completes without an idle timeout and the model is called once

#### Scenario: consumer stall preempts
ID: `streams.consumer-stall-preempts`
- WHEN the consumer takes no event for longer than `ConsumerStall`
- THEN the run suspends `Preempted` at its next safe point, the lease is released, and `Resume(token, Continue())` continues the run

#### Scenario: consumer stall detaches with log
ID: `streams.consumer-stall-detaches-with-log`
- WHEN the flow is built with `OnStall(Detach)` and an `EventLog`
- THEN the run continues detached and `Attach(runID, lastSeq)` delivers the events the client missed

#### Scenario: stream buffer bound
ID: `streams.stream-buffer-bound`
- WHEN the consumer stalls and the provider has produced more than `StreamBuffer` chunks
- THEN the provider read blocks, no chunk is dropped or reordered, and `gohan.stream.buffer_full` increments

#### Scenario: detached reconnect
ID: `streams.detached-reconnect`
- WHEN a `Detached` conversation run is in progress, a client consumed events up to `Seq 17`, disconnected, and calls `Attach(runID, 17)`
- THEN it receives events 18.. in order with no duplicates, including live events after catch-up

#### Scenario: detached requires log
ID: `streams.detached-requires-log`
- WHEN a flow is built with `Detached()` and no `EventLog`
- THEN the flow constructor returns an error
