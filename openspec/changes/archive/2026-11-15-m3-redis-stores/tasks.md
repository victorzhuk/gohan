# Tasks

## 1. Contracts

- [x] 1.1 Register the proposed scenario ids in `openspec/scenarios.json` (ADR-0171 pattern: proposed until admission) and sync the approved `stores` delta. Acceptance: ids registered as proposed, no main spec edited by this task, `openspec validate m3-redis-stores --strict` green.
  (2026-11-15: six ids registered with `origin: m3-redis-stores` and `deferred_to: M3`; the delta stays unapplied until archive; strict validation green.)
- [x] 1.2 Create `adapter/redis` module (`go.mod`, package layout mirroring `adapter/postgres`: one file per store, `record.go` envelope helpers) with the only new dependency (go-redis). Acceptance: module builds, `task lint` green with depguard passing for the new module.
  (2026-11-15: module pins published root `v0.3.0` and go-redis `v9.23.0`; `go vet` and `gofmt` clean; `golangci-lint` is not installed on the host, so `task lint` could not run here — depguard selects only `**/core/**` and `**/std/**`, so no rule matches the adapter. Lint remains unverified on this host.)
- [x] 1.3 Define pool configuration surface (pool name, per-pool limit, class reserved shares, queue-wait threshold, `pool/day` window) as module options, no core change. Acceptance: zero diff outside `adapter/redis` in this task; `task spec:types` unchanged.
  (`PoolConfig` carries all five; `task spec:types` clean and `docs/design/types.md` unchanged.)

## 2. Pool limiter and admission

- [x] 2.1 Implement the quota pool limiter: one atomic token bucket per `(pool, window)` in Lua using server `TIME`; combined rate across processes never exceeds the pool limit. Acceptance: `stores.shared-pool-across-services` green on two processes against one Redis; `stores.redis-limiter-store-clock` green.
  (Per-class token bucket script reads `TIME` inside Lua; the shared-pool scenario runs two clients against one container and additionally reads the bucket hash from Redis and refuses through a freshly built pool; the clock scenario compares the refill horizon with the server-derived value. Full-tag run green.)
- [x] 2.2 Implement class-based admission: `Interactive` reserved share, `Agentic` between, `Batch` deferred while pool queue-wait exceeds the threshold, jittered backoff by policy, `gohan.pool.deferred{class=batch}` on deferral. Acceptance: `stores.redis-admission-class-share` and `stores.batch-yields-to-interactive` green.
  (Class caps keep each reserved share; failover sets sum to the limit; deferral returns a retry horizon and jitter stays with the retry policy; the deferred meter is asserted. Both scenarios green.)
- [x] 2.3 Implement the `pool/day` breaker store and the cost-anomaly spend-rate signal. Acceptance: `stores.daily-pool-budget` and `stores.redis-pool-budget-window-reset` green; `gohan.cost.anomaly` increments when spend exceeds the rolling baseline factor.
  (Day accumulator keyed by the store clock's UTC day; anomaly compares the day against the previous seven-day mean, covered by a module subtest; both scenarios green.)

## 3. Shared EventLog

- [x] 3.1 Implement `EventLog` on Redis streams: per-run stream, `Seq` from stream id, `Read` after `afterSeq` without gap or duplicate, retention refusal for cursors older than retained entries, `Expire`, `EventLogCoalesce`. Acceptance: `stores.redis-eventlog-shared-reattach` green; existing `streams` EventLog contract scenarios pass with the redis implementation where the suite selects it.
  (Streams carry the Go-encoded envelope; the sequence lives in the id and a `seq` field; first sequence and counter are read atomically; `Expire` sweeps only runs whose oldest instant is before the horizon; the frozen `storetest.EventLog` suite is bound and green. `EventLogCoalesce` stays a core type applied before `Append`.)
- [x] 3.2 Implement `CompactionEventAppender` keyed on publication identity (ADR-0163 semantics: identical identity returns the committed version, appends nothing). Acceptance: `stores.redis-eventlog-compaction-idempotent` green.
  (Tombstone hash holds the fingerprint outside the retained window; duplicate is a no-op and appends nothing, a conflicting payload is refused. Scenario green.)

## 4. Conformance and integration

- [x] 4.1 Wire every implemented store into the `storetest` suites and add a two-clock suite mirroring `stores.stale-by-store-clock` semantics on Redis server time. Acceptance: `storetest` suites pass against the redis implementations with testcontainers; `stores.redis-conformance-parity` green.
  (`storetest.EventLog` bound; the parity scenario runs the suite and asserts envelope shape and the stale sentinel; two-clock coverage uses a wrapper client whose `TIME` is skewed, proving the store clock and the budget day key come from the client command rather than the process clock. Full-tag run green.)
- [x] 4.2 Add the redis module to `task adapters:verify` isolated verification and document wiring of `QuotaPool` to the redis limiter in the adapter mapping docs' scope. Acceptance: `task adapters:verify` green including redis; `openspec validate m3-redis-stores --strict`, `task lint`, `task test` green.
  (`adapterverify` registers the module at root `v0.3.0` with `-tags full` and three required entries; `go run ./tools/adapterverify -mod adapter/redis` PASS; §7.10 documents wiring; strict validation and the module suites green. `task lint` unverified because `golangci-lint` is absent; `task spec:gate` green with `failed_legs=0`.)
