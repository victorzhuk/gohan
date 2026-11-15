# Proposal: Redis stores

## Why

`ModelProfile.QuotaPool` names a shared provider account (ADR-0055), but no store implementation exists that can enforce the pool limit across processes, admit by latency class, or serve one `EventLog` to every service on the account (Q13). Without `adapter/redis` the `stores.shared-pool-across-services`, `stores.batch-yields-to-interactive` and `stores.daily-pool-budget` scenarios have no backing implementation and M3 cannot exit.

## What Changes

- Add `adapter/redis` as its own Go module implementing: the quota pool limiter (one token bucket per pool shared by every service using the same pool name), class-based admission (`Interactive` reserved share, `Agentic` between, `Batch` admitted only while pool queue-wait is under a threshold and deferred as it rises), the `pool/day` breaker store with cost-anomaly spend rate, and one shared `EventLog` (Redis streams) implementing the existing `stores` port.
- Keep every contract identical to the memory and Postgres implementations: same port methods, same refusal errors, store-clock semantics, tenant scoping, record envelopes, optional-interface fallbacks.
- Cover the implemented stores through the existing `storetest` kit and verify from isolated copies via `task adapters:verify`.

## Capabilities

### New Capabilities

None. Capability freeze holds: no new capability spec is added, the module is an implementation of the existing `stores` capability.

### Modified Capabilities

- `stores`: Redis contract requirement (limiter, admission, `pool/day`, shared EventLog) beside the existing Postgres contract requirement.

## Impact

Governing ADRs: ADR-0055 (quota pools, admission classes, `Budget` scopes, cost anomaly), ADR-0180 (module decision). Extends the `EventLog` port declared in `stores` (interface in `streams` contract block, port ownership in `stores`) rather than inventing a second event model. M3 milestone scope. No core method changes; no other capability touched.
