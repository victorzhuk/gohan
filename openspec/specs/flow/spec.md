# Flow and Conversation

Capability: `flow` · Spec v1.5 (ADR-0131) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `flow` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.2 Flow and Conversation

```go
type AbortError struct {
	Reason string
}

var ErrNoPrincipal = errors.New("gohan: no principal in context")
var ErrEmptyHistory = errors.New("gohan: session has no messages")

type Flow[In, Out any] interface {
	Invoke(ctx context.Context, in In) (Out, error)
	Resume(ctx context.Context, t ResumeToken, r ResumeInput) (Out, error)
}

type Conversation interface {
	Send(ctx context.Context, sessionID string, msg Message) iter.Seq2[Event, error]
	Continue(ctx context.Context, sessionID string) iter.Seq2[Event, error]
	Resume(ctx context.Context, t ResumeToken, r ResumeInput) iter.Seq2[Event, error]
	Cancel(ctx context.Context, sessionID string) error
	Steer(ctx context.Context, sessionID string, msg Message) error
}

func FlowFunc[In, Out any](name string, fn func(context.Context, In) (Out, error)) Flow[In, Out]

type FeedbackTarget struct {
	SessionID string
	RunID     string
	MessageID string
}

type FeedbackSource int

const (
	Explicit FeedbackSource = iota
	Implicit
)

type Feedback struct {
	Target     FeedbackTarget
	Name       string
	Value      any
	Comment    string
	Correction []Block
	Source     FeedbackSource
	Version    int64
}

func (s *Stack) Feedback(ctx context.Context, f Feedback) error

type HandoffContext struct {
	Reason string
	Skill  string
	Fields map[string]string
}

var ErrSessionHandedOff = errors.New("gohan: session is under human control")

func (s *Stack) TakeOver(ctx context.Context, sessionID string, h HandoffContext) error
func (s *Stack) HandBack(ctx context.Context, sessionID string) error
func (s *Stack) OperatorSend(ctx context.Context, sessionID string, msg Message) error
```

Implementations:

| Constructor | Backend |
|---|---|
| `gohan.FlowFunc` | plain Go / mock; `Resume` returns `ErrNotSuspendable` |
| `agent.New[In, Out](stack, spec, runtime, opts...) (Flow[In, Out], error)` | agent loop on `native`, `eino`, `adkgo` |
| `agent.NewConversation(stack, spec, runtime, opts...) (Conversation, error)` | same, streaming chat |
| `einoflow.FromRunnable[In, Out](stack, r, opts...)` | eino `compose.Runnable[I, O]` |
| `adkflow.FromAgent[In, Out](stack, a, opts...)` | adk-go `agent.Agent` incl. workflow agents |
| `flow.Extract[Out](stack, profile string, opts...) Flow[[]Block, Out]` | single model call with `StructuredOutput`; no tools (M0) |
| `flow.Classify[L ~string](stack, profile string, labels []L, opts...) Flow[[]Block, L]` | constrained single-label choice (M0) |
| `flow.Route[In, Out](stack, decider, routes map[string]Flow[In, Out]) Flow[In, Out]` | decider-selected sub-flow (M1) |
| `flow.Pipeline[A, B, C](f Flow[A, B], g Flow[B, C]) Flow[A, C]` | sequential composition (M1) |
| `flow.MapReduce[In, Item, Out](stack, split, each Flow[Item, Out], reduce, opts...)` | fan-out with `OnError`, `MaxParallelChildren` (M3, `subflows`) |
| `flow.RAG[In, Out](stack, retriever, answer Flow[[]Block, Out])` | retrieval as a `ContextProvider` around a flow (M2) |
| `flow.Judge[In](stack, profile string, rubric) Decider[In, Score]` | LLM judge as a decider (M4, evals) |

Streams returned by `Send`, `Resume` and `Attach` follow the error-tuple protocol in `streams`; non-streaming callers use `gohan.Last`/`gohan.Collect`.

