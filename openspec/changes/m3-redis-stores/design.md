# Design: m3-redis-stores

## Context

`adapter/postgres` is the reference implementation for adapter modules: own `go.mod` requiring the root module, one file per store port, embedded migrations, conformance tests through `storetest`, isolated verification. Redis differs in what it must be shared for: quota pools and the EventLog are cross-process coordination surfaces on one provider account, while durable run state (sessions, checkpoints, journal, runs) already has a transactional home in Postgres or the memory stores. The `stores` spec already names `adapter/redis` as the Redis EventLog implementation and as the backing store for `stores.shared-pool-across-services`.

## Goals / Non-Goals

Goals:

- One process-safe limiter per `QuotaPool`, shared by every service naming the same pool.
- Admission control by latency class: `Interactive` reserved share, `Agentic` in between, `Batch` deferred while pool queue-wait exceeds the threshold.
- `pool/day` breaker store across services, with the `gohan.cost.anomaly` spend-rate signal.
- One shared EventLog on Redis streams, honoring the existing `EventLog` port (`Append`, `Read` with `afterSeq`, `Expire`, retention-refusal rule, `EventLogCoalesce`).
- Conformance coverage through `storetest`; verification from isolated copies (`task adapters:verify`).

Non-Goals:

- No SessionLog, Checkpoints, Journal, Runs, AuditLog, OutputStore, NotesStore or RedactionMaps implementation.
- No new capability spec (capability freeze); no method added to any core port.
- No second event model: the module stores `Event` values of the existing port.

## Decisions

0. **Relocated spec detail (body compression).** The Redis contract body cites ADR-0055 and ADR-0180 for the quota surface. The limiter shares one bucket per pool across every process naming the same pool; callers pass durations, never instants. Implemented stores keep tenant scoping and record envelopes (schema-version) identical to memory and Postgres, not only methods and refusals. "No core port gains methods" applies to every port the module implements, existing or new.
1. **Module shape.** `adapter/redis` is its own module `github.com/victorzhuk/gohan/adapter/redis`, Go 1.27, requiring the root module and a Redis client library (go-redis; the only new dependency). Testcontainers covers integration suites; a miniredis-style fake is not used for contract tests because expiry and server time must be the server's.
2. **Pool limiter.** One key per `(pool, window)` using a Lua script for check-and-decrement so the bucket is atomic; sized from the profile's pool limit. All timing uses Redis server `TIME` inside the script (two-clocks contract); callers pass durations, never instants.
3. **Admission classes.** A per-pool reserved share for `Interactive` (class ratios configurable per pool, default 50/30/20 for Interactive/Agentic/Batch); `Batch` admission consults rolling pool queue-wait and defers (returns a deferred outcome the retry policy jitter-backoffs) while wait exceeds the threshold. `gohan.pool.deferred{class=batch}` increments on deferral.
4. **`pool/day` breaker.** Spend accumulator keyed `(pool, UTC day)`, incremented by `Budget` writes of scope `pool/day`; at limit, admission fails with the `pool/day` `*LimitExceededError` (Permanent) until the window resets. Spend-rate rolling baseline feeds `gohan.cost.anomaly`.
5. **Shared EventLog.** Redis streams, one stream per run, `Seq` from the stream id, retention via `MAXLEN`/`Expire`; a cursor older than retained entries is refused rather than served with a gap. `AppendCompactionEvent` (optional interface) is implemented keyed on publication identity like Postgres.
6. **Contracts parity.** Every implemented store passes the same `storetest` suites as memory and Postgres; refusals use the same sentinel errors; records persist in the schema-version envelope; tenant scoping in keys.

## Risks / Trade-offs

- Redis is not transactional across stores: by scoping the module to coordination surfaces only, no port needs cross-store atomicity.
- Clock: scripts use server `TIME`, so a skewed caller cannot extend a window; suite coverage mirrors `stores.stale-by-store-clock` semantics.
- Stream trimming can drop `Seq` the harness expects retained: retention-refusal rule tells the client instead of serving a gap; `EventLogCoalesce` keeps delta bursts within retention windows.
- Lua atomicity couples scripts to Redis versions: scripts are minimal and version-pinned in tests.

## Migration Plan

New module; no existing caller changes. Wiring is opt-in: a `Build` caller that names `QuotaPool` on a profile supplies the Redis limiter, exactly as a router or retry policy is supplied today. No data migration; the EventLog key namespace version starts at 1.

## Rollout

Land module + spec delta + conformance suites; `task adapters:verify` extended with the redis module in the same change. The three existing quota scenarios (`stores.shared-pool-across-services`, `stores.batch-yields-to-interactive`, `stores.daily-pool-budget`) gain their backing implementation; their tests move from marked-unmet to green.
