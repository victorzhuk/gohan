# ADR-0090: Context management — projections and persisted compaction

Status: accepted · Origin: grill round 16 (2026-09-29)

## Decision

`ContextPolicy` splits into two stages with different persistence. *Projections* (`Truncate`, `ClearToolResults{Keep, ClearAtLeast, Exclude}`) are deterministic assembly-time views over `History` and are never written. *Compaction* (`Summarize` harness-side via `PromptSet.Compaction`, or `ProviderCompact` when `Caps.Compaction` is text or opaque) appends a persisted `Compaction` block to `SessionLog` with `CoversUpTo`; assembly hides everything it covers. Invariants: pending or unknown-outcome tool calls are never covered; `ToolUse`/`ToolResult` pairs are never split; `notes_*` results are never cleared; a text summary carries `OriginModel` and passes `StageContext` guards; an opaque compaction is model-bound, dropped on endpoint change, and rejected by `Build` under cross-provider `Fallback`; every compaction is audited and emitted as `Compacted`.

## Context and evidence

Both major providers ship compaction as an API primitive with incompatible shapes: a readable summary block with custom instructions on one side, an opaque encrypted model-bound item that must be passed as-is on the other; plus harness-side tool-result clearing (keep-last-N, exclude list, batched clearing, `tool_use` retained). An unpersisted compaction re-summarizes every turn (cost, non-determinism, cache churn) and makes `Replay` diverge; a mutated `SessionLog` breaks audit and replay. Persisting the compaction and projecting the rest keeps `SessionLog` the single source of truth.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

New capability spec `openspec/specs/context/`; `messages` gains the `Compaction` block; `model` gains `Caps.Compaction`; `build` gains a rejection; `streams` gains `Compacted`; `telemetry` gains `gohan.context.*`. Supersedes the "`Summarize`, `Compact` later" note of ADR-0026's assembly contract.
