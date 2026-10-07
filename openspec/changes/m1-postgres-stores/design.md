# Design: m1-postgres-stores

## Context

The `stores` Contract fixes the Postgres adapter's whole surface: every table lives in schema `gohan` (or `postgres.WithSchema`), as `<store>_<record>`; `sqlc` queries are the only SQL; embedded goose migrations apply under an advisory lock, from the deploy pipeline or the `gohan-postgres migrate` command, with `postgres.WithAutoMigrate()` refused when `CostTags.Environment` is `production`; the adapter adds `func (j *Journal) CompleteTx(ctx context.Context, tx pgx.Tx, k gohan.CallKey, res gohan.ToolResult) error` (openspec/specs/stores/spec.md:341, 635-685). The *Store contracts* requirement says every `storetest` suite SHALL pass for the core memory implementations and `adapter/postgres` (testcontainers), and the one registered scenario this lane owns is `stores.same-transaction-journal` (openspec/specs/stores/spec.md:347-349, 415-418).

The tree ships the ports and their memory implementations only: `SessionLog`, `Runs`, `Checkpoints`, `Journal`, `AuditLog`, `OutputStore`, `EventLog`, `NotesStore` and `RedactionMaps` (`core/stores/`). The memory implementations already persist the `stores.RecordEnvelope` and apply the schema registry on read (ADR-0157 decision 7; `core/stores/schema.go`). No adapter module exists, so the port contract has one implementation and the atomic journal scenario has no path. Root stays pre-v1 through M2 and no release tag is cut (openspec/changes/m1/design.md:27).

## Goals / Non-Goals

**Goals:** Ship `adapter/postgres` implementing every core store port in its own module (ADR-0098, ADR-0139); make `Journal.CompleteTx` join a caller's `pgx.Tx` so a tool write and its journal completion commit or roll back together; own the Postgres schema, migrations and partitions; pass the store conformance suites through testcontainers behind a build tag on a `task test:full` path.

**Non-Goals:** No core or `std` change; no port grows a method (ADR-0117 — new capability arrives as an optional interface, and `CompleteTx` is adapter-concrete, not a port). No Redis, no HTTP transport, no eino/adk-go runtime work. No data migration from another store: the adapter's first schema version is its baseline. No release tag.

## Decisions

### D1 — The transaction boundary is one caller-owned `pgx.Tx`; the tool owns it

`stores.same-transaction-journal` states the atomicity unit exactly: a postgres-backed tool inserts a booking and calls `CompleteTx` in the same `pgx.Tx`, and a rollback leaves neither. The adapter therefore does not invent a unit-of-work abstraction. It exposes a transaction handle the service begins against the same pool the stores use:

```go
func (db *DB) Begin(ctx context.Context) (pgx.Tx, error)
```

and every store constructed from `db` runs its own statements on the pool when the caller passes no transaction. `CompleteTx` runs on the transaction the caller holds. The boundary is the caller's `pgx.Tx` because only the caller knows what else must commit with the journal entry — the tool's own row, a booking, an idempotency record. Core never holds a `pgx.Tx` across `flow.Invoke`: `stores.transaction-across-model-call` fails the lint if it does, so the tool is the only place the transaction can live. The tool receives the handle through its own wiring, not through the governed call context, so the harness cannot widen the boundary.

### D2 — `CompleteTx` signature and callers

The Contract fixes the signature (openspec/specs/stores/spec.md:341). `*postgres.Journal` implements it:

```go
func (j *Journal) CompleteTx(ctx context.Context, tx pgx.Tx, k gohan.CallKey, res gohan.ToolResult) error
```

It performs the same guarded update `Complete` performs — the entry must be live, else `stores.ErrJournalCompleteMissed` (ADR-0149) — but on `tx` instead of the pool, and it is the caller's responsibility to commit. `Complete` delegates to the same statement runner with the pool; the two share one SQL path so the guards cannot diverge. Callers are user tools that need their own write in the same transaction; the harness keeps calling the port's `Complete` (`core/drive_model_journal.go`), which is transaction-free by design because the driver has no tool-owned row to join. `CompleteTx` is a method on the concrete adapter type, not on the `stores.Journal` port, so the port stays frozen and a tool that wants atomicity depends on `adapter/postgres` directly — which is the layering the architecture already requires of infrastructure code (docs/design/architecture.md §4.1).

