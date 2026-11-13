# Tasks

## 1. Contracts

- [ ] 1.1 Register the proposed scenario ids in `openspec/scenarios.json` (ADR-0171 pattern: proposed until admission) and sync the approved `stores` delta. Acceptance: ids registered as proposed, no main spec edited by this task, `openspec validate m3-redis-stores --strict` green.
- [ ] 1.2 Create `adapter/redis` module (`go.mod`, package layout mirroring `adapter/postgres`: one file per store, `record.go` envelope helpers) with the only new dependency (go-redis). Acceptance: module builds, `task lint` green with depguard passing for the new module.
- [ ] 1.3 Define pool configuration surface (pool name, per-pool limit, class reserved shares, queue-wait threshold, `pool/day` window) as module options, no core change. Acceptance: zero diff outside `adapter/redis` in this task; `task spec:types` unchanged.

## 2. Pool limiter and admission

- [ ] 2.1 Implement the quota pool limiter: one atomic token bucket per `(pool, window)` in Lua using server `TIME`; combined rate across processes never exceeds the pool limit. Acceptance: `stores.shared-pool-across-services` green on two processes against one Redis; `stores.redis-limiter-store-clock` green.
- [ ] 2.2 Implement class-based admission: `Interactive` reserved share, `Agentic` between, `Batch` deferred while pool queue-wait exceeds the threshold, jittered backoff by policy, `gohan.pool.deferred{class=batch}` on deferral. Acceptance: `stores.redis-admission-class-share` and `stores.batch-yields-to-interactive` green.
- [ ] 2.3 Implement the `pool/day` breaker store and the cost-anomaly spend-rate signal. Acceptance: `stores.daily-pool-budget` and `stores.redis-pool-budget-window-reset` green; `gohan.cost.anomaly` increments when spend exceeds the rolling baseline factor.

## 3. Shared EventLog

- [ ] 3.1 Implement `EventLog` on Redis streams: per-run stream, `Seq` from stream id, `Read` after `afterSeq` without gap or duplicate, retention refusal for cursors older than retained entries, `Expire`, `EventLogCoalesce`. Acceptance: `stores.redis-eventlog-shared-reattach` green; existing `streams` EventLog contract scenarios pass with the redis implementation where the suite selects it.
- [ ] 3.2 Implement `CompactionEventAppender` keyed on publication identity (ADR-0163 semantics: identical identity returns the committed version, appends nothing). Acceptance: `stores.redis-eventlog-compaction-idempotent` green.

## 4. Conformance and integration

- [ ] 4.1 Wire every implemented store into the `storetest` suites and add a two-clock suite mirroring `stores.stale-by-store-clock` semantics on Redis server time. Acceptance: `storetest` suites pass against the redis implementations with testcontainers; `stores.redis-conformance-parity` green.
- [ ] 4.2 Add the redis module to `task adapters:verify` isolated verification and document wiring of `QuotaPool` to the redis limiter in the adapter mapping docs' scope. Acceptance: `task adapters:verify` green including redis; `openspec validate m3-redis-stores --strict`, `task lint`, `task test` green.
