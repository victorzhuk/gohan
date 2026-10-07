# Proposal: m1-postgres-stores

## Why

The `stores` *Postgres contract* requirement names the adapter's schema, migration, partition and retention behaviour, and the *Store contracts* requirement says every store suite SHALL pass for `adapter/postgres`, but no `adapter/postgres` module exists (openspec/specs/stores/spec.md:347-349, 635-685; openspec/changes/m1/design.md:176). The Contract already declares the one seam the adapter must add beyond the ports — `func (j *Journal) CompleteTx(ctx context.Context, tx pgx.Tx, k gohan.CallKey, res gohan.ToolResult) error` — and it is unimplemented, so a tool that writes its own row cannot join that write and its journal entry in one transaction (openspec/specs/stores/spec.md:341; `stores.same-transaction-journal`). The M1 matrix records the gap as the obligation "adapter/postgres, all ports incl. `AuditLog`, plus `CompleteTx`" (openspec/changes/m1/design.md:213). `stores.same-transaction-journal` is the one registered scenario this lane owns.

## What Changes

- Add the `adapter/postgres` module: `SessionLog` (with `SessionIndex`), `Checkpoints` (with `CheckpointResumer` and `ResumeReadyLister`), `Journal` (with `CompleteTx` and `ModelCompleter`), `Runs` (with `PreemptedLister`, `SuspendReasonRecorder` and `FinishReasonRecorder`), `AuditLog`, `OutputStore`, `EventLog`, `NotesStore` and `RedactionMaps`, in `github.com/victorzhuk/gohan/adapter/postgres` (openspec/specs/stores/spec.md:347; docs/design/architecture.md §4.2a, §5).
- Implement `Journal.CompleteTx` so a caller-owned `pgx.Tx` carries a tool's own write and its journal completion to the same commit, and a rollback leaves neither.
- Own the Postgres schema: tables in the `gohan` schema, embedded goose migrations applied under an advisory lock, `sqlc` as the only SQL, `WithSchema`, `MinSchema`, the schema-ahead metric, and the production refusal of `WithAutoMigrate`.
- Persist every stored shape in the `stores` record envelope so reads apply the schema registry (openspec/specs/lifecycle/spec.md:38-46), and implement `AuditLog` with the per-session hash chain and the tenant-keyed `RedactionMaps` the erasure path deletes (openspec/specs/redaction/spec.md:95-97).
- Drive the store conformance suites through testcontainers behind a build tag, add the `task test:full` path that runs them, and register `adapter/postgres` in the `spec:coverage`/`spec:gate`/`test` module lists so its scenario subtests are observed by the gate.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `stores`: the *Postgres contract* requirement's scenarios already fix the adapter's schema, migration, partition and retention behaviour, so the delta adds only the requirement-level obligation that the adapter implements every core port in its own module; the *Exactly-once tool effects* requirement gains the `CompleteTx` seam its scenario already exercises (openspec/specs/stores/spec.md:341, 403-418, 635-685).

## Impact

The change adds one adapter module and no core or `std` code: `adapter/postgres` carries `pgx`, `sqlc` output and goose, its own `go.mod`, `go.work` membership and its own tests. It adds the `task test:full` Taskfile target, registers the module in the `spec:coverage`/`spec:gate`/`test` module lists, and adds one store-suite binding. It edits no core port and adds no transport; root stays pre-v1 through M2 and no release tag is cut (openspec/changes/m1/design.md:27).
