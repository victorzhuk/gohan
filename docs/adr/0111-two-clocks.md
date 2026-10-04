# ADR-0111: Two clocks — store time for expiry, monotonic harness time for budgets, no clock port

Status: accepted · Origin: grill round 37 (2026-09-30)

## Decision

Every persisted-and-compared instant (lease expiry, staleness, checkpoint expiry, journal expiry, operation retention, catalog TTL) is computed and compared by the store from its own clock; callers pass durations. `Runs.Stale` takes `staleAfter time.Duration` instead of an instant. Budgets and timeouts use monotonic harness time only. `EventMeta.Time` and audit timestamps are informational; `Seq` orders. Core has no clock port: harness timing is tested under `testing/synctest`, store timing with `memory.WithNow` or short TTLs against real stores.

## Context and evidence

Go 1.25+ `synctest` gives a fake, deterministically advancing clock for in-process time, which removes the case for an injected `Clock` interface — but it cannot bubble a database call, and the database has its own clock anyway. Lease correctness across pods depends on one clock, and the only one every pod shares is the store's. The `Stale(olderThan time.Time)` signature let a skewed reaper reclaim healthy runs.

## Consequences

`stores` v1.1 (*Two clocks* rule, `Stale` signature, four scenarios), `limits` v1.1 (one scenario), `testing.md` seams; task 27 updated.
