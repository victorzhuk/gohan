# Tasks

Risk depth: existing-service-strict. Scenario ids are proposed at authoring time and registered at admission (ADR-0171). A task is done only when its listed scenario ids pass and `task lint`, `task spec` and `task test` are green.

## 1. Contracts

- [x] 1.1 Register the delta's scenario ids in `openspec/scenarios.json` as proposed and regenerate `docs/design/types.md`; sync the approved `subflows` and `stores` deltas into the main specs. Verify `openspec validate m3-subflows --strict` and `task spec:types`.
  Acceptance: all ids below plus `stores.run-cost-optional` registered; strict validation green; types index regenerated without unrelated churn.
- [x] 1.2 Freeze the effective-budget derivation (wall clock, cost, depth allowance) and the per-level checkpoint chain shape against `Checkpoint.Child` and `Runs` as shipped. Verify review against ADR-0091 and the suspension capability with no frozen-contract changes.
  Acceptance: derivation and chain shape match `docs/design/types.md`; no port method additions.
  Evidence: Derivations and chain shape reviewed against the change's *Frozen contracts* section, `docs/design/types.md`, ADR-0091 and the `subflows`/`suspension`/`stores` specs. Port method sets compared and unchanged: `Runs`, `Checkpoints`, `CheckpointResumer`, `ResumeReadyLister`, `CheckpointRuns`, `RunCostRecorder` (the pre-declared optional surface), `Tool`, `Waker`. One real divergence surfaced and was fixed: a depth-2+ child derived its wall clock from the calling flow's configured limit rather than that flow's inherited effective budget, and a resumed level re-derived the same way. The ledger branch now carries the inherited budget and the call start (`BranchInherited`, `wallClockLimit`), the call boundary seeds it (`runSubflow`), and the resume path caps the recorded budget; `TestSubflowsLimits/subflow_nested_derivation_clamps_to_the_inherited_wall_clock` proves the clamp. Chain shape verified unchanged: one token per level in `Checkpoint.Child`, single-use, child-first unwinding. ADR-0091's `MaxWallClock` requirement holds at the effective level: the shipped rule refuses a declaration above the parent's declared limit, clamps one above the parent's remaining time, and takes the parent's remaining time when undeclared, as `design.md` records. `mustRefuseConsumed` now fails when a consumed token is accepted instead of refused. Gates after the fix: `task test`, `task lint` (0 issues, both legs), `task spec` (`missing_in_scope=0 failed_legs=0`).

## 2. Behavior

The depth-1 contract this change builds also proves, with the same fixtures, `subflows.nested-approval`, `subflows.nested-resume`, `subflows.nested-tokens-single-use`, `subflows.max-depth`, `subflows.child-wall-clock`, `subflows.fixed-cost-share`, `subflows.max-parallel-children`, `subflows.fail-fast`, `subflows.collect-partials` and `stores.run-cost-optional`. `subflows.fresh-history`, `subflows.child-session`, `subflows.output-guarded`, `subflows.effect-derived`, `subflows.effect-tighten-only`, `subflows.cancel-propagates` and `subflows.child-events` stay deferred: their machinery is built here, their assertions belong to a later M3 slice.

- [x] 2.1 Implement and prove `subflows.suspension-depth-two-chain`, `subflows.resume-consumes-chain` and `subflows.inner-token-consumed-by-chain`: per-level checkpoints, one token consumed per `Resume`, chain completion order. Update existing architecture/testing guidance for the behavior.
  Acceptance: `subflows.suspension-depth-two-chain`, `subflows.resume-consumes-chain`, `subflows.inner-token-consumed-by-chain` pass as exact-ID public acceptance subtests.
- [x] 2.2 Implement and prove `subflows.nested-depth-allowance`: effective depth budget `MaxDepth - parent depth` checked at the call boundary before any grandchild starts.
  Acceptance: `subflows.nested-depth-allowance` passes with `*LimitExceededError{Limit: "MaxDepth"}` asserted before child start.
- [x] 2.3 Implement and prove `subflows.nested-cost-inheritance`: `ShareRemaining` resolves against the tree's remaining budget, and over-spend aborts only the child that holds the budget.
  Acceptance: `subflows.nested-cost-inheritance` passes; the parent continues after the depth-2 abort.
- [x] 2.4 Implement and prove `subflows.nested-wall-clock-inheritance`: `min(declared, parent remaining)` recorded on the child run and reused by a resumed depth-2 child.
  Acceptance: `subflows.nested-wall-clock-inheritance` passes; the recorded budget survives pod change via `Resume`.
- [x] 2.5 Implement and prove `subflows.fanout-results-keyed` and `subflows.fanout-partial-positions`: results collected in input order under out-of-order completion; `*PartialError` names failed input positions with nil placeholders.
  Acceptance: `subflows.fanout-results-keyed` and `subflows.fanout-partial-positions` pass for `MapReduce` and `fork`.
- [x] 2.6 Implement and prove `subflows.fanout-cost-attribution` and `subflows.fanout-per-wave-cap`: per-child spend on each `Runs` record summing into the tree budget; `MaxParallelChildren` enforced per wave.
  Acceptance: `subflows.fanout-cost-attribution` and `subflows.fanout-per-wave-cap` pass across two overlapping fan-out waves.

## 3. Integration

- [x] 3.1 Run the assigned scenarios, a crash-resume test over a chained checkpoint, and a race check on concurrent fan-out waves. Retain assertion-level evidence, process deadlines and worker bounds.
  Acceptance: chained suspension resumes after simulated crash; `go test -race` green on the touched packages with bounded workers.
- [x] 3.2 Verify `task lint`, `task spec`, `task test` and `task api:check` after inspecting wrappers; update `[Unreleased]` for shipped behavior.
  Acceptance: all four targets green; changelog entry covers nested suspension, limit inheritance and fan-out accounting.
  Evidence: `adapter/otel` re-pinned to root `v0.3.0`, which carries the telemetry export contracts; `adapterverify` and the manifest-drift test now enforce a per-module published pin (`v0.3.0` for `adapter/otel`, `v0.2.0` for the rest). `task adapters:verify` reports PASS for all seven modules, `task api:check` reports the root `exported API compatible with v0.2.0` and `adapter/otel` `exported API compatible with adapter/otel/v0.1.0` after the historical correction (other adapters untagged, skipped), `task lint` reports 0 issues on both legs, `task spec` reports `missing_in_scope=0 failed_legs=0`, `task test` is green. The changelog `[0.3.0]` section records the release and the per-module pins. Because the sandbox mounts `.git` read-only and holds no GitHub credential, the gates resolved root `v0.3.0` from a local file proxy built from the exact release commit `f09ce2a`; the recorded `go.sum` hashes are content-derived and are re-checked against `proxy.golang.org` once the owner pushes the `v0.3.0` tag and `master`.