**Takeover.** A session's `SessionMeta.Control` is `ControlAgent`, `ControlHandoffRequested` or `ControlHuman`. `TakeOver`, `HandBack` and `OperatorSend` require a principal of the owner's tenant with `session:control`; each is audited (`handoff_requested`, `handoff_accepted`, `handoff_completed`, `operator_message`) with the principal. `TakeOver` on a session with a live run cancels it at its next safe point and expires pending approval tokens (`not_executed: handed_off`), then sets `ControlHuman`. While `ControlHuman`: `Send` appends the user message, emits `Done{Reason: StopHandedOff}` at once, runs nothing and costs nothing; `Continue` and `Resume` fail with `ErrSessionHandedOff`; notes and shared state are untouched. `OperatorSend` appends the message with `RoleAssistant` and `OriginOperator{Subject}` and streams it to attached clients as `AssistantMessage{Operator: true}`. A flow or tool requests a human with `gohan.SuspendTool(HumanHandoff, HandoffContext{…})` (`std/flow` ships the `request_human` tool, `ReadOnly`, `Trusted`): the run ends `Done{Reason: StopHandedOff}` and `Control` becomes `ControlHandoffRequested` for transports and `Waker` to route. `HandBack` sets `ControlAgent`; the next turn assembles operator turns as assistant history wrapped by `PromptSet.OperatorTurn` so the model knows it did not write them, and `Continue` may run to let the agent summarise or close. `evals.Import` never uses an operator turn as an expected output unless `evals.IncludeOperator()` is set.

**Titles.** `std/flow.Title` is an `Extract[string]` recipe (at most six words, no tools) run by the harness after the first `Done` of a session's first primary run, as its own governed run of `Kind: SessionChild` with cost tag `title`, on the profile from `agent.WithTitleProfile` (default: the cheapest profile in the stack); the result is written with `UpdateSession`. A session whose `TitleLocked` is set is never retitled; a fork inherits the parent's title with the suffix `(edited)` and is not titled again. Until a title exists the index holds an empty `Title`.

**Feedback.** `Stack.Feedback` records a user or application signal about a run or one assistant message: `Value` is `bool`, `float64` or `string` (categorical); `Name` matches the tool-name grammar. It is owner-checked like `Inspect`, idempotent on `(Target, Name, Subject)` — a later call overwrites and bumps `Version` — and stored through the `FeedbackStore` port (`stores`). `Comment` and `Correction` carry `OriginUser` and are never assembled into a request, written to notes or subject memory, or shown to the model; they exist for evals and operators. Each call appends an audit record `feedback`, emits `FeedbackRecorded` on the run's `EventLog` when one exists, and increments `gohan.feedback{name, flow, release, variant}` (`telemetry`).

`Continue` runs one assistant turn on the existing history without appending input; it is owner-checked and follows the same lifecycle, lease, limits and idempotency rules as `Send`, and fails with `ErrEmptyHistory` on a session without messages. Regenerate is `Stores.ForkSession(s, lastUserMessage)` then `Continue`; editing a message is `ForkSession(s, messageBefore)` then `Send(edited)`.

Concurrency and idempotency at the seam:

