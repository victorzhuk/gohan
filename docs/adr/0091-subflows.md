# ADR-0091: Sub-flow contract

Status: accepted · Origin: grill round 17 (2026-09-29)

## Decision

`FlowAsTool`, `fork` and `MapReduce` children follow one contract (`openspec/specs/subflows/`): fresh history in a child session `<parent>/<callID>` on the same stores, only typed `In` crosses, typed `Out` returns as a guarded tool result; `ToolSpec.Effect` derived from the child's tools (`SideEffect` dominates, explicit effect may only tighten); nested suspension by token chaining (`Checkpoint.Child`, parent `Resume` routes into the child, both single-use); `RunLimits.MaxDepth` (2) and `MaxParallelChildren` (8), mandatory child `MaxWallClock` ≤ parent's remaining, `CostShare` `Remaining | Fixed`; fan-out `OnError: FailFast | Collect`; parent ctx cancels children with the `SideEffect` shield intact; child events carry `ParentRunID`/`Depth` in the parent's stream. Synchronous children only in v1; detached children are Q25. No dynamic free-text `spawn` tool.

## Context and evidence

Published harness experience converges on isolated child contexts with explicit inputs, structured outputs, depth 1–2, fan-out ~8, mandatory timeouts and early abort; none of it addresses approval inside a child, which the excursions example needs (booking inside an itinerary sub-flow). A free-text spawn surface would bypass `In` typing and make effect and approval derivation impossible.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

New capability `subflows`; `stores.Checkpoint` gains `Child`; `limits.RunLimits` gains `MaxDepth`, `MaxParallelChildren`; `streams.EventMeta` gains `ParentRunID`, `Depth`; `languages` `fork` gains `onError`. Extends ADR-0031 and ADR-0042.