The same seam is offered for `CompleteModel` where a model step needs it: `ModelCompleter.CompleteModel` stays the port-adjacent optional interface (ADR-0157), and the adapter adds the transaction-taking variant beside `CompleteTx` so a model step's extended entry fields and a caller write commit together.

### D3 — Migration and schema management

Migrations are goose SQL files embedded in the module and applied by `postgres.Migrate(ctx, pool, MigrateOptions{Schema, TargetVersion})` under a Postgres advisory lock, so concurrent pods serialise and each version applies once (`stores.pg-migrate-serialised`). The `gohan-postgres migrate` command is the operator entry point; `postgres.WithAutoMigrate()` is development-only and `Build` refuses it when `CostTags.Environment` is `production` (`stores.pg-auto-migrate-refused-in-prod`). `postgres.New` reads the schema's applied version and fails with `stores.ErrSchemaTooOld` before any query when it is below `MinSchema` (`stores.pg-schema-too-old`); a schema one version ahead warns through `gohan.postgres.schema_ahead` and builds (`stores.pg-schema-ahead-warns`). Every table is created inside the configured schema and no migration touches another schema (`stores.pg-schema-owned`). Time-partitioned tables (`journal_*`, `audit_*`, `event_*`) are created by `Maintain` fourteen days ahead; a write beyond the horizon fails with `stores.ErrPartitionMissing` (`stores.pg-partitions-ahead`), and journal retention drops a whole partition rather than issuing row-level `DELETE` (`stores.retention-by-partition`). A fresh head install and one migrated from version 1 produce the same schema dump apart from partition names (`stores.pg-fresh-equals-upgraded`).

### D4 — The adapter persists the record envelope; it does not re-implement schema evolution

Every stored shape the adapter writes is the `stores.RecordEnvelope{Shape, SchemaVersion, Payload}` the memory stores write, and every read decodes it through the same `stores.DecodeRecord[T]` before constructing the domain value (ADR-0157 decision 7; `core/stores/schema.go`). The adapter therefore supplies the serialized read path `m1-schema-read-paths` deferred until "an adapter provides a serialized path", and `lifecycle.upcast-on-read` and `lifecycle.historical-fixtures` reach their public proof here. The adapter adds no upcaster and rewrites no stored bytes; a record is versioned where it is stored and a read transforms a copy.

### D5 — Testcontainers behind a build tag, and the `task test:full` path

The store conformance suites live in `testkit/storetest` and are bound from the adapter's external tests, one factory per port (`storetest.Journal`, `storetest.Runs`, `storetest.SessionLog`, `storetest.Checkpoints`, `storetest.AuditLog`). Those tests carry a build tag, so `task test` (`-short`) skips them and stays offline; `task test:full` runs them and the real-provider tests. The suite starts one Postgres container per test binary through `testcontainers-go`, applies migrations with `postgres.Migrate`, and runs each factory against a fresh schema. The `stores.same-transaction-journal` subtest is named exactly its ID and drives the public path: it begins a `pgx.Tx` through `postgres.DB.Begin`, writes a booking row with the caller's own SQL, calls `CompleteTx` on the same `tx`, rolls back, and asserts neither the booking nor the journal entry exists; the committed case asserts both exist and a re-`Reserve` of the same key returns the recorded result. A recorded-fixture path is not a substitute: the scenario's atomicity is a property of the live server's transaction, so the proof runs against testcontainers. Where a suite needs store time, it drives the adapter's clock seam, mirroring `memory.WithNow` (stores *Two clocks*; docs/design/testing.md:11).

### D6 — Connection and pool configuration, and error classification

