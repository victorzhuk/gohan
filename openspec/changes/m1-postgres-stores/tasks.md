# Tasks

Delivered in `feat(postgres): implement the store ports and the atomic journal seam`. A task is done when its listed scenario IDs pass and `task lint`, `task spec` and `task test` are green.

## 1. Module and schema

- [x] 1.1 Create `adapter/postgres` as its own module `github.com/victorzhuk/gohan/adapter/postgres` with `go.mod` requiring the root module, add it to `go.work`, and land the `DB`/pool type plus `postgres.New(ctx, dsn, opts...)`, `WithSchema`, `WithAutoMigrate`, `WithClock`, `WithTelemetry` and `WithPool`; verify `go -C adapter/postgres build ./...` and `go -C adapter/postgres test -short -timeout 2m ./...`.
  - evidence: `adapter/postgres/go.mod`, `go.work`, `adapter/postgres/postgres.go` (`DB`, options).
- [x] 1.2 Add the embedded goose migrations for every store table in schema `gohan` (`<store>_<record>`), `postgres.Migrate(ctx, pool, MigrateOptions{Schema, TargetVersion})` under a Postgres advisory lock, the applied-version table, the `MinSchema` check failing with `types.ErrSchemaTooOld`, and the schema-ahead metric `gohan.postgres.schema_ahead`; verify `go -C adapter/postgres test -tags full -timeout 10m -run 'TestStores'` (testcontainers) and the `stores.pg-migrate-serialised`, `stores.pg-schema-too-old` and `stores.pg-schema-ahead-warns` subtests.
  - evidence: `adapter/postgres/migrations/*.sql`, `adapter/postgres/migrate.go`.
- [x] 1.3 Add the `gohan-postgres migrate` command (JSON output when stdout is not a TTY) and the `task test:full` Taskfile target that runs the tagged store suites across the workspace; add `adapter/postgres` to the `spec:coverage`, `spec:gate` and `test` module lists in `Taskfile.yml` so the module's scenario subtests are observed by the gate; verify `task test:full` reaches `adapter/postgres`, `task test` (`-short`) skips it, and `task spec:gate GATE=M1` reads `adapter/postgres`.
  - evidence: `adapter/postgres/cmd/gohan-postgres/`, `Taskfile.yml` (`test:full`).

## 2. Store ports

- [x] 2.1 Implement `SessionLog` (with the optional `SessionIndex`) and `Checkpoints` (with `CheckpointResumer` and `ResumeReadyLister`), persisting every record in the `stores.RecordEnvelope` and decoding through `stores.DecodeRecord[T]`; verify `go -C adapter/postgres test -tags full -timeout 10m -run 'TestSessionLog|TestCheckpoints'` and the `storetest.SessionLog`/`storetest.Checkpoints` suites.
  - evidence: `adapter/postgres/session.go`, `adapter/postgres/checkpoint.go`, `adapter/postgres/storetest_bind_test.go`.
- [x] 2.2 Implement `Runs` with the optional `PreemptedLister`, `SuspendReasonRecorder` and `FinishReasonRecorder`, store-clock leases with a generation that does not repeat, and the mailbox and notice outbox in the state-change transaction; verify `go -C adapter/postgres test -tags full -timeout 10m -run 'TestRuns'` and the `storetest.Runs` suite.
  - evidence: `adapter/postgres/runs.go`, `adapter/postgres/runs_notices.go`.
- [x] 2.3 Implement `Journal` with `ModelCompleter`, `AuditLog` with the per-session hash chain, `OutputStore`, `EventLog`, `NotesStore` and `RedactionMaps` (tenant-keyed, with `Region()`); verify `go -C adapter/postgres test -tags full -timeout 10m -run 'TestJournal|TestAudit|TestOutputs|TestEvents|TestNotes|TestRedaction'` and the `storetest.Journal`/`storetest.AuditLog` suites.
  - evidence: `adapter/postgres/journal.go`, `adapter/postgres/audit.go`, `adapter/postgres/outputs.go`, `adapter/postgres/events.go`, `adapter/postgres/notes.go`, `adapter/postgres/redaction.go`.
