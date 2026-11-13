## ADDED Requirements

### Requirement: Postgres schema ownership and maintenance
`adapter/postgres` MUST own schema upkeep: `Migrate` applies embedded versions under one advisory lock to `TargetVersion`; `gohan-postgres migrate` drives it offline; `Maintain` creates time partitions, reaps dead runs via `SKIP LOCKED` with coalesced heartbeats and drops aged partitions. Deployments MUST run expand migrations before rollouts and contract migrations only after all readers serve the new shape.

#### Scenario: migrate command drives one path
ID: `stores.pg-migrate-command`
- WHEN an operator runs `gohan-postgres migrate` with a target schema and `TargetVersion` on a schema three versions behind
- THEN exactly the pending versions apply under the advisory lock and a second run reports nothing to do

#### Scenario: maintain is idempotent and concurrent-safe
ID: `stores.pg-maintain-idempotent`
- WHEN two processes call `Maintain` at the same time
- THEN partitions exist once for the rolling window, expired partitions are dropped and both calls finish without error

#### Scenario: expand before contract across versions
ID: `stores.pg-expand-contract-skew`
- WHEN a reader on version N-1 and a writer on version N share one schema during a rolling upgrade
- THEN the writer uses only expanded columns and the contract migration is refused until every reader serves version N

#### Scenario: hot runs table stays small
ID: `stores.pg-hot-runs-reaping`
- WHEN dead runs accumulate while active runs heartbeat every interval and a reaper runs under live workers
- THEN reaping takes bounded `SKIP LOCKED` batches without blocking workers, heartbeats coalesce to one row update per run and the hot table stays within its row bound