`postgres.New(ctx, dsn, opts...)` takes the DSN and functional options; `postgres.WithPool(*pgxpool.Config)` or the adapter's own options set `MaxConns`, `MinConns`, `MaxConnLifetime`, `HealthCheckPeriod` and the connect timeout, defaulting to the `pgxpool` defaults. `WithSchema`, `WithAutoMigrate`, `WithClock` and `WithTelemetry` complete the option set. A connection or query failure that is not a store-contract refusal is classified through `types.ClassifyProviderError` only where the failure is a provider-shaped one; Postgres errors are not provider errors and are surfaced as wrapped errors, so the classifier is not applied to them. The one place the table is used is the schema-ahead and connect-retry surface, where a SQLSTATE connection class maps to `types.ClassTransient` and a schema mismatch to `types.ClassPermanent` so a caller's existing retry policy handles a restarting database without special-casing. Store-contract refusals keep their sentinel identity (`ErrVersionConflict`, `ErrTokenConsumed`, `ErrTokenExpired`, `ErrRunActive`, `ErrRunNotActive`, `ErrJournalCompleteMissed`, `ErrJournalFingerprintMismatch`, `ErrSchemaTooOld`, `ErrPartitionMissing`, `ErrSessionHeld`, `ErrMailboxFull`, `ErrSignalsPending`, `ErrOperationExists`, `ErrCheckpointIncompatible`) and are never reclassified.

### D7 — `AuditLog`, erasure and the other ports in the same module

One module serves every port the core declares, so a service wires one `postgres.DB` and gets a coherent store set (openspec/changes/m1/design.md:213). `AuditLog.Append` computes the per-session hash chain and `At` on the store clock, never accepts a caller-set chain field, and `Purge` deletes per tenant; `Read` returns records in append order. Erasure is satisfied in the same module: `EraseSubject` deletes the subject's sessions, events, outputs, checkpoints, journal entries and redaction maps, and returns the report; audit records remain with checksums only (openspec/specs/redaction/spec.md:95-97). `DeleteSession` cascades the session, its child and shadow sessions and every dependent record, fails with `ErrSessionHeld` first and otherwise with `ErrRunActive` on a live lease (`stores.delete-cascades`). `RedactionMaps` is tenant-keyed with retention independent of checkpoints and journal entries, and erasure deletes it with the subject's other records. `Region()` returns the configured region so a residency-bound tenant is served or refused by `Build` (`RegionProvider`; ADR-0157). The adapter implements the optional interfaces the spec names — `SessionIndex` on `SessionLog`, `CheckpointResumer` and `ResumeReadyLister` on `Checkpoints`, `ModelCompleter` on `Journal`, `PreemptedLister`, `SuspendReasonRecorder` and `FinishReasonRecorder` on `Runs` — so the core's type-assertion growth path is real, not aspirational (ADR-0117).

### D8 — Module layout and release discipline

`adapter/postgres` is its own module `github.com/victorzhuk/gohan/adapter/postgres` with its own `go.mod`, listed in `go.work` for development only (ADR-0069; docs/design/architecture.md §5). Its only non-std dependencies are `pgx`, `sqlc`-generated code and goose; core and `std` gain nothing. It is not tagged in M1; the module's `go.mod` requires the root pseudo-version until core is tagged, and the release task rejects a `replace` directive. The `task test:full` target, the module's registration in the `spec:coverage`/`spec:gate`/`test` module lists and the module's `adapter/postgres` job are the CI surface; the store conformance matrix runs the module's suites against the root version it requires.

## Risks / Trade-offs

- [Risk] A tool holding a `pgx.Tx` across a model call would pin a connection for the whole run. → Accepted and bounded: `stores.transaction-across-model-call` is a lint-failing build error, so the boundary cannot span `flow.Invoke`; the tool commits before it returns.
- [Risk] `CompleteTx` on the concrete type couples a tool to `adapter/postgres`. → Accepted: the coupling is exactly the infrastructure-layer dependency the architecture allows (docs/design/architecture.md §4.1), and the port stays frozen for every other caller.
- [Risk] Testcontainers make the full suite slow and environment-dependent. → Accepted and contained: the tests carry a build tag, so `task test` stays offline and `task test:full` is the only path that needs Docker.
- [Risk] A migration applied by a pod racing a deploy hook could double-apply. → Accepted: the advisory lock serialises writers and the applied-version table makes each version idempotent (`stores.pg-migrate-serialised`).

## Migration Plan

No data migration: the adapter's first goose version is its baseline, and no other store's data is imported. A service moves from the memory stores by building the stack with `stores.Stores` populated from `postgres.New`; the ports are unchanged, so no caller edits. The schema evolves additively with a new goose version per change; a record written at an older version is upcast on read and never rewritten (D4). `postgres.Migrate` is run by the deploy pipeline or the `gohan-postgres migrate` command, never by `Build` outside development. No release tag is cut during M1 authoring.
