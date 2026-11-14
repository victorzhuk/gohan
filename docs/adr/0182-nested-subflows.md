# ADR-0182: Nested sub-flows across levels, inherited limits and fan-out accounting

Status: proposed

## Decision

The sub-flow contract (ADR-0091) extends to nested trees without new ports:

1. **Checkpoint chain per level.** A suspension at depth `d` stores the level-below token in each intermediate `Checkpoint.Child` up to the root. `Resume` consumes exactly one token per level and drives the level below; the root completes only after every inner level finishes. All tokens stay single-use; presenting an inner token after its ancestor chain consumed it fails `ErrTokenConsumed`.
2. **Inheritance from remaining.** A child's effective budgets derive at the call boundary from the parent's remaining budget: wall clock `min(declared, parent remaining)`, cost `min(cost share, tree remaining)`, depth allowance `MaxDepth - parent depth`. Effective budgets are recorded on the child run so `Recover` replays the tree with the same budgets; exceeding an effective budget aborts only the child that holds it.
3. **Fan-out accounting.** Fan-out collects results keyed to input positions regardless of completion order; each child records its spent cost and final depth on its `Runs` record, and the parent tree budget reflects the sum of child spends. `*PartialError` under `Collect` identifies each failed child by input position. `MaxParallelChildren` bounds concurrent children per fan-out wave; queued children start as running ones finish.
4. **Cost share carries its cap; per-run spend is optional.** `ChildLimits` gains `FixedCost`, because the frozen `CostShare` enum cannot carry the cap value: `ShareFixed` reads `FixedCost` and `ShareRemaining` ignores it. Per-child spend rides the optional `RunCostRecorder` extension on `Runs`, discovered by type assertion; a store without it keeps that row's `Cost` at zero and the tree total stays authoritative. Neither addition is a port method.

## Context and evidence

ADR-0091 proved nested suspension, limits and fan-out failure at depth 1 with a single child. M3 workflow processes chain sub-flows two levels deep and fan out concurrently, which surfaces three unanswered questions: token behavior when more than one level suspends, what a child inherits when its parent already spends an inherited budget, and what a parent receives from a fan-out beyond the error split. Deep chains are bounded by `MaxDepth`, so the added checkpoint writes per suspension are constant; derived budgets must be deterministic across pods, which rule 2 guarantees by deriving only from persisted parent state.

## Consequences

No port method or stored-shape changes: `Checkpoint.Child`, `RunLimits.MaxDepth`/`MaxParallelChildren`, `ChildLimits`, `OnError` and `PartialError` keep their frozen shapes. Behavior lands in `core` (chain, derivation at the call boundary) and `std/flow` (`fork`, `MapReduce` collection). `subflows` gains the delta requirements `subflows` nested-suspension extension, nested limit inheritance and fan-out accounting with proposed scenario ids registered at admission per ADR-0171.
