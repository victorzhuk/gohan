# Proposal: m2-context-persisted-compaction

## Why

The `context` capability splits history shaping into two stages with different persistence, and only the first has code. `stores.Compactor` is declared (`core/stores/projection.go:20-26`) but nothing calls it; `ContextPolicy.Compactor` is carried but never resolved (`core/build_options.go:205-213`); no `Summarize` exists in `std/context`; and the assembly wrapper composes projections directly into the caller assembler (`core/native_effects.go:31`) without ever collapsing a persisted `Compaction` block. Five registered scenario ids therefore have no public-path behaviour: `context.compaction-persisted`, `context.replay-after-compaction`, `context.pending-call-survives`, `context.summary-guarded`, `context.compaction-audited` (`openspec/changes/m2/design.md:53-73`, all `deferred_to: M2`).

Without this change a long session overflows and re-summarizes every turn: the compaction is never written to `SessionLog`, so `Replay` diverges and the audit trail has no `Compacted` record. The projection stage that must run underneath it already ships (`m2-context-projections`, archived 2026-10-07); persisted compaction is the seam it was built for (`openspec/specs/context/spec.md`, *Contract*, "Two stages with different persistence", rule 7).

## What Changes

- Reconcile the shipped `stores.Compactor` stub with the capability Contract: `Compact(ctx, History, types.ModelProfile) (types.Compaction, error)`, returning the persisted block rather than a reduced `History` (`core/stores/projection.go:24-26`; `openspec/specs/context/spec.md`, *Contract*). The `ContextPolicy.Compactor` field is unchanged, so no frozen struct gains a field.
- Add the compaction view to the native assembly path: before the declared projections, collapse the loaded history by the latest `types.Compaction` block, dropping the first `CoversUpTo` messages and rendering the block in their place. The store keeps `History.Version == len(History.Messages)` (`core/stores/session_memory.go:331-385`), so the mapping is exact and replay reads it straight off the log.
- Add `std/context.Summarize`: a `stores.Compactor` that folds the covered messages into a `CompactionText` block whose `Summary` comes from a declared `SummarySource`. The `PromptSet.Compaction` instruction, tuning and policy source stay with M4 (`openspec/changes/m2/design.md` §3 item 2).
- Add the compaction stage to the native drive: decide via `ContextBudget`/`NeedsCompaction` (`core/types/context_budget.go:16-58`), run the compactor, clamp `CoversUpTo` below the earliest pending `ToolUse`, guard the summary through `ConsultGuard` at `StageContext` (`core/telemetry.go:75`, `std/guard/context.go`), and on rejection persist nothing, fall back to `Truncate` and count `gohan.guard.context_rejected{provider="compaction"}`.
- Persist: append the guarded `Compaction` block to `SessionLog` through the run's `HistoryAppender` (`core/drive_turn.go:581-590`), write the `Compacted` audit record (`core/stores/audit.go`) and emit the `types.Compacted` event (`core/types/event.go:92-99`).
- Public-path coverage in `core` over `Build`, `Send` and `Resume`, and unit coverage in `std/context` and `core/stores`, with each owned id bound to a named subtest.
- **Not breaking:** a flow with no `Compactor` and no `Compaction` block in its log assembles exactly as today; the view step is a no-op when the log carries no compaction.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `context`: Complete the *Compaction is persisted* requirement with the append-and-hide mechanics, the `CoversUpTo` clamp, replay equality, the guarded-summary fallback with its counter, and the `Compacted` audit record and event (`openspec/specs/context/spec.md`, *Compaction is persisted*). Opaque model binding is not part of this change: those two scenario blocks are reproduced only because the MODIFIED block must stay complete.

## Impact

The package touches `core/stores` (the `Compactor` signature, beside `History`), `std/context` (`Summarize` beside `Truncate` and `ClearToolResults`), `core/native_effects.go` (one view step in the assembly composition), `core/drive_turn.go` (one compaction stage), `core/stores/audit.go` (the `AuditCompacted` kind and its record payload) and `docs/design/types.md` (the regenerated index). It adds no store interface and no new capability. Provider compaction, calibration, the overflow retry and the `Summarize` policy surface stay out of scope with `m2-provider-compaction` and `m2-context-calibration-overflow` (`openspec/changes/m2/design.md:53-73`).
