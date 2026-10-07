# Spec Delta

## Context

This delta MODIFIES the `flow` *Flow contract* requirement, which is the capability's only requirement. The main requirement block carries one normative paragraph and twenty-five scenario blocks. This delta is a strict superset of that block: it reproduces every scenario unchanged and keeps the existing paragraph verbatim, then adds the title trigger and lock precedence, the `request_human` terminal class, and the hand-back fence shape the requirement lacked. The archive merge MUST union the added rules and MUST keep every reproduced scenario.

## MODIFIED Requirements

### Requirement: Flow contract

The recipe surface MUST stay the governed single-call recipes `flow.Extract`,
`flow.Classify` and `flow.Quarantine`. `Quarantine[Out]` MUST be `Extract`
restricted to untrusted content, offering no tool, and MUST return
`Quarantined[Out]`: the decoded value with the origins of the blocks it was
extracted from. The recording path MUST attach those origins to the recorded
result blocks.

The title generator MUST be `std/flow.Title`, an `Extract[string]` recipe of at
most six words that offers no tool. The harness MUST start it after the first
`Done` of a session's first primary run as its own governed run of
`Kind: SessionChild` with cost tag `title`, on the profile from
`agent.WithTitleProfile` (default: the cheapest configured profile), and MUST
write the result with `UpdateSession`. The primary run's `Done` MUST NOT be
delayed by the title run. Generation MUST be skipped when `TitleLocked` is set
or the session already carries a title, and the generated write MUST set
`TitleLocked`; a rename therefore always wins and a session is titled at most
once. A fork inherits the parent's title with the suffix `(edited)` and is not
titled again. Until a title exists the index holds an empty `Title`.

A model request for a human MUST be a terminal outcome, not a suspension. The
`request_human` built-in tool MUST return a `types.HandoffError` carrying the
`HandoffContext`; the error MUST join the control-error class that never renders
as a `ToolResult`, and the lifecycle MUST close the run with
`Done{Reason: StopHandedOff}` — never `Suspended` — with no `ResumeToken`, so
`Resume` cannot continue it. The same terminal transition MUST write
`ControlHandoffRequested`, persist the `HandoffContext` on the session index,
and audit the transition as `handoff_requested`.

After `HandBack` restores `ControlAgent`, assembly MUST fence the operator
turns: every history block whose origin is `OriginOperator` MUST be projected
with a preceding `PromptSet.OperatorTurn` marker so the operator turns are
wrapped as one marked segment, and the model's reply MUST carry `OriginModel`.
History MUST NOT be rewritten by the projection.

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
