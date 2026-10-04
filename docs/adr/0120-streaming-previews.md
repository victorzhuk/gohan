# ADR-0120: Argument and result deltas are previews, never inputs

Status: accepted · Origin: grill round 46 (2026-09-30)

## Decision

`ModelChunk` gains `DeltaToolArgs`; `streams` gains `ToolArgsDelta` and `ResultDelta`. Both are previews for clients: never journaled, gated, tainted, appended or passed to a tool; execution starts only after the complete `ToolUse` passes `jsontext` validation and the batch protocol. `std/structured.Partial[Out]` renders a deep-partial view of a streaming typed result; `Done.Result` remains the only validated value. Detached runs coalesce deltas per `EventLogCoalesce` before logging. The AG-UI adapter emits `TOOL_CALL_ARGS` per fragment.

## Context and evidence

Agent frameworks stream tool-call argument fragments (`tool-call-delta`, `TOOL_CALL_ARGS`) so UIs can show file paths and forms as they fill, and generative-UI SDKs stream deep-partial objects; execution waits for completion. gohan emitted `ToolUse` only whole and typed results only at `Done`, and had no rule preventing an implementer from acting on partial arguments.

## Consequences

`model` v1.4, `streams` v1.2 (preview rule, two scenarios), `structured-output` v1.1 (`Partial`, two scenarios), `tools` v1.2, `agui` v1.3 (mapping rows, one scenario); tasks 20 and 26; AG-UI scenario M4.
