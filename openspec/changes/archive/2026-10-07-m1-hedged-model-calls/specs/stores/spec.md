# Spec Delta

## MODIFIED Requirements

### Requirement: Store contracts

`storetest.SessionLog`, `storetest.Checkpoints`, `storetest.Journal`, `storetest.Runs`, `storetest.AuditLog` SHALL pass for the core memory implementations and `adapter/postgres` (testcontainers). The journal `Entry` carries `Attempts int` and `Endpoint string` in addition to its tool fields: a model step records the attempts that started and the winner's endpoint, and both fields are zero-valued for a tool entry.

#### Scenario: append conflict
ID: `stores.append-conflict`
- WHEN two writers append with the same `expectedVersion`
- THEN exactly one succeeds; the other gets `ErrVersionConflict`

#### Scenario: concurrent consume
ID: `stores.concurrent-consume`
- WHEN 10 goroutines `Consume` the same token
- THEN exactly one succeeds; 9 get `ErrTokenConsumed`

#### Scenario: concurrent reserve
ID: `stores.concurrent-reserve`
- WHEN 10 goroutines `Reserve` the same `CallKey`
- THEN exactly one gets `created=true`

#### Scenario: no secrets stored
ID: `stores.no-secrets-stored`
- WHEN a checkpoint is stored while the caller's `Credential` holds a token
- THEN the stored `Originator` holds only `Subject`, `Tenant` and `Scopes`, and no token is persisted

#### Scenario: lease exclusivity
ID: `stores.lease-exclusivity`
- WHEN two pods call `Runs.Start` for the same session
- THEN exactly one gets a lease; the other gets `ErrRunActive`

#### Scenario: reclaim race
ID: `stores.reclaim-race`
- WHEN 10 reapers call `Reclaim` on the same stale run
- THEN exactly one succeeds
