# 0179. Postgres schema ownership: maintain scheduling, skew gating and the transaction lint

Status: proposed

## Context

ADR-0169 recorded the migration cycle as a `stores` contract but scoped itself to the down-up-down check. ADR-0073 fixed the physical shape of the Postgres adapter: time-partitioned journal, audit and event tables with `DROP PARTITION` retention, a small hot `runs` table with coalesced heartbeats, `SKIP LOCKED` batched reaping, session timeouts on every pool, and a lint that forbids holding a transaction across a model or tool call. Neither ADR states how `Maintain` is scheduled, how expand and contract migrations skew across a rolling upgrade, or what exactly the lint rejects. M3 delivers the contract and must not reopen the settled shape.

## Decision

`Migrate(ctx, pool, MigrateOptions{Schema, TargetVersion})` stays the only migration entry point; a zero `TargetVersion` means head, and the `gohan-postgres migrate` command is a thin offline wrapper over it, so operators, CI and tests drive one code path.

`Maintain` is scheduled by the deployment, never by the library. The adapter spawns no timers or goroutines; operators call `Maintain` on a schedule, from cron or from the offline command. One `Maintain` call performs partition creation for the rolling window, retention by `DROP PARTITION`, and `SKIP LOCKED` reaping in bounded batches. It is idempotent and safe to run concurrently: duplicate partition creation is a no-op, retention and reaping use the same advisory discipline as `Migrate`.

The skew rule is deployment-facing and gated: a release ships expand migrations only; a contract migration (drop or narrowing) becomes applicable only after every reader in the fleet serves the newer shape, and the adapter refuses to apply it while `MinSchema` admits an older reader. This keeps rolling upgrades free of missing-column reads and dropped-column writes and bounds the skew window explicitly.

The lint is a static check over the caller's package graph. It rejects exactly these spans: a `pgx.Tx` (or any handle bound to one) alive across `flow.Invoke`, a model call or a tool call. It does not flag transactions held across store-only work, which ADR-0166 already governs, and it does not require transactions to be absent, only short. A long transaction pins the MVCC horizon, stalls autovacuum and turns the partitioned tables into the bloat pattern ADR-0073 forbids.

`storetest.Migrations` and `storetest.Bloat` join the stores testkit as the proofs for fresh-equals-upgraded and the bloat bound; both reuse the existing scenario wording, and the bloat run stays behind the full-test lane with a process deadline because 1 M cycles are slow.

## Consequences

The library stays free of background scheduling; a deployment that never calls `Maintain` sees missing future partitions and `ErrPartitionMissing` on out-of-window writes, which is explicit rather than silent. Contracting early fails at migration time instead of corrupting older readers. The lint may produce false positives on exotic transaction wrappers, so it reports offending spans rather than failing opaquely.

## Related records

- 0169 — the migration cycle is a `stores` contract.
- 0073 — partitioned tables, hot runs, reaping, pool timeouts, lint.
- 0125 — the adapter owns the PostgreSQL schema and its embedded migrations.
- 0117 — no core port gains a method for an adapter-specific seam.
- 0166 — claims, cursors, cascades and transaction lifetimes.
