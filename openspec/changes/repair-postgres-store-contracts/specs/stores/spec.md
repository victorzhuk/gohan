# Spec Delta

## ADDED Requirements

### Requirement: Postgres output claim ledger

`adapter/postgres.Outputs.Put` MUST record one claim per `(ref, session)`, so a ref written again by another session keeps a claim for each. A ref MUST live until its last claim drops; subject erasure and the session cascade MUST drop only their own claims and MUST delete a ref only when no claim remains. `CopySessionDependents` MUST give a fork a claim on each ref the parent owns.

#### Scenario: shared ref survives one owner's erasure
ID: `stores.pg-output-shared-ref-survives-erase`
- **WHEN** two sessions of different subjects `Put` equal bytes yielding one ref and one subject is erased
- **THEN** the ref still reads for the other subject and the erasure report counts no removal for it

#### Scenario: last claim drops the ref
ID: `stores.pg-output-last-claim-deletes-ref`
- **WHEN** the last claim on a ref drops through a session delete
- **THEN** `Get(ref)` returns `stores.ErrOutputNotFound`

### Requirement: Postgres output claim backfill

The migration MUST create session claims for surviving persisted references whose session and owner are reconstructable. When no session attribution survives, it MUST preserve every `output_record` tenant and subject as a legacy owner claim and MUST NOT invent a synthetic session. A ref MUST stay live while a session claim or a legacy owner claim exists, and a claim change MUST run under a ref-keyed advisory lock taken in sorted order.

#### Scenario: reconstructable legacy ownership is preserved
ID: `stores.pg-output-legacy-claims-preserved`
- **WHEN** an upgraded fixture holds refs whose runs still reference them through the recorded session and owner
- **THEN** each ref keeps a session claim after the migration and the subject erasure of one owner does not delete it for the other

#### Scenario: Put cannot race last-claim removal
ID: `stores.pg-output-put-erase-race`
- **WHEN** `Put` for one ref and the removal of that ref's last claim run concurrently
- **THEN** the ref survives with exactly one claim and `Get(ref)` returns the canonical bytes

### Requirement: Postgres session index pagination

The PostgreSQL session index MUST freeze complete matching membership and LastActivity-descending order for each cursor. Activity changes MUST NOT omit or duplicate surviving members. Filters MUST select membership before pagination. The default page size MUST be 25 and the maximum MUST be 200. Cursors MUST remain owner-scoped and expire after LeaseTTL.

#### Scenario: order and filters inside the query
ID: `stores.pg-sessions-order-and-filters`
- **WHEN** a subject's sessions mix archived, fork, child and shadow kinds and a message is appended to the oldest primary
- **THEN** the default page returns primaries and forks only, oldest-by-activity last, and the archived, child and shadow sessions appear only under the matching filters

#### Scenario: activity change between pages
ID: `stores.pg-sessions-cursor-stable`
- **WHEN** a session's `LastActivity` changes between two page requests on one cursor
- **THEN** that session is returned at most once and the page boundary stays bound to the cursor's snapshot

#### Scenario: snapshot stays complete past one page cap
ID: `stores.pg-sessions-snapshot-complete`
- **WHEN** more than 1600 matching sessions exist and unseen sessions change activity between pages
- **THEN** every surviving snapshot member appears exactly once in the initial order

#### Scenario: cursor expiration refuses continuation
ID: `stores.pg-sessions-cursor-expiration`
- **WHEN** the store clock reaches the cursor expiration time
- **THEN** continuation fails without restarting the listing

#### Scenario: expired cursors are pruned
ID: `stores.pg-sessions-cursor-pruned`
- **WHEN** Sessions or Purge executes after expiration
- **THEN** expired headers and items are removed

#### Scenario: cursor is bound to its owner
ID: `stores.pg-sessions-cursor-owner-scope`
- **WHEN** another owner submits the cursor
- **THEN** the adapter returns `types.ErrSessionForbidden`

