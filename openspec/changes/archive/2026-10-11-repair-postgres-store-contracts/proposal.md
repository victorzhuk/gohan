# Proposal: repair-postgres-store-contracts

## Why

The `stores` *Postgres contract* requirement says `adapter/postgres` implements every core store port (openspec/specs/stores/spec.md:641), and `stores` already registers the concurrency and ordering behaviour its suites must show (`stores.concurrent-reserve`, `stores.sessions-order-last-activity`). Seven concrete defects break those contracts: the output store records one owner per content-addressed ref, so a second subject's `Put` of equal bytes claims nothing and the first subject's erasure deletes live bytes for both; `Sessions` orders by `session_id`, applies `Archived`, `Kinds` and `Control` after the `LIMIT`, and paginates on a mutable tuple cursor whose bounded id snapshot truncates complete listings; `Journal.Reserve` trusts a `SELECT ... FOR UPDATE` on a row that does not exist yet, so a first reservation race can reach the insert twice; `Runs.Start` serialises `OperationID` only per session, so a cross-session race surfaces a raw unique-violation; `Runs.Notices` claims pending rows without a guarded predicate, so two dispatchers receive the same notice id; dependent deletion runs on each dependent's own pool instead of the session-delete transaction; and no concrete `RunForSession` exists although the core finder is declared and consumed (core/conversation.go:44-45, core/conversation.go:468-479).

## What Changes

- Add an `output_claim` table and a legacy owner claim table, and make `adapter/postgres.Outputs` record one claim per `(ref, session)`. A ref lives until its last claim drops; `EraseSubject` and the session cascade drop only the caller's claims and delete a ref only when unclaimed; `CopySessionDependents` gives a fork the parent's claims. The migration backfills reconstructable ownership from surviving `run_record.result_ref` and `output_record` scope and documents the owners that prior `ON CONFLICT` writes discarded. `output_record` keeps the canonical bytes and its first-writer scope.
- Make `adapter/postgres.SessionLog.Sessions` select the complete matching membership in SQL, freeze it in `session_cursor` and `session_cursor_item` rows ordered by `LastActivity` descending then `session_id` descending, and page through ordinal ranges. No listing is truncated by a total-session cap; expired cursors are pruned by the existing `Sessions` and `Purge` paths.
- Make `adapter/postgres.Journal.Reserve` and `ReserveTx` insert-or-reuse atomically, so ten concurrent first reservations of one `CallKey` yield exactly one `created=true` and no raw unique-violation.
- Make `adapter/postgres.Runs.Start` serialise a non-empty tenant-scoped `OperationID` under a transaction advisory lock taken before the session lock, and translate the `run_record_operation` conflict into `stores.OperationExistsError{RunID}`.
- Make `adapter/postgres.Runs.Notices` claim pending notices with `SELECT ... FOR UPDATE SKIP LOCKED` inside the notice transaction, so two dispatchers never receive the same notice id.
- Move dependent deletion inside the session-delete transaction: the concrete `Runs`, `Notes`, `Outputs` and `Checkpoints` dependents share package-local SQL between a pool entry and a `deleteSessionDependentsTx(ctx, tx, sessionID)` transaction entry, and the cascade always uses the supplied transaction.
- Add `adapter/postgres.Runs.RunForSession(ctx context.Context, sessionID string) (stores.Run, error)`, satisfying the existing core `gohan.SessionRunFinder`, returning the session's live run or `stores.ErrRunNotFound`, so a pod can reach another pod's run.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `stores`: the repair adds focused requirements for the Postgres adapter's output claim ledger and backfill, session-index pagination, journal reservation atomicity, operation-id uniqueness, notice claim exclusivity, in-transaction session cascade and session-run discovery. The `Postgres contract`, `Session index`, `Notice outbox` and `Store contracts` requirements stay as written; no existing scenario is reworded and no core port gains a method.

## Impact

Changes stay inside the `adapter/postgres` module: one additive goose migration creating `output_claim`, a legacy owner claim table, `session_cursor` and `session_cursor_item`, plus a data step that inserts reconstructable claims; no column is dropped and no existing table is altered. There is no core or `std` edit and no port change. The new scenario IDs are proposed and registered in `openspec/scenarios.json` at apply time; the `stores` main spec is synced from this delta at apply time, and the Contract listing and types index are regenerated then. No Taskfile, CI or release change; no tag is cut.