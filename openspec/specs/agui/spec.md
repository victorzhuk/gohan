# Client transport: AG-UI

Capability: `agui` · Spec v1.5 (ADR-0131) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

Delivers a gohan `Conversation` to browsers and apps over the AG-UI event protocol through `adapter/agui`, and names the three core additions that make the mapping lossless: message ids, reasoning deltas and typed shared state. Out of scope: rendering (CopilotKit and similar sit above), A2A (`docs/design/adapters.md`).


## Contract

### Core additions

`Message.ID` (`messages`): assigned by `SessionLog.Append`, unique per session, stable across replay and compaction; `TextDelta`, `AssistantMessage` and `ReasoningDelta` carry `MessageID`.

`ReasoningDelta{Turn int; MessageID string; Delta string}` (`streams`): emitted only when the profile's reasoning is exposable (`Caps.ReasoningVisible`); otherwise the opaque `Reasoning` block is carried once as an encrypted value at message end.

`StateChanged{Version int64; Patch []PatchOp}` (`streams`) and shared state (`working-state`):

```go
func SharedState[S any](ctx context.Context) (S, int64, bool)
func SetSharedState[S any](ctx context.Context, next S) (int64, error)

type PatchOp struct {
	Op    string
	Path  string
	Value any
}
```

Shared state is typed, JSON-serialisable, versioned, persisted in session metadata and restored on resume; `SetSharedState` appends a `StateChanged` with an RFC 6902 patch computed by `std/state` (core carries the `PatchOp` type only); a client-supplied state on run input passes `StageInput` and replaces the current value with a new version. `HandedOff` is removed from `Event` (reserved since ADR-0031, never emitted); subagent events carry the tree.

### `adapter/agui` server

`RunAgentInput{threadId, runId, parentRunId, messages, tools, state, forwardedProps}` → `Conversation.Send(threadId, lastUserMessage)` or `Resume(token, in)` when the input answers an interrupt. Mapping:

| gohan | AG-UI |
|---|---|
| run start / `Done` / error | `RUN_STARTED{threadId=SessionID, runId=RunID, parentRunId}` / `RUN_FINISHED{outcome: success}` + `metadata{usage, cost, traceId}` / `RUN_ERROR` |
| `TextDelta`…`AssistantMessage` | `TEXT_MESSAGE_START/CONTENT/END{messageId=Message.ID}` |
| `ReasoningDelta`, opaque `Reasoning` | `REASONING_MESSAGE_*` / `REASONING_ENCRYPTED_VALUE` |
| `ToolArgsDelta` | `TOOL_CALL_START` on the first fragment, then `TOOL_CALL_ARGS` per fragment |
| `ToolStarted{ToolUse}` | `TOOL_CALL_END` (after any remaining `TOOL_CALL_ARGS`) |
| `ResultDelta` | `TEXT_MESSAGE_CONTENT` on the structured message id |
| `ToolFinished` | `TOOL_CALL_RESULT{toolCallId, messageId, content}` |
| `Suspended{Token, Reason, Payload}` | `RUN_FINISHED{outcome: interrupt, interrupts: [{id=token, reason, payload}]}` |
| child run events (`ParentRunID`, `Depth`) | `SUBAGENT_STARTED/FINISHED/ERROR{subagentRunId}`; child events carry `subagentRunId` |
| `StateChanged` | `STATE_SNAPSHOT` at run start, `STATE_DELTA{delta}` thereafter |
| `Compacted`, `GuardBlocked`, `LimitWarning` | `CUSTOM{name: "gohan.<event>"}` |
| `Seq` | SSE `id:`; `Last-Event-ID` reattaches through `EventLog` |

Rules:

1. **Frontend tools.** Each `tools[]` entry in the input registers for that run as an `Untrusted` tool with `MaxEffect: ReadOnly`, `Deferred: false`, whose `Call` suspends `AwaitingTool{toolCallId}`. The client's `TOOL_CALL_RESULT` in the next input becomes `Resume(token, Deliver(content))`; the result passes `StageToolResult`. Raising the effect is an explicit server-side option per tool name. Frontend tools are `Untrusted`, so `Capabilities.Exfil` defaults to true (`taint`): a tainted value reaching the client is denied by the default policy; `WithExfil(false)` per tool name opts a trusted UI out.
2. **Interrupt answers.** An input carrying interrupt responses maps by reason: `HumanApproval` → `Approve`/`Reject`/`EditArgs` with `Approver` set by the adapter from the transport principal; `AwaitingInput` → `Deliver(data)` validated against the schema; unknown or consumed tokens → `RUN_ERROR{code: "token"}`.
3. **Statelessness.** The adapter holds nothing between requests; `Checkpoints`, `Runs`, `EventLog` and session metadata are the only state. Reconnect with `Last-Event-ID` replays from `EventLog` then follows live.
4. **Transports.** SSE and WebSocket; the event JSON is identical.
5. **Namespace.** Text, reasoning and activity ids share one namespace: `Message.ID` for text and reasoning, `"<Message.ID>#<n>"` for activity.


