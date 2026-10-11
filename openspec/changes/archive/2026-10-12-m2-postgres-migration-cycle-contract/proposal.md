# Proposal: m2-postgres-migration-cycle-contract

## Why

`adapter/postgres` ships embedded migrations whose down sections are exercised by `TestMigrationCycle` (`adapter/postgres/migrate_cycle_test.go:113`), which runs the registered subtest `stores.pg-migration-down-up-down`. That ID is not in `openspec/scenarios.json`, so `task spec:gate GATE=M2` reports it as `unregistered`, and no requirement states the migration-cycle guarantee the test asserts. The behaviour exists and is tested; the contract that binds it does not.

## What Changes

- Add one paragraph to the `stores` capability's `Postgres contract` requirement: the embedded down sections MUST support a down-up-down schema-shape check on an empty scratch schema installed at head, reapplication through `postgres.Migrate` MUST reproduce the fresh head install's normalized column and constraint definitions, and after the final down sweep the only ordinary table MUST be `schema_migrations`.
- Add the scenario `stores.pg-migration-down-up-down` to that requirement, between `stores.pg-fresh-equals-upgraded` and `stores.pg-auto-migrate-refused-in-prod`.
- Register `stores.pg-migration-down-up-down` in `openspec/scenarios.json` with `capability: stores`, `origin: m2-postgres-migration-cycle-contract` and `deferred_to: M2`, beside the other `stores.pg-*` entries.
- Sync the `stores` delta into `openspec/specs/stores/spec.md` in the same change, record the decision in an ADR, and regenerate `docs/design/types.md`.

No source or test edit. The shipped subtest, its name and its assertions stay unchanged; no production code, Taskfile, CI or release change.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `stores`: the `Postgres contract` requirement gains one paragraph and one scenario for the migration down-up-down cycle. Its existing prose, its ten existing scenarios and every other requirement of the capability are unchanged, and existing `stores.pg-*` deferrals keep their current values.

## Impact

`openspec/changes/m2-postgres-migration-cycle-contract/` (proposal, design, tasks, `specs/stores/spec.md`), `openspec/scenarios.json` (one added member), `openspec/specs/stores/spec.md` (one paragraph and one scenario), `docs/adr/` (one record) and `docs/design/types.md` (regenerated). The migration cycle itself is already implemented in `adapter/postgres`; this change records its acceptance. No port, migration file or test changes, and no tag is cut.
