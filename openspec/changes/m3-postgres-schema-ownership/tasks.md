# Tasks

Risk depth: existing-service-strict. No skipped or flaky scenario passes. A task is done when its scenario ids pass, `task lint`, `task spec` and `task test` are green.

## 1. Contracts

- [ ] 1.1 Add ADR-0179, sync the approved delta, register the proposed scenario ids and regenerate `docs/design/types.md`.
  Acceptance: `task spec:types` clean; ids present in `openspec/scenarios.json` with proposed origin.
- [ ] 1.2 Finalize `MigrateOptions`, `Maintain` signature, the command surface and the testkit entries against existing adapter idioms.
  Acceptance: declarations compile in `adapter/postgres` and `testkit` without core changes; `task api:check` clean.

## 2. Behavior

- [ ] 2.1 Implement `Migrate` target-version support and the `gohan-postgres migrate` command over the same entry point.
  Acceptance: `stores.pg-migrate-command` passes.
- [ ] 2.2 Implement `Maintain`: rolling partition creation, `DROP PARTITION` retention and idempotent concurrent execution.
  Acceptance: `stores.pg-maintain-idempotent` and `stores.pg-partitions-ahead` pass.
- [ ] 2.3 Implement `SKIP LOCKED` batched reaping with coalesced heartbeats on the hot `runs` table.
  Acceptance: `stores.pg-hot-runs-reaping` passes under concurrent run workers.
- [ ] 2.4 Enforce the expand/contract skew rule: contract migrations gated on reader coverage, `MinSchema` refusing older readers.
  Acceptance: `stores.pg-expand-contract-skew` and `stores.pg-schema-too-old` pass.
- [ ] 2.5 Extend the static lint to fail any transaction held across a model or tool call.
  Acceptance: `stores.transaction-across-model-call` passes and the lint reports each offending span.
- [ ] 2.6 Add `storetest.Migrations` (fresh head equals migrated install) and `storetest.Bloat` (1 M cycles, autovacuum on, bounded p99 and dead tuples).
  Acceptance: `stores.pg-fresh-equals-upgraded` and `stores.bloat-bound` pass.

## 3. Integration

- [ ] 3.1 Run the affected store suites with process deadlines and worker bounds; retain assertion-level evidence for the bloat run.
  Acceptance: `adapter/postgres` suites green with no skipped subtest in the assigned scenarios.
- [ ] 3.2 Verify `task lint`, `task spec`, `task test`, `task api:check`; update the `[Unreleased]` changelog entry.
  Acceptance: all four tasks green; changelog names the new command, `Maintain` and the testkit additions.
