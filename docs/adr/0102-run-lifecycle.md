# ADR-0102: Run lifecycle ordering

Status: accepted · Amended by ADR-0110 (graceful shutdown), ADR-0114 (tool batch protocol) and ADR-0116 (slow consumers). · Origin: review round 28 (2026-09-29)

## Decision

`runtime` gains a normative *Run lifecycle* section: before `Drive` (principal → lease → load → frozen flags → append input), per step (assemble from `AssembleInput` built by `Drive` → model chain → append assistant message → per tool: gate → reserve → call → complete → batched result append → heartbeat → limits → events to `EventLog`), on suspend (checkpoint → `Runs.Suspend` → `Suspended` event), on done (`Verify` → `Runs.Finish` → `Done` last). `AgentRun.Assemble` takes `AssembleInput`; `AgentRun.History` is `History`; `State.HistoryVersion` replaces `HistoryLen`; `Step` returns only runtime-originated events. Lease defaults are constants in `stores`. Specs cite ADRs, not archived D-numbers.

## Context and evidence

The pieces existed across five specs but the order — which decides crash-window correctness — did not; two assembly signatures and an ambiguous `Step` return would have been guessed by the implementer.

## Consequences

Three ordering scenarios in `runtime`; task 22 updated; Q14 resolved.
