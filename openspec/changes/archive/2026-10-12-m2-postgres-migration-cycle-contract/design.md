# Design: m2-postgres-migration-cycle-contract

## Context

`adapter/postgres` embeds its migrations and owns the PostgreSQL schema (ADR-0125). `TestMigrationCycle` (`adapter/postgres/migrate_cycle_test.go:111-185`) installs an empty scratch schema at head, runs every embedded down section newest first with the migration bookkeeping cleared, reapplies through `postgres.Migrate`, and compares normalized column and constraint definitions against a separate fresh head install. Its subtest is named `stores.pg-migration-down-up-down` — a registered-ID shape the coverage gate binds to `openspec/scenarios.json` (`tools/spec_coverage.py:113-119`).

That ID was never registered. The gate reports it `unregistered` (`tools/spec_coverage.py:148-152`), and `openspec/specs/stores/spec.md:752-789` states no migration-cycle contract, so the test's guarantee is unowned. `m2` is the milestone that must accept it and is a `skip_specs` umbrella; the capability delta therefore belongs to a dedicated micro-change.

## Goals / Non-Goals

**Goals:** register the existing scenario ID, bind it to the `stores` capability through a `Postgres contract` delta, sync that delta into the main spec, and reach a green `GATE=M2` coverage summary for this ID.

**Non-Goals:** no source, test or migration edit; no rename of `TestMigrationCycle` or its subtest; no change to `m2` or any other change's posture; no rewrite of any existing scenario or registry deferral; no claim of data restoration, complete PostgreSQL-object residue removal or store-suite coverage; no new port method.

## Decisions

### D1 — A dedicated spec-carrying micro-change owns the delta

`m2`, `m3`, `m4`, `m2-e2b-sandbox`, `m2-api-freeze` and `m2-s4-acceptance` all declare `skip_specs` and carry no delta; the only active spec-carrying change is `m2-sandbox-lifecycle-resume`, which owns workspace metadata and owner fencing, not general DDL reversal. The archived `repair-postgres-store-contracts` already shipped its own registration list without this ID. A new `spec-driven` change without `skip_specs` is the shape the archived PostgreSQL repair used (`openspec/changes/archive/2026-10-11-repair-postgres-store-contracts/.openspec.yaml`), so the migration-cycle contract gets the same posture while `m2` stays the milestone gate owner.

### D2 — `MODIFIED`, not `ADDED`: the contract is appended to `Postgres contract`

The paragraph belongs to the existing `Postgres contract` requirement, whose subject is the adapter's schema and migration duties; a new requirement would fragment a contract that already covers `Migrate`, `MinSchema` and fresh-versus-upgraded shape. The delta therefore carries the complete requirement — its prose and every surviving scenario — with the paragraph appended before the scenarios and the new scenario inserted after `stores.pg-fresh-equals-upgraded`. `openspec validate` and archive both reject a `MODIFIED` block that drops a scenario the main spec still has, so the delta reproduces the other ten scenarios verbatim.

### D3 — The registry entry names the change, not the milestone

`origin: m2-postgres-migration-cycle-contract` with `deferred_to: M2` follows the dedicated-change entries added by `repair-postgres-store-contracts` (`openspec/scenarios.json:2519-2531`). Existing `stores.pg-schema-owned` and its siblings keep `origin: R51` and `deferred_to: M3`; only the corrected ID changes owner.

### D4 — Wording stays stable

The scenario keeps the exact ID the shipped subtest uses, and its GIVEN/WHEN/THEN lines state only what the test observes: version zero after the sweep, reapplication through `postgres.Migrate`, normalized column and constraint definitions matching a fresh head install, and `schema_migrations` as the only ordinary table left. The prose states the same limits — no deleted-data recovery and no public down-migration API — so the contract never widens past the evidence.

## Risks / Trade-offs

- [Risk] The `MODIFIED` delta and the synced main spec drift apart → the sync is one edit in this change: the same paragraph and scenario are applied to `openspec/specs/stores/spec.md` under the requirement that already carries the other nine scenarios, and `openspec validate --specs` checks the result.
- [Risk] A gate run without the full build tag would report the ID missing → the gate recipe runs `adapter/postgres` with `-tags full` (`Taskfile.yml:41`); `task spec:coverage` alone cannot prove this scenario and is not used as evidence.
- [Risk] Unrelated M2 scenarios stay missing → accepted and reported separately; this change establishes only its own ID.
- [Risk] A concurrent change picks the same ADR number → the number is taken from the current `docs/adr/` head at write time and re-checked immediately before writing.

## Migration Plan

No data or code migration. The `stores` delta lands with the change, the main spec is synced in the same change, the registry gains one member, and the ADR plus the regenerated types index are written. The change stays active until its tasks are ticked; the driver commits. Nothing here runs on a service.
