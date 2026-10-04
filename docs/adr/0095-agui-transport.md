# ADR-0095: AG-UI client transport and the three core additions

Status: accepted · Origin: grill round 21 (2026-09-29)

## Decision

`adapter/agui` is the server transport for browsers and apps: `RunAgentInput` maps to `Send`/`Resume`, gohan events map to the AG-UI catalog, frontend tools register per run as `Untrusted`, `ReadOnly`-capped tools whose call suspends `AwaitingTool` and resumes from the client's `TOOL_CALL_RESULT`, interrupts are `Suspended`, reconnect uses `Seq` as `Last-Event-ID` over `EventLog`. Three core additions make the mapping lossless and are useful without AG-UI: `Message.ID` assigned at append and stable across replay; `ReasoningDelta` gated by `Caps.ReasoningVisible`; typed, versioned shared state (`State`/`SetSharedState`) emitting `StateChanged` with RFC 6902 patches. `HandedOff` is removed. AG-UI vocabulary stays out of core.

## Context and evidence

The front-end side standardised on AG-UI while gohan's stream was Go-only; the catalogs are isomorphic except message ids, reasoning deltas and shared state, which the excursions and support-triage front ends need regardless of protocol.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

New capability `agui`; `messages.Message` gains `ID`; `streams` gains `ReasoningDelta`, `StateChanged`, `MessageID` on deltas and drops `HandedOff`; `model.Caps` gains `ReasoningVisible`; `working-state` gains shared state; `docs/design/adapters.md` lists `adapter/agui`. Amends ADR-0031.
