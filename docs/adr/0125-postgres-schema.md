# ADR-0125: Postgres schema is library-owned, migrations embedded, expand/contract skew rule

Status: accepted · Origin: grill round 51 (2026-09-30)

## Decision

`adapter/postgres` owns schema `gohan` (configurable per stack) and never lets users alter its tables. Migrations are embedded goose files applied by `postgres.Migrate` under an advisory lock or by the `gohan-postgres migrate` command, meant for the deploy pipeline; auto-migrate is development-only and refused in production. Each release must run against the previous and next schema (expand/contract); `postgres.New` refuses schemas older than `MinSchema` and `Build` warns when the schema is ahead. Partitions are created 14 days ahead by `Migrate` and `Maintain`; a missing partition fails the write. `storetest.Migrations` proves a fresh install equals an upgraded one.

## Context and evidence

Libraries that own tables (River and the goose ecosystem) ship embedded migrations with both a Go API and a CLI, support target versions and alternate schemas for multiple installs, and separate migration from application start. gohan had record-level upcasters and partition-drop retention but no DDL ownership, no runner, no version-skew rule and nobody creating future partitions.

## Consequences

`stores` v1.6 (*Postgres schema* rule, `ErrSchemaTooOld`, `ErrPartitionMissing`, seven scenarios), `docs/design/adapters.md` pointer, M3 proposal wording; all scenarios M3.
