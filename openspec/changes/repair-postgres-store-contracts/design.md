# Design: repair-postgres-store-contracts

## Context

`adapter/postgres` implements the core store ports (openspec/specs/stores/spec.md:641). Seven concrete defects break its own contracts, each visible in the sources:

- `Outputs.Put` inserts one `output_record` row per ref with `ON CONFLICT (ref) DO NOTHING` and stores `(tenant, subject)` on the row (adapter/postgres/outputs.go:29-47). A second subject's `Put` of equal bytes records nothing, and `Outputs.EraseSubject` deletes by `(tenant, subject)` (adapter/postgres/erasure.go:283-296), so erasing the first writer deletes bytes the second still references. No claim table exists (adapter/postgres/migrations/00001_baseline.sql:67-76).
- `SessionLog.Sessions` orders by `session_id`, applies `Archived`, `Kinds` and `Control` in Go after the `LIMIT`, and returns a bare `session_id` cursor (adapter/postgres/session.go:430-489). The normative behaviour is `LastActivity` descending, filters and page size inside the query, and a cursor that survives inter-page activity changes (openspec/specs/stores/spec.md:491-498). Session activity is not a column: `LastActivity` and `Control` live in the `SessionMeta` envelope inside `session_record.payload` (adapter/postgres/session.go:62-77).
- `reserveOnTx` reads a missing row with `SELECT ... FOR UPDATE`, which takes no lock, then inserts (adapter/postgres/journal.go:235-283).
- `Runs.Start` checks `operation_id` under a per-session advisory lock only (adapter/postgres/runs.go:156-222), so two sessions racing one `OperationID` both pass and one insert fails the `run_record_operation` unique index raw (adapter/postgres/migrations/00001_baseline.sql:53-55).
- `Runs.Notices` selects pending rows and then claims each by `seq` with no predicate, so two dispatchers claim the same notice (adapter/postgres/runs_notices.go:199-246). `noticeClaimTTL` is `stores.LeaseTTL` (adapter/postgres/runs_notices.go:16-19).
- `SessionLog.removeSession` calls `d.DeleteSessionDependents(ctx, id)` on each dependent, and the concrete dependents run on their own pool (adapter/postgres/session.go:337-376; adapter/postgres/runs.go:532-546), so dependent deletion is outside the session-delete transaction.
- No concrete `RunForSession` exists although the core finder is declared and consumed (core/conversation.go:41-46, core/conversation.go:468-479).

## Goals / Non-Goals

**Goals:** Repair the seven defects inside `adapter/postgres`, keep every store-port signature unchanged, backfill the ownership that surviving data still reconstructs, and give each repaired defect one or more proposed scenarios on the public path.

**Non-Goals:** No core or `std` change and no port method (ADR-0117). No new compaction payload or hash interface — the compaction lane owns those. No M3 partition/schema lifecycle work. No release tag. No claim can be reconstructed for a ref whose only evidence was discarded by an earlier `ON CONFLICT (ref) DO NOTHING` write; that gap is documented, not invented away. No changes to adapter scenario ownership held by other M2 lanes.

## Decisions

### D1 — Repair only the concrete adapter; ports stay frozen

Every fix lands on a concrete `adapter/postgres` type. The transaction-taking seams follow the existing `CompleteTx`/`ReserveTx` pattern: package-local methods that take `pgx.Tx`. Core keeps no `pgx` import, and no `stores` interface gains a method.

### D2 — Output claim ledger with a reconstructed legacy backfill

New table `output_claim (ref text, session_id text, tenant text, subject text, created_at timestamptz, PRIMARY KEY (ref, session_id))` with indexes on `(tenant, subject)` and `(session_id)`, plus `output_legacy_claim (ref text, tenant text, subject text, PRIMARY KEY (ref, tenant, subject))` for refs whose session attribution does not survive. No synthetic session is invented.

`Outputs.Put` keeps the `ON CONFLICT (ref) DO NOTHING` byte insert and always records a claim for `(ref, ri.SessionID, tenant, subject)`. Lifetime is claim-based: a ref is live while either a session claim or a legacy owner claim exists. `EraseSubject(tenant, subject)` deletes that subject's claims from both tables and then deletes every referenced `output_record` with no claim left. `deleteSessionDependentsTx(ctx, tx, sessionID)` deletes that session's claims and the now-unclaimed rows; `CopySessionDependents(from, to)` inserts the parent's claims for the child. `Get` stays ref-keyed because refs are content-addressed.

