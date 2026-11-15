# Design: Postgres schema ownership

## Context

`adapter/postgres` embeds goose migrations and applies them under an advisory lock; the down-up-down cycle check is already a registered scenario (ADR-0169). Partitioning, retention, heartbeats, reaping and the lint were decided in ADR-0073 but had no delivery contract. `docs/design/testing.md` lists `storetest.Bloat` as an M3 deliverable; `docs/design/adapters.md` puts schema ownership in the adapter, never in core.

## Goals / Non-Goals

Own the full upkeep cycle: install, upgrade, downgrade target, partition maintenance, reaping and the static lint, plus the testkit proofs. Non-goals: new store ports, a public down-migration API, data recovery of deleted rows, background maintenance goroutines inside the library.

## Decisions

`Migrate(ctx, pool, MigrateOptions{Schema, TargetVersion})` applies embedded versions under one advisory lock to the requested target; zero target means head. The `gohan-postgres migrate` command is a thin wrapper over the same entry point so operators and tests drive one path.

`Maintain` is idempotent and safe under concurrency: it creates partitions for the rolling window (existing `pg-partitions-ahead` shape), drops partitions past retention with `DROP PARTITION`, and reaps dead runs in bounded batches with `SKIP LOCKED` so a stalled reaper never blocks run workers. Heartbeats are coalesced: one update per interval per run, never per event. Scheduling is deployment-owned: the library never spawns a timer; operators call `Maintain` on a schedule or run the offline command.

Expand/contract skew: a release that adds columns ships expand migrations only; contract migrations (drops, narrowing) apply only after every reader in the fleet serves the newer shape. Rolling upgrades therefore never read a missing column and never write a dropped one. The skew window is bounded by `MinSchema`, which refuses to start below the oldest supported reader.

The lint is a static check over the caller's package graph: any `pgx.Tx` whose lifetime spans a `flow.Invoke`, model call or tool call fails the build, because a long transaction pins the MVCC horizon and stalls autovacuum (ADR-0073).

`storetest.Migrations` installs one schema at head and compares it with one migrated from version 1 (existing `pg-fresh-equals-upgraded`). `storetest.Bloat` drives 1 M reserve/complete cycles through the journal with autovacuum enabled and asserts bounded p99 `Reserve` latency and bounded dead tuples (existing `stores.bloat-bound`).

## Risks / Trade-offs

Risk depth: existing-service-strict. Bloat runs are slow; the bound test stays behind the full-test lane with a process deadline. Partition windows interact with clock skew; writes beyond the window already fail `ErrPartitionMissing`, which keeps the failure explicit. Contracting too early is the main operator hazard; the lint and the skew rule keep it visible rather than silent.

## Migration Plan

No port gains a method in core; `Migrate`, `Maintain`, options and the command are adapter seams (ADR-0117). Existing schemas upgrade through the normal migration path. Rollback is the down-section sweep already covered by `stores.pg-migration-down-up-down`.
