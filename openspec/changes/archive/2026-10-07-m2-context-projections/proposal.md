# Proposal: m2-context-projections

## Why

The `context` capability splits history shaping into two stages, and only the first one has any code. `std/context.Truncate` is the sole projection in the tree (`std/context/truncate.go:12-22`), it is declared nowhere — no `ContextPolicy`, no `Projection` type, no `ClearToolResults` — and the native path calls the caller assembler directly through `nativeAssemble` (`core/native_effects.go:215-243`). Five registered scenario ids therefore have no public-path behaviour at all: `context.projection-does-not-write`, `context.projection-deterministic`, `context.keep-last-n`, `context.notes-excluded`, `context.clear-at-least` (`openspec/scenarios.json:939-968`, all `deferred_to: M2`).

Without this change a long session can only lose turns, never stale tool output: the tool results the model has already consumed keep occupying the window forever, and there is no batched way to invalidate the cache prefix. Persisted compaction, which is the other half of the capability, depends on the projection stage existing first — a compaction decision is taken on the projected request (`openspec/specs/context/spec.md`, *Contract* rule 7 of the stage pair), so the later `m2-context-persisted-compaction` change has no seam to sit on until this one lands.

## What Changes

- Declare the projection contract in `core/stores`, beside `History`: a `Projection` that receives `(ctx, stores.History, types.ModelProfile)` and returns a projected history, plus `ContextPolicy{Projections, Compactor}` holding the ordered stage list. The floor package cannot hold it, because `core/stores` already imports `core/types`, so naming `stores.History` from `core/types` would form an import cycle (`openspec/specs/context/spec.md`, *Contract*).
- Apply the declared projections in order inside the native assembly wrapper, before the caller assembler, so the projected messages are what assembly reads and nothing else changes shape (`core/native_effects.go:215-243`; blob resolution, redaction and the operator-turn fence stay where they are, after the assembler).
- Implement `std/context.ClearToolResults`: `ToolUse.ID` identifies each result's tool, the last `Keep` tool uses are retained, every `ToolUse` block and every `ToolResult` position survives, and results for excluded tools — always `notes_read` and `notes_write` — are never cleared (`std/context/truncate.go` is the sibling projection and keeps the package's estimator and turn-grouping rules).
- Replace cleared content with a fixed deterministic placeholder while preserving `ID`, `Outcome`, `Error` and `Origin`, and decide `ClearAtLeast` through `ContextBudget` with the shipped estimator port so a firing clears at least the requested tokens or changes nothing (`core/types/context_budget.go:16-33`, `core/types/tokens.go:19`).
- Add `std/context` unit coverage for the projection rules and `core` public-path coverage over `Build`, `Send` and `Resume`, with each scenario id bound to a named subtest (`openspec/changes/m2/design.md` §2.2).
- **Not breaking:** a flow that declares no `ContextPolicy` assembles exactly as today, and the projection stage is a no-op until one is declared.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `context`: Complete the *Projections are views* requirement with the stage order, the `ClearToolResults` retention and exclusion rules, and the batched-clearing decision (`openspec/specs/context/spec.md`, *Projections are views*).

## Impact

The package touches `core/stores` (the projection and policy declarations, beside `History`), `std/context` (one new projection beside `Truncate`) and `core/native_effects.go` (one stage inside the existing wrapper), plus focused tests in `std/context` and `core`. Persisted compaction, summaries, calibration and the provider compaction caps are out of scope and stay with `m2-context-persisted-compaction`, `m2-context-calibration-overflow` and `m2-provider-compaction` (`openspec/changes/m2/design.md:53-73`). The `Projection` and `ContextPolicy` shapes are new v1 surface, so they must be settled before the M2 API freeze (`docs/overview.md:108`).