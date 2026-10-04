# ADR-0114: Tool batch protocol — gate first, read-only in parallel, effects in order, asks last

Status: accepted · Origin: grill round 40 (2026-09-30) · Resolves Q7 · Amends ADR-0102

## Decision

A turn's tool calls are one batch. All calls are gated and reserved against `MaxToolCalls` before any executes. `ReadOnly` calls run concurrently under `RunLimits.MaxParallelTools` (default 8, 4 for batch); `Idempotent`/`SideEffect` calls run sequentially in call order afterwards with the existing journal and cancel-shield rules. `Ask` calls suspend one at a time after the allowed work, with the unchanged single `ApprovalRequest`. Exactly one result per call is appended in call order; unexecuted calls carry `Failed(Permanent)` with a `not_executed:` reason. `Caps.ParallelTools == false` or `agent.SequentialTools()` makes the adapter request single-call turns. No scheduling option exists; behaviour derives from `Effect`.

## Context and evidence

Providers leave execution order to the client and require a complete result set, with an error result for calls deliberately not run. Sequential execution cost latency on the common read-only fan-out and left the semantics of a deny, limit overrun or approval in the middle of a batch unwritten. Keeping side effects sequential preserves the one-reserved-unknown-at-a-time recovery story.

## Consequences

`runtime` v1.2 (batch protocol, eight scenarios), `limits` v1.3 (`MaxParallelTools`), `build` option `SequentialTools()`; task 22 updated; Q7 resolved.
