# Proposal

## Why

ADR-0091 froze the depth-1 sub-flow contract: token chaining through `Checkpoint.Child`, `MaxDepth`/`MaxParallelChildren`, `ChildLimits` cost shares and the fan-out `OnError` modes. M3 workflow processes need the contract to hold across more than one nesting level and under concurrent fan-out: suspension that chains through grandparent checkpoints, limits that inherit down the tree and account per child, and fan-out result and cost collection the parent can reason about. Today the spec proves these properties only at depth 1 and for a single child.

## What Changes

- `subflows` nested suspension is specified across nesting levels: a suspension at depth 2 chains a token through every intermediate checkpoint in order, resume consumes exactly one token per level, and recovery replays the whole chain.
- `subflows` limits gain tree inheritance: a child's effective wall clock, cost budget and depth allowance are derived from the parent's remaining budget at the call boundary, and each fan-out child contributes its own spent cost and reached depth to the parent's accounting.
- `subflows` fan-out gains the collection contract: results are collected keyed to their inputs regardless of completion order, `Collect` reports per-child cost and depth alongside `*PartialError`, and the concurrency cap is enforced per fan-out wave, not per process lifetime.

Existing scenarios, ids and error classes are unchanged. Everything stays on the existing seams (`FlowAsTool`, `Checkpoint.Child`, `RunLimits`, `EventMeta.ParentRunID`/`Depth`, `*LimitExceededError`, `*PartialError`); the change adds no new port methods, only specified behavior on frozen ones.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `subflows`: nested suspension across levels, limit inheritance and per-child accounting, fan-out collection and per-wave concurrency.

## Impact

Implementation touches `core` (checkpoint chaining, limit derivation at the call boundary), `std/flow` (`fork`, `MapReduce` collection) and `stores.Runs` (an optional per-run cost surface, discovered by type assertion; no port method is added). Spec deltas only; `openspec/scenarios.json` registration and `docs/design/types.md` regeneration happen at admission (task 1.1), per ADR-0171. No frozen contract changes. One ADR, `docs/adr/0182-nested-subflows.md`, records the decision.

Two deltas: `specs/subflows/` carries every extended behavior in the existing `subflows` capability, whose spec already owns suspension, limits and fan-out; `specs/stores/` carries the optional `RunCostRecorder` surface that makes a child's own spend readable from its run row. No other capability boundary is crossed.
