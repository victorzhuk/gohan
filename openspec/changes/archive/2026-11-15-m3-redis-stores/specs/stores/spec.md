## ADDED Requirements

### Requirement: Redis contract

`adapter/redis` MUST provide quota pool limiting, class admission, `pool/day` breaker and `EventLog` behind `ModelProfile.QuotaPool` in module `github.com/victorzhuk/gohan/adapter/redis`. Windows, expiry, staleness MUST use Redis server time; callers MUST pass durations. Stores MUST match memory and Postgres port methods and refusals and pass `storetest`. It MUST NOT implement SessionLog/Checkpoints/Journal/Runs/AuditLog/OutputStore/NotesStore/RedactionMaps.

#### Scenario: limiter decided on store clock
ID: `stores.redis-limiter-store-clock`
- WHEN a caller whose wall clock is minutes ahead admits against the pool
- THEN the window boundaries and refill come from Redis server time and the caller cannot extend or shorten its share

#### Scenario: admission class share
ID: `stores.redis-admission-class-share`
- WHEN the pool is near saturation and `Interactive`, `Agentic` and `Batch` requests contend
- THEN `Interactive` is admitted from its reserved share first and `Batch` requests are deferred while queue-wait exceeds the threshold

#### Scenario: pool budget window reset
ID: `stores.redis-pool-budget-window-reset`
- WHEN `pool/day` spend reaches its limit and the UTC day passes on Redis server time
- THEN admission resumes without any caller action and spend restarts from zero for the new window

#### Scenario: shared eventlog reattach
ID: `stores.redis-eventlog-shared-reattach`
- WHEN a client of another service reconnects with `Last-Event-ID` against the shared Redis `EventLog`
- THEN events after that `Seq` replay with no gap and no duplicate, and a cursor older than retained entries is refused instead of served with a gap

#### Scenario: eventlog compaction idempotent
ID: `stores.redis-eventlog-compaction-idempotent`
- WHEN the same compaction publication is appended twice to the Redis `EventLog`
- THEN the first append commits and the second returns the original version appending nothing

#### Scenario: conformance parity
ID: `stores.redis-conformance-parity`
- WHEN the `storetest` suites run against the redis implementations with testcontainers
- THEN the suites pass with the same sentinel refusals and envelope shapes as the memory and Postgres implementations
