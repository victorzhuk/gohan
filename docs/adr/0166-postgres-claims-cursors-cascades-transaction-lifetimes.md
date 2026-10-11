# 0166. Postgres claims, cursors, cascades and transaction lifetimes

Status: accepted

## Context

The `adapter/postgres` implementation of six store contracts was specified by
prose the adapter did not meet. An output ref was keyed by `ref` alone and a
second writer's owner was discarded by `ON CONFLICT (ref) DO NOTHING`, so a
subject erasure could delete bytes another session still addressed. Session
pagination used a mutable `(LastActivity, session_id)` keyset, so a session that
advanced between pages was skipped or duplicated. A journal reservation guarded
a missing row with `SELECT ... FOR UPDATE` only, a duplicate tenant operation id
surfaced as a raw unique violation, and a notice claim compared and set by `seq`
without a row lock. Session deletion ran dependent deletes outside its own
transaction. The port interfaces were correct in all six cases; the guarantees
lived only in the design document.

## Decision

Every fix lands on a concrete `adapter/postgres` type. The transaction-taking
seams follow the existing `CompleteTx`/`ReserveTx` pattern: package-local
methods that take `pgx.Tx`. No `stores` interface gains a method, `core` keeps no
`pgx` import, and no new port is declared.

Output lifetime is claim-based. Two tables — `output_claim (ref, session_id,
tenant, subject)` and `output_legacy_claim (ref, tenant, subject)` — carry
ownership; `Put` keeps its `ON CONFLICT (ref) DO NOTHING` byte insert and always
records a claim for `(ref, session_id, tenant, subject)`. A ref is live while a
session claim or a legacy owner claim exists, so `EraseSubject` drops only that
subject's claims and deletes a record only when none remains.
`CopySessionDependents(from, to)` gives the fork a claim on every ref the parent
owns. `Get` stays ref-keyed because refs are content-addressed.

The backfill is reconstructive, not a rewrite. It joins `run_record.result_ref`
and `run_record.session_id` to `output_record` and `session_record` where both
survive, and preserves every `output_record` tenant and subject with no surviving
session attribution as one legacy owner claim. It never invents a synthetic
session.

Irrecoverable history is documented, not papered over. `ON CONFLICT (ref) DO
NOTHING` discarded every later writer's owner before the migration, and erased
bytes cannot be reconstructed. Where neither a surviving reference nor an owner
record remains, the migration asserts nothing about that ref rather than
manufacturing ownership.

`Put`, claim removal and final-byte deletion serialize on a ref-keyed transaction
advisory lock. A multi-ref operation takes its locks in sorted order, so `Put`
cannot race last-claim deletion and two transactions cannot deadlock against each
other. `Put` commits the canonical bytes and its claim together.

Cursor pagination materializes membership in SQL rather than deriving it from a
mutable tuple. `session_cursor` carries the header and `session_cursor_item`
carries one row per member with `ON DELETE CASCADE` from the header. The first
page inserts the header, then `INSERT ... SELECT ... row_number() OVER (ORDER BY
<activity> DESC, session_id DESC)` writes the complete ordered membership;
filters select membership before pagination, and the application never loads the
whole id list. `LastActivity` and `Control` come from the `SessionMeta` envelope
inside `session_record.payload`, `Archived` and `Kinds` from their own columns, and
the owner from the `owner` JSON — no `last_activity` column is assumed.

The first page reads its own materialization after commit. A continuation selects
the next ordinal range and resolves each `session_id` against the live rows, so
metadata is current while membership and order stay frozen. Deletions leave gaps;
the cursor advances by consumed ordinals, not returned row count, and the scan
continues past the gap. `Limit` defaults to 25 and is capped at 200; a later page
may use a different limit without changing membership.

Cursor expiry is `created_at + stores.LeaseTTL` on the store clock and is never
extended by reading a page. A malformed, missing, expired or filter-mismatched
cursor is refused with an adapter error beginning `list sessions: invalid cursor`;
a cursor owned by another principal is refused with
`types.ErrSessionForbidden`. An expired cursor never silently restarts. Pruning
uses the existing entry points: `Sessions` deletes expired headers before
processing a first page or a continuation, and `Purge` deletes them inside its
existing transaction even when it removes no sessions. The item rows go with the
header through the cascade. No `Maintain` method is added.

Four related concrete guarantees follow the same rule — a guarantee the adapter
can make lands in the adapter:

- Journal reservation inserts first with `ON CONFLICT (session_id, call_id) DO
  NOTHING RETURNING 1`. A returned row is a fresh `Reserved` entry with
  `created=true`; otherwise the row exists and is read `FOR UPDATE` through the
  existing live/completed/expired branch. The unique index is the serialization
  point, so ten concurrent first reservations yield one `created=true` and nine
  reuses, never a unique violation.
- `Runs.Start` takes a transaction advisory lock for a non-empty `OperationID`,
  keyed on a length-prefixed `(tenant, operation_id)` byte tuple hashed by
  `hashtextextended` — not a colon-joined string, which collides across tenant
  boundaries. The operation lock is acquired before the session lock on every
  `Start` path. Only then does the duplicate lookup run; the loser receives
  `stores.OperationExistsError{RunID: recorded}`. Catching SQLSTATE `23505` and
  re-reading inside the aborted transaction is rejected.
- `Runs.Notices` claims with `SELECT ... ORDER BY seq LIMIT $2 FOR UPDATE SKIP
  LOCKED` under the existing `(claim IS NULL OR claim <= cutoff)` predicate and
  returns only the rows that transaction claimed. `noticeClaimTTL` stays
  `stores.LeaseTTL` and the cutoff uses the store clock, so an unacknowledged
  claim lapses and the same notice id returns to pending. The
  compare-and-set-by-`seq` design is removed; it and the row lock are not both
  kept.
- Each concrete dependent gains `deleteSessionDependentsTx(ctx, tx, sessionID)`,
  sharing one SQL statement per dependent with its pool entry as
  `Complete`/`CompleteTx` already do. `SessionLog.removeSession` takes the
  `pgx.Tx` it already holds and always calls the transaction entry through a
  package-local interface, so `Delete`, `Purge` and `EraseSessions` become one
  commit and a rollback preserves the session and every dependent row.

A third-party `stores.SessionDependent` that supplies no transaction method still
falls back to the pool, and that fallback is explicitly outside the atomicity
guarantee. The guarantee names the four concrete stores: `Runs`, `Notes`,
`Outputs` and `Checkpoints`.

`Runs.RunForSession(ctx, sessionID)` selects rows with `state IN (Running,
Resuming)`, a non-null lease and `lease_expires > $now` on the store clock, orders
by `started_at DESC, run_id DESC`, and returns `stores.ErrRunNotFound` when no row
is eligible. Suspended, terminal, expired and null-lease runs are excluded by the
predicate. It satisfies `gohan.SessionRunFinder` through a package-local
structural assertion with the same signature, so `adapter/postgres` imports no
`core` for a compile-time check. It is a plain pool read; `Signal` remains the
cross-pod handle.

The migration is additive. One goose version creates `output_claim`,
`output_legacy_claim`, `session_cursor` and `session_cursor_item` with their
indexes and the header-to-item cascade; no existing table is altered and no column
is dropped. It carries a real `-- +goose Down` that drops the four tables, and
`storetest.Migrations` compares a fresh head install against one migrated from the
previous version, including legacy-data behavior. A service keeps the ports
unchanged, rebuilds with the repaired adapter and runs `postgres.Migrate` at
startup. No release tag is cut.

This record adds no capability and no identifier the delta did not already
introduce. It fixes where each guarantee lives and what the adapter owes.