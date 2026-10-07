# Spec Delta

## MODIFIED Requirements

### Requirement: Exactly-once tool effects

`adapter/postgres` MUST expose `CompleteTx(ctx context.Context, tx pgx.Tx, k gohan.CallKey, res gohan.ToolResult) error` on its concrete `Journal` so a caller-owned `pgx.Tx` carries a tool's own write and the journal completion to one commit; a rollback MUST leave neither. `Complete` and `CompleteTx` MUST share one guarded statement path, so a key with no live reservation is refused with `ErrJournalCompleteMissed` on either, and `CompleteTx` MUST NOT commit — the caller owns the transaction.

#### Scenario: replay returns recorded result
ID: `stores.replay-returns-recorded-result`
- WHEN a completed call is re-invoked during resume
- THEN the recorded result is returned, `ToolFinished.Replayed=true`, and the tool function is not called

#### Scenario: crash window
ID: `stores.crash-window`
- WHEN the process dies after tool execution and before `Complete`
- THEN on retry the tool is called again with the same `IdempotencyKey(ctx)` and `gohan.journal.unknown_outcome` increments

#### Scenario: same-transaction journal
ID: `stores.same-transaction-journal`
- WHEN a postgres-backed tool inserts a booking and calls `CompleteTx` in the same `pgx.Tx`, and the tx rolls back
- THEN neither the booking nor the journal entry exists

### Requirement: Postgres contract

`adapter/postgres` MUST implement every store port the core declares in its own module `github.com/victorzhuk/gohan/adapter/postgres`: `SessionLog` (with `SessionIndex`), `Checkpoints` (with `CheckpointResumer` and `ResumeReadyLister`), `Journal` (with `CompleteTx` and `ModelCompleter`), `Runs` (with `PreemptedLister`, `SuspendReasonRecorder` and `FinishReasonRecorder`), `AuditLog`, `OutputStore`, `EventLog`, `NotesStore` and `RedactionMaps`. No port gains a method in core; the adapter-specific seams are methods on the concrete adapter types (ADR-0117). Every stored shape MUST be persisted in the `stores` record envelope and decoded through the schema registry on read.

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
