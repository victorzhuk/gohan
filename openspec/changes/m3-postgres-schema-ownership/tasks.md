# Tasks

Risk depth: existing-service-strict. No skipped or flaky scenario passes. A task is done when its scenario ids pass, `task lint`, `task spec` and `task test` are green.

## 1. Contracts

- [x] 1.1 Add ADR-0179, sync the approved delta, register the proposed scenario ids and regenerate `docs/design/types.md`.
  Acceptance: `task spec:types` clean; ids present in `openspec/scenarios.json` with proposed origin.
- [x] 1.2 Finalize `MigrateOptions`, `Maintain` signature, the command surface and the testkit entries against existing adapter idioms.
  Acceptance: declarations compile in `adapter/postgres` and `testkit` without core changes; `task api:check` clean. (2026-11-15: root module diffed against `v0.3.0` with no break, `adapter/otel` compatible with `adapter/otel/v0.1.0`, untagged modules including `adapter/postgres` skipped.)

## 2. Behavior

- [x] 2.1 Implement `Migrate` target-version support and the `gohan-postgres migrate` command over the same entry point.
  Acceptance: `stores.pg-migrate-command` passes.
- [x] 2.2 Implement `Maintain`: rolling partition creation, `DROP PARTITION` retention and idempotent concurrent execution.
  Acceptance: `stores.pg-maintain-idempotent` and `stores.pg-partitions-ahead` pass.
- [x] 2.3 Implement `SKIP LOCKED` batched reaping with coalesced heartbeats on the hot `runs` table.
  Acceptance: `stores.pg-hot-runs-reaping` passes under concurrent run workers.
- [x] 2.4 Enforce the expand/contract skew rule: contract migrations gated on reader coverage, `MinSchema` refusing older readers.
  Acceptance: `stores.pg-expand-contract-skew` and `stores.pg-schema-too-old` pass.
- [x] 2.5 Extend the static lint to fail any transaction held across a model or tool call.
  Acceptance: `stores.transaction-across-model-call` passes and the lint reports each offending span.
- [x] 2.6 Add `storetest.Migrations` (fresh head equals migrated install) and `storetest.Bloat` (1 M cycles, autovacuum on, bounded p99 and dead tuples).
  Acceptance: `stores.pg-fresh-equals-upgraded` and `stores.bloat-bound` pass.

## 3. Integration

- [x] 3.1 Run the affected store suites with process deadlines and worker bounds; retain assertion-level evidence for the bloat run.
  Acceptance: `adapter/postgres` suites green with no skipped subtest in the assigned scenarios.
- [x] 3.2 Verify `task lint`, `task spec`, `task test`, `task api:check`; update the `[Unreleased]` changelog entry.
  Acceptance: all four tasks green; changelog names the new command, `Maintain` and the testkit additions. (2026-11-15: `task lint` 0 issues across all three legs on golangci-lint v2.14.0, `task spec` ends `missing_in_scope=0 unregistered=0 failed_legs=0`, `task test` green, `task api:check` clean.)
