# Tasks

Risk depth: existing-service-strict. Scenario ids are proposed at authoring time and registered at admission (ADR-0171). A task is done only when its listed scenario ids pass and `task lint`, `task spec` and `task test` are green.

## 1. Contracts

- [ ] 1.1 Register the delta's scenario ids in `openspec/scenarios.json` as proposed and regenerate `docs/design/types.md`; sync the approved `subflows` delta into the main spec. Verify `openspec validate m3-subflows --strict` and `task spec:types`.
  Acceptance: all ids below registered; strict validation green; types index regenerated without unrelated churn.
- [ ] 1.2 Freeze the effective-budget derivation (wall clock, cost, depth allowance) and the per-level checkpoint chain shape against `Checkpoint.Child` and `Runs` as shipped. Verify review against ADR-0091 and the suspension capability with no frozen-contract changes.
  Acceptance: derivation and chain shape match `docs/design/types.md`; no port method additions.

## 2. Behavior

- [ ] 2.1 Implement and prove `subflows.suspension-depth-two-chain`, `subflows.resume-consumes-chain` and `subflows.inner-token-consumed-by-chain`: per-level checkpoints, one token consumed per `Resume`, chain completion order. Update existing architecture/testing guidance for the behavior.
  Acceptance: `subflows.suspension-depth-two-chain`, `subflows.resume-consumes-chain`, `subflows.inner-token-consumed-by-chain` pass as exact-ID public acceptance subtests.
- [ ] 2.2 Implement and prove `subflows.nested-depth-allowance`: effective depth budget `MaxDepth - parent depth` checked at the call boundary before any grandchild starts.
  Acceptance: `subflows.nested-depth-allowance` passes with `*LimitExceededError{Limit: "MaxDepth"}` asserted before child start.
- [ ] 2.3 Implement and prove `subflows.nested-cost-inheritance`: `ShareRemaining` resolves against the tree's remaining budget, and over-spend aborts only the child that holds the budget.
  Acceptance: `subflows.nested-cost-inheritance` passes; the parent continues after the depth-2 abort.
- [ ] 2.4 Implement and prove `subflows.nested-wall-clock-inheritance`: `min(declared, parent remaining)` recorded on the child run and reused by a resumed depth-2 child.
  Acceptance: `subflows.nested-wall-clock-inheritance` passes; the recorded budget survives pod change via `Resume`.
- [ ] 2.5 Implement and prove `subflows.fanout-results-keyed` and `subflows.fanout-partial-positions`: results collected in input order under out-of-order completion; `*PartialError` names failed input positions with nil placeholders.
  Acceptance: `subflows.fanout-results-keyed` and `subflows.fanout-partial-positions` pass for `MapReduce` and `fork`.
- [ ] 2.6 Implement and prove `subflows.fanout-cost-attribution` and `subflows.fanout-per-wave-cap`: per-child spend on each `Runs` record summing into the tree budget; `MaxParallelChildren` enforced per wave.
  Acceptance: `subflows.fanout-cost-attribution` and `subflows.fanout-per-wave-cap` pass across two overlapping fan-out waves.

## 3. Integration

- [ ] 3.1 Run the assigned scenarios, a crash-resume test over a chained checkpoint, and a race check on concurrent fan-out waves. Retain assertion-level evidence, process deadlines and worker bounds.
  Acceptance: chained suspension resumes after simulated crash; `go test -race` green on the touched packages with bounded workers.
- [ ] 3.2 Verify `task lint`, `task spec`, `task test` and `task api:check` after inspecting wrappers; update `[Unreleased]` for shipped behavior.
  Acceptance: all four targets green; changelog entry covers nested suspension, limit inheritance and fan-out accounting.