#### Scenario: membership stays frozen
ID: `stores.pg-sessions-cursor-membership`
- **WHEN** a new session appears or existing filters change after materialization
- **THEN** continuation retains the initial membership

#### Scenario: invalid cursor is refused
ID: `stores.pg-sessions-cursor-invalid`
- **WHEN** a cursor is malformed, missing, or used with different filters
- **THEN** the adapter refuses it without restarting

### Requirement: Postgres journal reservation atomicity

`adapter/postgres.Journal.Reserve` and `ReserveTx` MUST make a first reservation atomic: a missing row's `SELECT ... FOR UPDATE` MUST NOT be the only guard. The adapter MUST insert with conflict handling and then read the row under `FOR UPDATE`, so concurrent first reservations of one `CallKey` yield exactly one `created=true` and the others reuse or refresh the winner's entry.

#### Scenario: concurrent first reservation
ID: `stores.pg-journal-concurrent-reserve`
- **WHEN** ten goroutines `Reserve` the same fresh `CallKey`
- **THEN** exactly one gets `created=true` and no caller receives a unique-violation error

### Requirement: Postgres operation-id uniqueness

`adapter/postgres.Runs.Start` MUST refuse a duplicate tenant-scoped `OperationID` with `stores.OperationExistsError{RunID}` naming the recorded run and MUST NOT surface a raw unique-violation when two sessions race the same operation id. The per-session advisory lock alone does not serialise this.

#### Scenario: operation id raced across sessions
ID: `stores.pg-operation-id-cross-session-race`
- **WHEN** two concurrent `Start` calls on different sessions use the same tenant `OperationID`
- **THEN** exactly one succeeds and the other gets `stores.OperationExistsError{RunID}` naming the recorded run

### Requirement: Postgres notice claim exclusivity

`adapter/postgres.Runs.Notices` MUST claim each pending notice with a guarded claim that succeeds for one dispatcher only, so two concurrent dispatchers never receive the same notice id. An unacknowledged claim MUST expire after `LeaseTTL` and return the notice to pending with its id unchanged.

#### Scenario: two dispatchers never share a notice
ID: `stores.pg-notice-claim-exclusive`
- **WHEN** two dispatchers call `Notices` at the same time
- **THEN** no notice id is returned to both, and after the claim expires the notice returns to pending with the same id

### Requirement: Postgres session cascade atomicity

`adapter/postgres.SessionLog` `Delete`, `Purge` and `EraseSessions` MUST delete dependents inside the session-delete transaction through package-local transaction methods on the concrete `Runs`, `Notes`, `Outputs` and `Checkpoints` stores. A rollback MUST preserve the session and all dependent rows, and a commit MUST remove them all.

#### Scenario: cascade commits or rolls back as one
ID: `stores.pg-cascade-in-one-transaction`
- **WHEN** a session delete removes dependent rows and the transaction rolls back
- **THEN** no dependent row is removed, and on commit the session and every dependent row are gone

### Requirement: Postgres session run discovery

`adapter/postgres.Runs` MUST implement `gohan.SessionRunFinder` through `RunForSession(ctx context.Context, sessionID string) (stores.Run, error)`. It MUST return the session's most recent run whose state is `Running` or `Resuming` and whose non-null lease expires strictly after the store clock, and MUST return `stores.ErrRunNotFound` when no row is eligible. `Suspended`, terminal, expired and null-lease runs MUST be excluded.

#### Scenario: live run found for another pod
ID: `stores.pg-run-for-session-active`
- **WHEN** a session holds a live run lease and a second pod looks it up
- **THEN** `RunForSession` returns that run and `Signal` on its id is accepted

#### Scenario: no live run
ID: `stores.pg-run-for-session-none`
- **WHEN** the only run for a session has a lease expired by the store clock
- **THEN** `RunForSession` returns `stores.ErrRunNotFound`

#### Scenario: ineligible run states are excluded
ID: `stores.pg-run-for-session-ineligible`
- **WHEN** the only run for a session is `Suspended`, terminal, or holds a null lease
- **THEN** `RunForSession` returns `stores.ErrRunNotFound`