- `Send`/`Invoke` on a session with a live lease returns `ErrRunActive`; core never queues. `Cancel(ctx, sessionID)` is owner-checked, posts `SignalCancel` to the run's mailbox (`stores`), which the lease holder observes at its next safe point (the `SideEffect` cancel shield is unchanged), and returns when `Runs` shows the run finished or `LeaseTTL` elapses.
- **Steering.** `Steer(ctx, sessionID, msg)` is owner-checked, runs `msg` through the `StageInput` guard exactly as `Send` does, and posts `SignalSteer{msg}` to the live run's mailbox; it returns `ErrRunNotActive` when the session has no `Running` run (the client then calls `Send`) and `ErrMailboxFull` past `MaxPendingSignals`. A suspended run (`HumanApproval`, `AwaitingInput`, …) is not `Running`. The runtime appends drained steers to history at the next safe point (`runtime`); a steer is never lost: steers arriving after the model's final reply make the run take one more turn. A transport's three modes are therefore: *steer* → `Steer`; *interrupt* → `Cancel` then `Send`; *queue* → wait for `Done`, then `Send` — the queue lives in the client, never in core.
- With `IdempotencyKey(ctx)` set, `Send`/`Invoke` dedup through `Runs.ByOperation(key)`: an existing run is reattached (`Conversation`, via `EventLog` from `Seq` 1) or its stored result returned (`Flow`, via `ResultRef`), and no message is appended; without a key there is no dedup.

Typed output on `Conversation`: when the underlying agent flow has `Out ≠ string`, the final `AssistantMessage` carries the value as one `Text` block of canonical JSON and `Done.Result` carries the same bytes as `json.RawMessage`; clients never parse assistant prose for structured results.

Agent flows map `In` to the user message via `Render func(In) []Block` (default: JSON of `In`, or the string itself) and produce `Out` via the `StructuredOutput` strategy (`Out = string` → final text).


## Requirements

### Requirement: Flow contract

#### Scenario: plain invoke
ID: `flow.plain-invoke`
- WHEN a `FlowFunc` returns `x`
- THEN `Invoke` returns `x, nil` and emits a `gohan.flow` span

#### Scenario: cancel from another request
ID: `flow.cancel-other-request`
- WHEN a second HTTP request calls `Cancel(sessionID)` while a `Send` streams on another pod
- THEN the run stops at its next safe point, `Done{Reason: cancelled}` is emitted, and `Cancel` returns after `Runs` shows the run finished

#### Scenario: steer applied at boundary
ID: `flow.steer-applied-at-boundary`
- WHEN `Steer(sessionID, "use the cheaper carrier")` is called while a tool batch executes
- THEN the batch completes, the steer is appended after the tool results as an `OriginUser` message, `SteerApplied{MessageID}` is emitted, and the next model call sees it

#### Scenario: steer preserves adjacency
ID: `flow.steer-preserves-adjacency`
- WHEN a steer is applied after a two-call batch
- THEN the assistant message with two `ToolUse` is followed by their two `ToolResult`s and only then by the steer

#### Scenario: steer after final reply runs a turn
ID: `flow.steer-after-final-reply-runs-turn`
- WHEN a steer is posted after the model produced a reply with no tool calls and before `Finish`
- THEN `Finish` returns `ErrSignalsPending`, the steer is appended and one more model turn runs, counted against `MaxTurns`

#### Scenario: steer with no active run
ID: `flow.steer-no-active-run`
- WHEN `Steer` is called on a session whose run is finished or suspended
- THEN `ErrRunNotActive` is returned, nothing is appended, and the client falls back to `Send`

#### Scenario: send during active run
ID: `flow.send-during-active-run`
- WHEN `Send` is called on a session whose lease is live
- THEN `ErrRunActive` is returned before any message is appended

#### Scenario: idempotent send
ID: `flow.idempotent-send`
- WHEN `Send` is retried with the same `IdempotencyKey(ctx)` after the first call succeeded
- THEN no second user message is appended, no second run starts, and the caller receives the original run's events from `Seq` 1

#### Scenario: extract recipe
ID: `flow.extract-recipe`
- WHEN `flow.Extract[Invoice]` runs over a document block with a scripted model returning valid JSON
- THEN `Invoke` returns the typed `Invoice`, the request carried the derived schema, and no tool was offered

#### Scenario: classify recipe
ID: `flow.classify-recipe`
- WHEN `flow.Classify` runs with labels `[refund, billing, other]` and the model returns `billing`
- THEN `Invoke` returns the typed label; a value outside the label set is a `ValidateRepair` retry then a `Permanent` error

