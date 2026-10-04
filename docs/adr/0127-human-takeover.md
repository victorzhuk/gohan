# ADR-0127: Human takeover — session control state, operator origin, hand-back fencing

Status: accepted · Origin: grill round 53 (2026-09-30)

## Decision

`SessionMeta.Control` (`ControlAgent | ControlHandoffRequested | ControlHuman`) governs whether `Send` runs the model. `TakeOver`, `HandBack` and `OperatorSend` need `session:control` and are audited; takeover cancels a live run at its safe point and expires pending approvals. Operator replies are `RoleAssistant` with the new `OriginOperator{Subject}`, streamed as `AssistantMessage{Operator: true}`, trusted for taint but never model-authored for audit, evals or feedback. A flow requests a human with `SuspendTool(HumanHandoff, HandoffContext)`: the run ends `StopHandedOff` with no token. On hand-back the next turn wraps operator turns with `PromptSet.OperatorTurn`.

## Context and evidence

The handoff pattern hands the transcript and routing context to a human hub, keeps the bot out of the answer path while a human holds the conversation, and returns control on completion; support products expose the same pause/resume per conversation. gohan had no session-level pause, no operator authorship, and only a flags-related `AwaitingControl`, so a takeover would have meant forging assistant messages.

## Consequences

`flow` v1.4 (`HandoffContext`, `ErrSessionHandedOff`, three operations, rule block, five scenarios), `messages` v1.3 (`OriginOperator`, catalog row), `stores` v1.7 (`SessionControl`, query filter, one scenario), `streams` v1.5 (`StopHandedOff`, `AssistantMessage.Operator`), `suspension` v1.2 (`HumanHandoff`), `chains` (`PromptSet.OperatorTurn`), `permission` v1.3 and `release` v1.3 (one scenario each); task 23; `request_human` and fencing M1, evals M4.
