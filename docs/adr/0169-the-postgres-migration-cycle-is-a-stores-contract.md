# 0169. The Postgres migration cycle is a stores contract

Status: accepted

## Context

`adapter/postgres` embeds its goose migrations and `Migrate` applies them under an advisory lock. `TestMigrationCycle` (`adapter/postgres/migrate_cycle_test.go:111-185`) installs a scratch schema at head, runs every embedded down section newest first with the migration bookkeeping cleared, reapplies through `postgres.Migrate`, and compares token-normalized column and constraint definitions against a separate fresh head install.

Its subtest is named `stores.pg-migration-down-up-down` — the registered-ID shape the coverage gate matches against `openspec/scenarios.json`. The ID was never registered: `task spec:gate GATE=M2` reported it `unregistered`, and `openspec/specs/stores/spec.md` stated no migration-cycle contract, so the guarantee the test asserts had no owner. The `m2` umbrella is a `skip_specs` milestone change, and the archived `repair-postgres-store-contracts` shipped its registration list without this ID.

## Decision

The migration cycle is a `stores` contract, and a dedicated spec-carrying change without `skip_specs` records it — the posture the archived PostgreSQL repair used. The `Postgres contract` requirement gains one paragraph and one scenario rather than a new requirement: the adapter's schema and migration duties already live there, and `Migrate`, `MinSchema` and the fresh-versus-upgraded shape are its existing subject matter. The delta carries the complete requirement, prose and every surviving scenario, so the sync cannot drop a scenario the main spec has.

The scenario ID is the name the shipped subtest already uses, and the wording states only what the test observes: version zero after the down sweep, reapplication through `postgres.Migrate`, normalized column and constraint definitions equal to a fresh head install, and `schema_migrations` as the only ordinary table remaining. The contract claims no deleted-data recovery and no public down-migration API.

The registry entry names the change that owns the correction (`origin: m2-postgres-migration-cycle-contract`) and defers acceptance to `M2`, the milestone that gates it. The existing `R51` PostgreSQL scenarios keep `origin: R51` and their `M3` deferrals.

## Consequences

`task spec:gate GATE=M2` counts the ID as registered and covered once its subtest passes, and the unregistered report for it disappears. The `m2` umbrella keeps its posture: this change owns the delta, `m2` owns the milestone gate. No source, test, migration or port changes, and no store-suite coverage is claimed by the cycle test. `docs/design/types.md` is regenerated because a capability spec changed.

## Related records

- 0125 — the adapter owns the PostgreSQL schema and its embedded migrations.
- 0117 — no core port gains a method for an adapter-specific seam.
- 0166 — the PostgreSQL store-contract repair that gave the down sections real SQL.