#### Scenario: typed result on conversation
ID: `flow.typed-result-on-conversation`
- WHEN a `Conversation` wraps an agent flow with `Out = Itinerary`
- THEN the last `AssistantMessage` is one `Text` block of JSON and `Done.Result` unmarshals into `Itinerary`

#### Scenario: takeover pauses the agent
ID: `flow.takeover-pauses-agent`
- WHEN an operator calls `TakeOver` while a run is streaming with a pending `HumanApproval` token
- THEN the run stops at its next safe point, the token is expired with `not_executed: handed_off`, `Control` is `ControlHuman` and audit holds `handoff_accepted` with the operator

#### Scenario: operator message carries origin
ID: `flow.operator-send-origin`
- WHEN the operator calls `OperatorSend`
- THEN the message is appended with `RoleAssistant` and `OriginOperator{Subject}`, attached clients receive `AssistantMessage{Operator: true}`, and no model call occurs

#### Scenario: send during human control runs nothing
ID: `flow.send-during-human-control-no-run`
- WHEN the user sends a message to a `ControlHuman` session
- THEN it is appended, `Done{Reason: StopHandedOff}` is emitted, `Runs` records no run and `Continue` returns `ErrSessionHandedOff`

#### Scenario: request human ends the run
ID: `flow.request-human-ends-run`
- WHEN the model calls `request_human` with a reason
- THEN the run ends `Done{Reason: StopHandedOff}`, `Control` is `ControlHandoffRequested` with the `HandoffContext`, and no resume token exists

#### Scenario: hand-back fences operator turns
ID: `flow.handback-fences-operator-turns`
- WHEN `HandBack` is called after two operator messages and the user sends again
- THEN the assembled request contains both operator turns wrapped by `PromptSet.OperatorTurn` and the model's reply is `OriginModel`

#### Scenario: title generated after first done
ID: `flow.title-generated-after-first-done`
- WHEN a new session's first run completes
- THEN one child run with cost tag `title` produces a title of at most six words and the index shows it; the primary run's `Done` was not delayed by it

#### Scenario: title locked by rename
ID: `flow.title-locked-by-rename`
- WHEN the owner renames a session and later runs complete
- THEN no title run is started and the user's title stays

#### Scenario: feedback owner-checked
ID: `flow.feedback-owner-checked`
- WHEN a principal from another tenant calls `Stack.Feedback` for a session
- THEN `ErrSessionForbidden` is returned before any store write

#### Scenario: feedback idempotent per name
ID: `flow.feedback-idempotent-per-name`
- WHEN the same subject rates message `m3` `thumbs: false` then `thumbs: true`
- THEN one feedback row exists for `(m3, thumbs, subject)` with `Version` 2 and value `true`

#### Scenario: feedback never in context
ID: `flow.feedback-never-in-context`
- WHEN feedback with a `Comment` and a `Correction` is recorded and the session continues
- THEN no later request, note or memory entry contains the comment or correction

#### Scenario: continue without input
ID: `flow.continue-without-input`
- WHEN `Continue` is called on a session whose last message is from the user
- THEN one assistant turn runs, no user message is appended and `Done` is emitted

#### Scenario: regenerate is fork and continue
ID: `flow.regenerate-is-fork-and-continue`
- WHEN a client forks a session at its last user message and calls `Continue` on the fork
- THEN the fork gets a new assistant message, the parent's history is unchanged, and the fork's first model call reports cached input tokens for the shared prefix

#### Scenario: not suspendable
ID: `flow.not-suspendable`
- WHEN `Resume` is called on a `FlowFunc`
- THEN it returns `ErrNotSuspendable`

#### Scenario: backend swap
ID: `flow.backend-swap`
- WHEN the S1 acceptance test suite runs against the S1 native flow and the S4 eino graph flow with the same scripted model
- THEN both pass without changes to test code