## Requirements

### Requirement: Core additions

#### Scenario: input on live run steers
ID: `agui.input-on-live-run-steers`
- WHEN a `RunAgentInput` with one new user message arrives for a thread whose run is `Running`
- THEN the adapter calls `Steer` and the message appears in the stream as a user message after the current tool results; when `Steer` returns `ErrRunNotActive` the adapter starts a new run instead

#### Scenario: message id stable
ID: `agui.message-id-stable`
- WHEN a session is replayed on another pod after compaction
- THEN every surviving message keeps the `ID` it had at append

#### Scenario: reasoning gated
ID: `agui.reasoning-gated`
- WHEN the profile has `ReasoningVisible: false`
- THEN no `ReasoningDelta` is emitted and the assistant message end carries the encrypted value once

#### Scenario: state patch
ID: `agui.state-patch`
- WHEN a flow calls `SetSharedState` changing one field of a three-field struct
- THEN one `StateChanged` is emitted whose patch has a single `replace` op and the version increments by one

#### Scenario: client state guarded
ID: `agui.client-state-guarded`
- WHEN run input carries `state` that the `StageInput` guard rejects
- THEN the run does not start and `RUN_ERROR{code: "guard"}` is sent

### Requirement: Mapping

#### Scenario: interrupt round-trip
ID: `agui.interrupt-roundtrip`
- WHEN a run suspends for `HumanApproval`
- THEN the client receives `RUN_FINISHED{outcome: interrupt}` with the token, and a following input with an approval resumes the same run with `Approver` set

#### Scenario: frontend tool
ID: `agui.frontend-tool`
- WHEN the model calls a tool that came from `RunAgentInput.tools`
- THEN the stream carries `TOOL_CALL_START/ARGS/END`, the run suspends `AwaitingTool`, and the client's `TOOL_CALL_RESULT` resumes it with the content as a guarded tool result

#### Scenario: frontend tool effect capped
ID: `agui.frontend-tool-effect`
- WHEN a frontend tool is registered without a server-side raise
- THEN its effect is `ReadOnly` and it is `Untrusted`

#### Scenario: subagent attribution
ID: `agui.subagent-events`
- WHEN a `FlowAsTool` child runs
- THEN `SUBAGENT_STARTED`/`SUBAGENT_FINISHED` bracket its events and each carries `subagentRunId == child RunID`

#### Scenario: reconnect
ID: `agui.reconnect-last-event-id`
- WHEN a client reconnects with `Last-Event-ID: 17`
- THEN events with `Seq > 17` are replayed from `EventLog` before live events, with no gap or duplicate

#### Scenario: run error carries code
ID: `agui.run-error-code`
- WHEN a run fails with `ErrSessionForbidden`
- THEN the client receives `RUN_ERROR{code: "gohan.session_forbidden", message: <title>}` and nothing else about the session

#### Scenario: tool call args per delta
ID: `agui.tool-call-args-per-delta`
- WHEN the model streams four argument fragments
- THEN the client receives `TOOL_CALL_START`, four `TOOL_CALL_ARGS` and one `TOOL_CALL_END` for the call

#### Scenario: regenerate maps to fork
ID: `agui.regenerate-maps-to-fork`
- WHEN `RunAgentInput.messages` is a strict prefix of the thread's history followed by a changed or removed last assistant message
- THEN the adapter calls `ForkSession` at the last common message and `Continue` (regenerate) or `Send` (edit), and the new `threadId` is returned in the run's metadata

#### Scenario: consumed token
ID: `agui.consumed-token`
- WHEN an input answers an interrupt whose token was already consumed
- THEN the adapter returns `RUN_ERROR{code: "token"}` and no component runs
