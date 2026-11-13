# ADR-0180: `adapter/redis` implements the shared quota pool limiter, class-based admission and the shared EventLog as its own Go module

Status: proposed

## Decision

`adapter/redis` is a new Go module (`github.com/victorzhuk/gohan/adapter/redis`) that implements a bounded slice of the `stores` ports: the quota pool limiter and admission store that backs `ModelProfile.QuotaPool` (ADR-0055) with admission control by latency class, the `pool/day` breaker store, and one shared `EventLog` used by every service on the same provider account. It does not implement SessionLog, Checkpoints, Journal, Runs, AuditLog, OutputStore, NotesStore or RedactionMaps; services needing those combine Redis for cross-process limiting and streaming with the core memory stores or `adapter/postgres` for durable state. The module ships conformance coverage through the existing `storetest` kit and is verified from isolated copies like the other adapters (`task adapters:verify`).

## Context and evidence

Provider limits are per organization account (ADR-0055). A single process limiter cannot enforce them when several services share the account: their combined request rate, not either one's, hits the organization ceiling, and one unpaced batch job starves interactive traffic everywhere. The same account boundary makes `pool/day` a breaker that must be observable across services, and Q13's shared-`EventLog` answer needs a store that every pod on the account can read for reattach and `Last-Event-ID` replay. Redis is the shared, latency-appropriate substrate for these cross-process coordination surfaces; durable run state stays where transactional semantics already live.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one. The `stores` spec gains a Redis contract requirement beside the Postgres contract requirement; no new capability spec is added (capability freeze) and no port gains a method in core. All limiting and expiry use Redis server time, honoring the two-clocks contract; the limiter, admission and `pool/day` store are keyed by pool name and tenant, and every store the module implements passes the same `storetest` suites as the memory and Postgres implementations.