The backfill joins `run_record.result_ref` and `run_record.session_id` to `output_record` and `session_record` where both survive, and preserves every `output_record` tenant and subject that has no surviving session attribution as a legacy owner claim. Put, claim removal and final-byte deletion run in transactions serialized by a ref-keyed transaction advisory lock; multiple refs lock in sorted order, so `Put` cannot race last-claim deletion. `Put` commits the canonical bytes and its claim together.

Irrecoverable history is documented: `ON CONFLICT (ref) DO NOTHING` previously discarded every later writer's owner, and erased bytes cannot be reconstructed. Where neither a surviving reference nor an owner record remains, the migration asserts nothing about that ref rather than inventing ownership.

### D3 — Session-index pagination: a materialized membership snapshot

Cursor pagination cannot use a mutable tuple alone: `LastActivity` changes between pages, so a plain `(LastActivity, session_id)` keyset skips a session that advances past the boundary and a filter on `LastActivity <= T` drops one that advances before it is paged. A capped id array loses membership instead. The adapter therefore materializes the complete ordered membership in SQL.

- Tables: `session_cursor (cursor_id text PRIMARY KEY, tenant text, subject text, filters text, created_at timestamptz, expires_at timestamptz)` and `session_cursor_item (cursor_id text, ordinal bigint, session_id text, PRIMARY KEY (cursor_id, ordinal))` with `ON DELETE CASCADE` from the header.
- First page (`After == ""`): one transaction inserts the header, then `INSERT ... SELECT ... row_number() OVER (ORDER BY <activity> DESC, session_id DESC)` writes one item per matching session. Membership, filters and order are selected in SQL; the application never loads the whole id list.
- Activity and `Control` come from the `SessionMeta` envelope inside `session_record.payload` (`payload::jsonb -> 'payload' ->> 'last_activity'` and `->> 'control'`, adapter/postgres/record.go:11-20, adapter/postgres/session.go:62-77). No `last_activity` column is assumed. `Archived` uses the `session_record.archived` column, `Kinds` uses `session_record.kind`, and owner uses `owner->>'Tenant'` / `owner->>'Subject'`.
- The first page reads its own materialization after commit; later pages select the next ordinal range `WHERE cursor_id = $1 AND ordinal > $2 ORDER BY ordinal LIMIT $3`, resolving each `session_id` against the live rows so returned metadata is current while membership and order stay frozen. Deletions leave gaps; the cursor advances by consumed ordinals, not returned row count, and the scan continues past the gap.
- `Limit` defaults to 25 and is capped at 200 (adapter/postgres/session.go:22-23). A later page may use a different `Limit`; membership does not change.
- Surviving members are returned with current metadata. Deleted members are omitted and never replaced by a session that joined after materialization.
- Expiry is fixed at `created_at + stores.LeaseTTL` using the store clock and is never extended by reading a page. A malformed, missing, expired or filter-mismatched cursor is refused with an adapter error whose message begins `list sessions: invalid cursor`; a cursor owned by another principal is refused with `types.ErrSessionForbidden`. An expired cursor never silently restarts.
- Pruning uses the existing entry points: `Sessions` deletes expired headers before it processes either a first page or a continuation, and `Purge` deletes expired headers inside its existing transaction even when it removes no sessions. The item rows go with the header through the cascade. No `Maintain` method and no new port method are added.

### D4 — Journal reservation atomicity

`reserveOnTx` inserts first: `INSERT INTO journal_record (...) VALUES (...) ON CONFLICT (session_id, call_id) DO NOTHING RETURNING 1`. When a row comes back it returns the fresh `Reserved` entry with `created=true`. When it does not, the row exists; the adapter reads it `FOR UPDATE` and runs the existing live/completed/expired branch unchanged. The unique index on `(session_id, call_id)` (adapter/postgres/migrations/00001_baseline.sql:15-18) is the serialisation point, so ten concurrent first reservations produce one `created=true` and nine reuses, never a unique-violation.

### D5 — Operation-id uniqueness under cross-session concurrency

`Runs.Start` takes a transaction advisory lock for a non-empty `OperationID`, keyed on an unambiguous encoding of `(tenant, operation_id)` — a length-prefixed byte tuple hashed by `hashtextextended`, not a colon-joined string that collides across tenant boundaries. The operation lock is acquired before the existing session lock, and every `Start` path uses that order. Only then does the existing duplicate lookup run. `run_record_operation` remains the database uniqueness authority; the loser receives `stores.OperationExistsError{RunID: recorded}`. The alternative — catching SQLSTATE `23505` and re-reading inside the aborted transaction — is removed, because the failed statement has already aborted the transaction.

