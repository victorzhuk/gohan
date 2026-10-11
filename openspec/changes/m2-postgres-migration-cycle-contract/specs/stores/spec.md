# Spec Delta

## Context

This delta MODIFIES one requirement of the `stores` capability, `Postgres contract` (`openspec/specs/stores/spec.md:752-789`). The requirement's prose and all ten of its existing scenario blocks are reproduced verbatim; the one new paragraph lands in the prose before the scenarios and the one new scenario `stores.pg-migration-down-up-down` sits between `stores.pg-fresh-equals-upgraded` and `stores.pg-auto-migrate-refused-in-prod`. The registered ID is the name the shipped subtest already uses (`adapter/postgres/migrate_cycle_test.go:113`). No other requirement of `stores` or of any other capability is touched, and no existing scenario is reworded.

## MODIFIED Requirements

### Requirement: Postgres contract

`adapter/postgres` MUST implement every store port the core declares in its own module `github.com/victorzhuk/gohan/adapter/postgres`: `SessionLog` (with `SessionIndex` and `SessionWorkspaceMeta`), `Checkpoints` (with `CheckpointResumer` and `ResumeReadyLister`), `Journal` (with `CompleteTx` and `ModelCompleter`), `Runs` (with `PreemptedLister`, `SuspendReasonRecorder` and `FinishReasonRecorder`), `AuditLog`, `OutputStore`, `EventLog`, `NotesStore` and `RedactionMaps`. No port gains a method in core; the adapter-specific seams are methods on the concrete adapter types (ADR-0117). `SessionWorkspaceMeta` MUST run one transaction that takes the session serialization lock, locks `session_record` and then the owning `run_record` last, applies the `stores` refusal precedence, writes `expectedGeneration+1` and commits without releasing the owner row lock; any operation that needs both rows uses the session-before-run order, and a run-only mutation keeps run-only locking. Every stored shape MUST be persisted in the `stores` record envelope and decoded through the schema registry on read.

On an empty scratch schema installed at head, the embedded migration down sections MUST support a down-up-down schema-shape check. The check MUST execute down sections newest first and clear migration bookkeeping before each reapplication. Reapplication through `postgres.Migrate` MUST apply every embedded version and reproduce the fresh head install's normalized column and constraint definitions. After the final down sweep, the only ordinary table MUST be `schema_migrations`. This check does not guarantee recovery of deleted data or require a public down-migration API.

#### Scenario: schema owned by the adapter
ID: `stores.pg-schema-owned`
- WHEN two stacks are built with `WithSchema("a")` and `WithSchema("b")` on one database
- THEN each sees only its own sessions and runs and neither migration touches the other schema

#### Scenario: migrate serialised
ID: `stores.pg-migrate-serialised`
- WHEN five pods call `Migrate` at the same time
- THEN one applies each pending version, the others wait on the advisory lock and finish with nothing to do

#### Scenario: schema too old
ID: `stores.pg-schema-too-old`
- WHEN `postgres.New` runs against a schema below its `MinSchema`
- THEN it fails with `ErrSchemaTooOld` before any query

#### Scenario: schema ahead warns
ID: `stores.pg-schema-ahead-warns`
- WHEN the schema is one version newer than the code knows
- THEN `Build` succeeds, `gohan.postgres.schema_ahead` is 1 and every store suite still passes

#### Scenario: partitions created ahead
ID: `stores.pg-partitions-ahead`
- WHEN `Maintain` runs
- THEN partitions exist for the next 14 days and a write dated inside them succeeds; a write beyond them fails with `ErrPartitionMissing`

#### Scenario: fresh equals upgraded
ID: `stores.pg-fresh-equals-upgraded`
- WHEN `storetest.Migrations` compares a fresh head install with one migrated from version 1
- THEN the schema dumps are identical apart from partition names and the suites pass on both

#### Scenario: migration down-up-down
ID: `stores.pg-migration-down-up-down`
- GIVEN an empty scratch schema installed at head and a separate fresh head install for comparison
- WHEN every embedded down section runs newest first and migration bookkeeping is cleared
- THEN the recorded migration version is zero
- WHEN `postgres.Migrate` reapplies the migrations
- THEN every embedded version is applied and the normalized column and constraint definitions match the fresh head install
- WHEN every down section runs newest first again and migration bookkeeping is cleared
- THEN the only ordinary table remaining is `schema_migrations`

#### Scenario: auto-migrate refused in production
ID: `stores.pg-auto-migrate-refused-in-prod`
- WHEN a stack with `WithAutoMigrate()` is built with `CostTags.Environment: "production"`
- THEN `Build` fails naming the option

#### Scenario: retention by partition
ID: `stores.retention-by-partition`
- WHEN journal retention runs for a day older than TTL
- THEN the adapter drops the partition and no row-level `DELETE` is issued

#### Scenario: transaction across model call
ID: `stores.transaction-across-model-call`
- WHEN example code holds a `pgx.Tx` across `flow.Invoke`
- THEN the lint fails the build

#### Scenario: bloat bound
ID: `stores.bloat-bound`
- WHEN `storetest.Bloat` runs 1 M reserve/complete cycles
- THEN p99 `Reserve` stays under the bound and dead tuples stay bounded