- [x] 2.4 Implement erasure and cascade in the same module: `EraseSubject` deletes the subject's sessions, events, outputs, checkpoints, journal entries and redaction maps and returns the report with audit records kept as checksums; `DeleteSession` cascades child and shadow sessions and dependent records, fails with `types.ErrSessionHeld` first and otherwise with `types.ErrRunActive` on a live lease; verify `go -C adapter/postgres test -tags full -timeout 10m -run 'TestErase|TestDeleteCascade'`.
  - evidence: `adapter/postgres/erasure.go`, `adapter/postgres/session.go` (`DeleteSession`).
- [x] 2.5 Add `storetest.Migrations` (compares a fresh head install with one migrated from version 1) and `storetest.Bloat` (1 M reserve/complete cycles; p99 and dead-tuple bounds) to `testkit/storetest`, the two helpers the `stores.pg-fresh-equals-upgraded` and `stores.bloat-bound` scenarios name; verify `go test -short -timeout 2m ./testkit/storetest/...` and the `TestStores` subtests that drive them.
  - evidence: `testkit/storetest/migrations.go` (`Migrations`); `testkit/storetest/bloat.go` (`Bloat`).

## 3. The atomic journal seam

- [x] 3.1 Add `CompleteTx(ctx, tx pgx.Tx, k gohan.CallKey, res gohan.ToolResult) error` on `*postgres.Journal`, sharing one guarded statement path with `Complete` and never committing; add the transaction-taking model-completion variant beside it; verify `go -C adapter/postgres test -tags full -timeout 10m -run 'TestStores'` with the `stores.same-transaction-journal` subtest.
  - evidence: `adapter/postgres/journal.go` (`CompleteTx`, shared statement path).
- [x] 3.2 Add the top-level `TestStores` whose subtests are named exactly the owned scenario IDs, driving the public path against testcontainers: `stores.same-transaction-journal` begins a `pgx.Tx` through `postgres.DB.Begin`, writes a booking row, calls `CompleteTx`, rolls back, and asserts neither row exists; the committed case asserts both exist and a re-`Reserve` replays the recorded result. Also assert the store suites' scenario IDs (`stores.append-conflict`, `stores.concurrent-consume`, `stores.concurrent-reserve`, `stores.lease-exclusivity`, `stores.reclaim-race`, `stores.stale-by-store-clock`, `stores.checkpoint-expiry-store-clock`, `stores.replay-returns-recorded-result`, `stores.crash-window`, `stores.pg-schema-owned`, `stores.pg-migrate-serialised`, `stores.pg-schema-too-old`, `stores.pg-schema-ahead-warns`, `stores.pg-partitions-ahead`, `stores.pg-fresh-equals-upgraded`, `stores.pg-auto-migrate-refused-in-prod`, `stores.retention-by-partition`, `stores.transaction-across-model-call`, `stores.bloat-bound`, `stores.delete-cascades`, `stores.hash-chain`, `stores.no-secrets-stored`, `stores.journal-ttl`) pass against the adapter; verify `go -C adapter/postgres test -tags full -timeout 10m -run 'TestStores'`.
  - evidence: `adapter/postgres/scenarios_test.go` (`TestStores` with the ID-named subtests).
- [x] 3.3 Run the repository wrappers and resolve failures; verify `task lint` and `task test`, and `task test:full` for the testcontainers path.
  - evidence: driver-run `task lint` 0 issues; `task test` green across root, `adapter/otel`, `adapter/openai`, `adapter/anthropic`, `adapter/postgres` (short), `examples`; `task test:full` green.
- [x] 3.4 Validate the change after all required M1 owners land; verify `openspec validate m1-postgres-stores --strict --no-interactive` and `task spec:gate GATE=M1`.
  - evidence: `Change 'm1-postgres-stores' is valid`; `task spec:gate GATE=M1` reports `unregistered=0` and no missing ID owned by this change.