### D6 — Notice claim under row locks

`Runs.Notices` selects claimable rows inside the notice transaction with `SELECT ... ORDER BY seq LIMIT $2 FOR UPDATE SKIP LOCKED` and the existing `(claim IS NULL OR claim <= cutoff)` predicate, updates the claimed rows, and returns only the rows that transaction claimed. `noticeClaimTTL` stays `stores.LeaseTTL` and the cutoff uses the store clock, so an unacknowledged claim lapses and the same notice id returns to pending. The compare-and-set-by-`seq` design is removed; it and the row lock are not both kept.

### D7 — In-transaction session cascade

Each concrete dependent gains a package-local transaction entry `deleteSessionDependentsTx(ctx context.Context, tx pgx.Tx, sessionID string) error`, which shares one SQL statement per dependent with its pool entry, exactly as `Complete`/`CompleteTx` already do. The concrete stores are `Runs`, `Notes`, `Outputs` and `Checkpoints`. `SessionLog.removeSession` takes the `pgx.Tx` it already holds and always calls the transaction entry through a package-local interface; `Runs`, `Notes`, `Outputs` and `Checkpoints` all supply one, so no registered concrete store falls back to the pool. The generic fallback remains for a third-party `stores.SessionDependent` that supplies no transaction method, and that fallback stays outside the atomicity guarantee. `Delete`, `Purge` and `EraseSessions` share `removeSession`, so all three become one commit. The recursion, deletion guards and fork-survival behavior are unchanged.

### D8 — Session run discovery

`Runs.RunForSession(ctx context.Context, sessionID string) (stores.Run, error)` selects rows with `state IN (Running, Resuming)`, `lease_expires IS NOT NULL` and `lease_expires > $now` where `$now` is the store clock, ordered by `started_at DESC, run_id DESC`, returns the decoded `stores.Run`, or `stores.ErrRunNotFound` when no row matches. Suspended, terminal, expired and null-lease rows are excluded by the predicate. It satisfies the existing core `gohan.SessionRunFinder` (core/conversation.go:41-46) through a package-local structural assertion with the same signature; `adapter/postgres` does not import `core` for a compile-time assertion. It is a plain read on the pool; `Signal` remains the cross-pod handle. `Conversation.ActiveRun`, owner authorization and control discovery stay with the lifecycle lane, and PostgreSQL lookup acceptance precedes the lifecycle cross-pod acceptance that consumes it.

## Risks / Trade-offs

- [Risk] The session-index snapshot materializes one row per matching session per cursor. → Accepted: rows carry no payload, `expires_at` bounds them at `LeaseTTL`, and `Sessions`/`Purge` prune expired headers on the normal paths.
- [Risk] Filtering on JSON inside `payload` cannot use an index. → Accepted: the existing schema keeps `LastActivity` in the envelope, and correctness of the frozen order outranks an index that does not exist today.
- [Risk] Owners discarded by prior `ON CONFLICT (ref) DO NOTHING` writes are unrecoverable. → Accepted and documented in D2 and the migration: the backfill reconstructs what survives and asserts nothing about the rest.
- [Risk] An advisory lock on the operation id adds one lock per non-empty `OperationID` start. → Accepted: it replaces a raw conflict with the contract error, and the fixed acquisition order prevents deadlock against the session lock.
- [Risk] A transaction entry duplicates the pool entry's SQL. → Accepted: both share one statement helper per dependent, as `Complete`/`CompleteTx` already do.
- [Risk] A third-party dependent without a transaction entry is deleted outside the transaction. → Accepted and bounded: the atomicity requirement names the four concrete stores; the fallback is documented as outside the guarantee.

## Migration Plan

One additive goose version creates `output_claim`, `output_legacy_claim`, `session_cursor` and `session_cursor_item`, with indexes on the claim owner, the claim session and the cursor header, and an `ON DELETE CASCADE` from header to item. No existing table is altered and no column is dropped. The data step is reconstructive rather than a rewrite: it inserts session claims by joining `run_record.result_ref` and `session_id` to `output_record` and `session_record`, and inserts one legacy owner claim per surviving `output_record` tenant and subject that has no session attribution. The migration carries a real `-- +goose Down` that drops the four tables, and `storetest.Migrations` compares a fresh head install with one migrated from the previous version, including legacy-data behavior, so the new version is applicable and reversible. A service keeps the ports unchanged, rebuilds with the repaired adapter and runs `postgres.Migrate` at startup; no release tag is cut and no deployment-pipeline change is introduced.