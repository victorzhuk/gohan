# Tasks

Repair only `adapter/postgres`; every port signature stays unchanged. A task is done when its listed scenario IDs pass on the public path through the named store entry point. `-tags full` tasks need Docker and a bounded timeout. Lane unit acceptance is `timeout 3m go -C adapter/postgres test -short -timeout 2m ./...`; lane integration acceptance is `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v ./...`.

## 1. Output claims migration

- [x] 1.1 Add goose migration `adapter/postgres/migrations/00005_output_claims.sql`: create `output_claim (ref text NOT NULL, session_id text NOT NULL, tenant text NOT NULL, subject text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (ref, session_id))` with `CREATE INDEX output_claim_owner ON output_claim (tenant, subject)` and `CREATE INDEX output_claim_session ON output_claim (session_id)`; create `output_legacy_claim (ref text NOT NULL, tenant text NOT NULL, subject text NOT NULL, PRIMARY KEY (ref, tenant, subject))` with `CREATE INDEX output_legacy_claim_owner ON output_legacy_claim (tenant, subject)`; create `session_cursor (cursor_id text NOT NULL PRIMARY KEY, tenant text NOT NULL, subject text NOT NULL, filters text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL)` with `CREATE INDEX session_cursor_expires ON session_cursor (expires_at)`; create `session_cursor_item (cursor_id text NOT NULL REFERENCES session_cursor (cursor_id) ON DELETE CASCADE, ordinal bigint NOT NULL, session_id text NOT NULL, PRIMARY KEY (cursor_id, ordinal))`; write a real `-- +goose Down` dropping all four tables.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestStores'` with the `stores.pg-fresh-equals-upgraded` subtest, and a manual `up` then `down` then `up` on one scratch schema leaves no residue.
- [x] 1.2 Add the reconstructive data step to the same migration: insert session claims by joining `run_record.result_ref` and `run_record.session_id` to `output_record` and `session_record`, and insert one `output_legacy_claim` row per surviving `output_record` tenant and subject that has no surviving session attribution. Do not insert a synthetic session. Document in the migration header that owners discarded by an earlier `ON CONFLICT (ref) DO NOTHING` write, and bytes already erased, are irrecoverable.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestStores'` names `stores.pg-output-legacy-claims-preserved` and `stores.pg-fresh-equals-upgraded` against an upgraded fixture with legacy rows, and the fresh-versus-upgraded comparison covers legacy data behavior, not only schema shape.
- [x] 1.3 Confirm the migration alters no existing table and drops no column, so the previous release still runs against the new schema. Proof shape: diff the shipped migration against the baseline tree `5d3ed56dfca745c9e460bc3236be5aa5686117d5` and show that the delta adds new objects only — no `ALTER TABLE ... DROP`, no `DROP COLUMN`, no in-place type change on an existing column. Down carve-out: the `down` section (`adapter/postgres/migrations/00005_output_claims.sql:67-70`) drops only the four tables the same file creates, so the baseline diff is 87 insertions and 0 deletions. Boundary: the proof is a source diff against that baseline commit, not a runtime migration rehearsal; the baseline commit is the pre-change tree for this lane, so a clean diff there is the only claim made here.
  - verify: the `stores.pg-fresh-equals-upgraded` schema dump differs only by the four new tables, and a migrated previous-version schema accepts the previous adapter build.

## 2. Output claim ledger

- [x] 2.1 Change `Outputs.Put` (`adapter/postgres/outputs.go:29-47`) to take a ref-keyed transaction advisory lock in sorted order, insert the `output_record` row `ON CONFLICT (ref) DO NOTHING`, and insert a claim row for `(ref, ri.SessionID, ri.Principal.Tenant, ri.Principal.Subject)`. Commit the bytes and the claim together. Keep the ref derivation and `Get` behavior unchanged.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestOutputs'` names `stores.pg-output-put-erase-race` through `Outputs.Put`.
- [x] 2.2 Make `Outputs.EraseSubject(tenant, subject)` (`adapter/postgres/erasure.go:283-296`) delete that subject's `output_claim` and `output_legacy_claim` rows and then delete every referenced `output_record` with no claim left, reporting the refs actually removed. Keep the row's own `(tenant, subject)` scope as the fallback for rows written before migration 00005.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestOutputs|TestErasure'` names `stores.pg-output-shared-ref-survives-erase` and `stores.pg-output-legacy-claims-preserved` through `Outputs.EraseSubject`.
- [x] 2.3 Add `Outputs.deleteSessionDependentsTx(ctx, tx, sessionID)` and `Outputs.CopySessionDependents(ctx, from, to)` so the session cascade drops a session's claims and a fork inherits the parent's claims; register `Outputs` as a session dependent in the harness wiring.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestOutputs|TestDeleteCascade'` shows a forked session retaining a parent ref after the parent is deleted, through `SessionLog.Delete`.
- [x] 2.4 Document the claim lifetime and the legacy owner claim on the `Outputs` type and in `adapter/postgres` package docs: a ref is content-addressed, one claim per session, delete-on-last-claim, legacy owner claims preserve pre-migration ownership, and `Get` carries no authorization.
  - verify: `task lint` reports no doc-comment issue in `adapter/postgres`.
- [x] 2.5 Bind the store-suite cases for shared-ref, last-claim, legacy-claim and Put/erase race in `adapter/postgres/output_claim_test.go:20-154` (the existing `TestOutputClaimsLedger` parent at that file), not beside the `storetest` bindings. The ledger already owns these four cases as named subtests, so the binding reuses that parent rather than adding a second registration. Verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run '^TestOutputClaimsLedger$'` runs the four subtests with a nonzero match and passes.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run '^TestOutputClaimsLedger$'` names the shared-ref, last-claim, legacy-claim and Put/erase-race cases inside `adapter/postgres/output_claim_test.go:20-154`, none zero-matched.

## 3. Session index pagination

- [x] 3.1 Rewrite `SessionLog.Sessions` (`adapter/postgres/session.go:430-489`) to select the complete matching membership in SQL: owner from `owner->>'Tenant'`/`owner->>'Subject'`, `Archived` from `archived`, `Kinds` from `kind`, `LastActivity` and `Control` from the `SessionMeta` envelope inside `session_record.payload`. Order by `LastActivity` descending then `session_id` descending. Clamp `Limit` to the default 25 and the maximum 200 before selecting. Do not read a `last_activity` column; none exists (`adapter/postgres/migrations/00001_baseline.sql:19-31`).
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestSessions'` names `stores.pg-sessions-order-and-filters` through `Sessions`, and the existing `stores.sessions-listed-by-owner`, `stores.control-in-session-index` and `stores.sessions-default-excludes-forks-children` subtests still pass.
- [x] 3.2 Replace the bare-`session_id` cursor with the materialized snapshot from design D3. On the first page, insert the `session_cursor` header with `expires_at = created_at + stores.LeaseTTL` and write the complete ordered membership with one `INSERT ... SELECT ... row_number()` statement in the same transaction, then read the first ordinal range back. On later pages, select `WHERE cursor_id = $1 AND ordinal > $2 ORDER BY ordinal LIMIT $3`, resolve each id against the live row for current metadata, and return the last consumed ordinal as the next cursor so a deletion gap does not end the listing. Refuse a malformed, missing, expired or filter-mismatched cursor with an adapter error whose message begins `list sessions: invalid cursor`, and a cursor owned by another principal with `types.ErrSessionForbidden`. Never restart an expired cursor silently and impose no total-session cap.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestSessions'` names `stores.pg-sessions-snapshot-complete`, `stores.pg-sessions-cursor-expiration`, `stores.pg-sessions-cursor-owner-scope`, `stores.pg-sessions-cursor-membership`, `stores.pg-sessions-cursor-invalid` and `stores.pg-sessions-cursor-stable` through `Sessions`, and the existing `stores.sessions-order-last-activity` subtest still passes.
- [x] 3.3 Prune expired cursors through the existing entry points: `Sessions` deletes expired `session_cursor` headers before it processes a first page or a continuation, and `Purge` (`adapter/postgres/session.go:382-427`) deletes expired headers inside its existing transaction even when it removes no sessions. Items go with the header through the cascade. Add no `Maintain` method and no port method.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestSessions'` names `stores.pg-sessions-cursor-pruned` through `Sessions` and through `Purge`.
- [x] 3.4 Document the cursor contract on `adapter/postgres.SessionLog.Sessions` and in the `adapter/postgres` package docs (opaque, owner-scoped, membership-frozen, `LeaseTTL`-expiring, error prefix `list sessions: invalid cursor`), beside the existing adapter method comments. This lane edits no file under `core` or `std`, so the cursor lifetime stays an adapter behaviour and never becomes a shared contract for every `SessionIndex` implementation.
  - verify: `task lint` reports no doc-comment issue for `adapter/postgres` and `timeout 2m go -C adapter/postgres build ./...` succeeds.
- [x] 3.5 Register the seven pagination IDs in the adapter scenario test file beside the other `stores.*` subtests.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestStores'` names every ID from 3.2 and 3.3, none zero-matched.

## 4. Journal reservation atomicity

- [x] 4.1 Change `reserveOnTx` (`adapter/postgres/journal.go:235-283`) to insert-first: `INSERT ... ON CONFLICT (session_id, call_id) DO NOTHING RETURNING 1`; on a returned row return the fresh `Reserved` entry with `created=true`, otherwise read the existing row `FOR UPDATE` and run the current live/completed/expired branch unchanged.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestJournal'` names `stores.pg-journal-concurrent-reserve` through `Journal.Reserve`, and the existing `stores.concurrent-reserve`, `stores.reserve-fingerprint-mismatch`, `stores.complete-without-reservation` and `stores.same-transaction-journal` subtests still pass.
- [x] 4.2 Confirm `ReserveTx` shares the same statement path so both entry points stay atomic inside a caller transaction.
  - verify: the same test run drives both `Journal.Reserve` and `Journal.ReserveTx` races and passes.
- [x] 4.3 Document the insert-first reservation on the `Reserve` and `ReserveTx` doc comments.
  - verify: `task lint` green for `adapter/postgres`.

## 5. Runs: operation-id uniqueness and run discovery

- [x] 5.1 In `Runs.Start` (`adapter/postgres/runs.go:156-180`), take a transaction advisory lock for a non-empty `OperationID` before the existing session lock, keyed on an unambiguous length-prefixed `(tenant, operation_id)` tuple hashed by `hashtextextended`. Run the existing duplicate lookup only after the operation lock, keep `run_record_operation` (`adapter/postgres/migrations/00001_baseline.sql:53-55`) as the uniqueness authority, and return `stores.OperationExistsError{RunID: recorded}` for the loser. Remove the SQLSTATE `23505` re-read alternative; the failed insert has already aborted the transaction.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestRuns'` names `stores.pg-operation-id-cross-session-race` through `Runs.Start`, and the existing `stores.lease-exclusivity` and `stores.reclaim-race` subtests still pass.
- [x] 5.2 Add `Runs.RunForSession(ctx context.Context, sessionID string) (stores.Run, error)` selecting `state IN (Running, Resuming)`, `lease_expires IS NOT NULL` and `lease_expires > $now` with `$now` from the store clock, ordered by `started_at DESC, run_id DESC`, returning the decoded `stores.Run` or `stores.ErrRunNotFound`. Satisfy the existing core `gohan.SessionRunFinder` (`core/conversation.go:41-46`) through a package-local structural assertion with the same signature; do not import `core`, and do not add or relocate the interface. Keep `Conversation.ActiveRun` and owner authorization with the lifecycle package.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestRuns'` names `stores.pg-run-for-session-active`, `stores.pg-run-for-session-none` and `stores.pg-run-for-session-ineligible` through `RunForSession`, and the existing `stores.signal-cancel-cross-pod` subtest still passes.
- [x] 5.3 Document both behaviors on the `Runs` type and method comments, including that the lifecycle lane's `Conversation.ActiveRun` consumes `RunForSession`, and record that this lane's lookup acceptance precedes the lifecycle cross-pod acceptance.
  - verify: `task lint` green for `adapter/postgres`.
- [x] 5.4 Register the four IDs in the adapter scenario test file.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestStores'` names the four new IDs from 5.1 and 5.2, none zero-matched.

## 6. Notice claim exclusivity

- [x] 6.1 Rewrite `Runs.Notices` (`adapter/postgres/runs_notices.go:199-246`) to claim inside the notice transaction with `SELECT ... WHERE claim IS NULL OR claim <= $cutoff ORDER BY seq LIMIT $2 FOR UPDATE SKIP LOCKED`, update only the rows that transaction locked, and return only those rows. Keep `noticeClaimTTL = stores.LeaseTTL` and the store clock. Remove the compare-and-set-by-`seq` alternative.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestNotices'` names `stores.pg-notice-claim-exclusive` through `Runs.Notices`, and the existing `stores.notice-written-with-finish` subtest still passes.
- [x] 6.2 Document the row-lock claim and its expiration on `Notices` and `AckNotice`.
  - verify: `task lint` green for `adapter/postgres`.
- [x] 6.3 Register the ID in the adapter scenario test file.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestStores'` names the new ID.

## 7. In-transaction session cascade

- [x] 7.1 Add the package-local transaction entry `deleteSessionDependentsTx(ctx context.Context, tx pgx.Tx, sessionID string) error` on `Runs`, `Notes`, `Outputs` and `Checkpoints`, each sharing one statement helper with its pool entry, as `Complete`/`CompleteTx` already do (`adapter/postgres/runs.go:532-546`, `adapter/postgres/checkpoint.go:319-336`).
  - verify: `timeout 3m go -C adapter/postgres test -short -timeout 2m ./...` builds and `task lint` is green.
- [x] 7.2 Change `SessionLog.removeSession` (`adapter/postgres/session.go:337-376`) to call the transaction entry through a package-local interface so `Delete`, `Purge` and `EraseSessions` delete the session and every dependent row in one commit. Every registered concrete store supplies the entry, so none falls back to the pool; the generic fallback stays only for a third-party dependent and remains outside the atomicity guarantee. Keep the recursion, deletion guards and fork-survival behavior unchanged.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestDeleteCascade'` names `stores.pg-cascade-in-one-transaction` through `Delete`, `Purge` and `EraseSessions`, each with a rollback case asserting the session and every dependent row survive, and the existing `stores.delete-cascades`, `stores.fork-survives-parent-delete` and `stores.feedback-cascades-on-erase` subtests still pass.
- [x] 7.3 Document the transaction seam on the `SessionDependent` wiring in `adapter/postgres` docs, including the bounded atomicity guarantee.
  - verify: `task lint` green for `adapter/postgres`.
- [x] 7.4 Register the ID in the adapter scenario test file.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -run 'TestStores'` names the new ID.

## 8. Contract, spec sync and registry (apply time)

- [x] 8.1 At apply time, sync the `stores` main spec from this delta and register exactly these proposed IDs in `openspec/scenarios.json`: `stores.pg-output-shared-ref-survives-erase`, `stores.pg-output-last-claim-deletes-ref`, `stores.pg-output-legacy-claims-preserved`, `stores.pg-output-put-erase-race`, `stores.pg-sessions-order-and-filters`, `stores.pg-sessions-cursor-stable`, `stores.pg-sessions-snapshot-complete`, `stores.pg-sessions-cursor-expiration`, `stores.pg-sessions-cursor-pruned`, `stores.pg-sessions-cursor-owner-scope`, `stores.pg-sessions-cursor-membership`, `stores.pg-sessions-cursor-invalid`, `stores.pg-journal-concurrent-reserve`, `stores.pg-operation-id-cross-session-race`, `stores.pg-notice-claim-exclusive`, `stores.pg-cascade-in-one-transaction`, `stores.pg-run-for-session-active`, `stores.pg-run-for-session-none`, `stores.pg-run-for-session-ineligible`.
  - verify: `task spec:coverage` lists every ID above as covered and lists none that is absent. Boundary note: the `openspec` CLI in this workspace is a stub that exits 64, so no `openspec validate` gate is run or claimed; the task spec plus `task spec:coverage` is the check.
- [x] 8.2 At apply time, extend the `stores` Contract listing (`openspec/specs/stores/spec.md:641`) with the package-local transaction seams this change adds (`Runs`, `Notes`, `Outputs`, `Checkpoints` each gain `deleteSessionDependentsTx` beside the existing `CompleteTx` sentence) and regenerate the types index. No port gains a method, and the index regeneration covers what the adapter actually exposes.
  - verify: `task api:check` reports no port-method change and the regenerated types index matches the adapter's exported surface.
- [x] 8.3 At apply time, record an ADR only if a decision from this change needs one; do not reserve an ADR number during authoring, and leave the ADR task unclaimed if none is needed.
  - verify: `task spec` is green for the change package and the archive records either the ADR or the decision that none was required. Boundary note: the `openspec` CLI in this workspace is a stub that exits 64, so no `openspec validate` gate is run or claimed.

## 9. Lane verification

- [x] 9.1 Run the lane acceptance and report it as exactly the owned scenario IDs plus the public paths they drive: `SessionLog.Sessions` and `SessionLog.Purge`, `Outputs.Put` and `Outputs.Get`, `Outputs.EraseSubject`, `SessionLog.Delete`/`Purge`/`EraseSessions`, `Journal.Reserve`/`ReserveTx`, `Runs.Start`, `Runs.Notices`, `Runs.RunForSession` and `Runs.Signal`.
  - verify: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v ./...` and `timeout 3m go -C adapter/postgres test -short -timeout 2m ./...` and `task lint` are green; every owned ID appears as a named subtest with a nonzero match. M1 remains the completed regression floor; the global M2 gate is not a mandatory green gate for this lane.
- [x] 9.2 Record the change-specific regression depth at implementation: existing-service-strict behavior is asserted on every repaired public path, and each repair adds a focused failing-then-passing case before its fix lands. Regression evidence shape, 12 fields per repaired path: `capability`, `scenario_id`, `public_path`, `repair`, `red_test`, `red_failure`, `green_test`, `green_result`, `test_file`, `test_command`, `match_count` (nonzero), `first_observed`. One record per repaired path; no field is omitted and no value is inferred.
  - verify: each ID in 8.1 has a named subtest that fails without its repair and passes with it, and each carries a 12-field evidence record as above; no scenario is satisfied by a zero-matched test.

### 9.2 Regression evidence (reconstructed, first_observed 2026-10-11)

Scratch artifacts: `/tmp/pg-regression-evidence/`. Every record below is a reconstruction run on this working tree (green captured first, then a repair-reversal captured red, then the tree restored byte-identical — `git status --porcelain adapter/postgres` empty after restoration). Fixtures: testcontainers `postgres:16-alpine` (`adapter/postgres/testenv_test.go:53`), per-test scratch schemas (`bindSchema`, `adapter/postgres/storetest_bind_test.go:23`); Docker required for every command shown. Shared green command template `G(parent)`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run '^<parent>$' ./...` (exit 0); red commands replace `-run` with the subtest path. No ADR/redesign implied by any record.

- **stores.pg-output-shared-ref-survives-erase**
  - `repair_group`: output claims (2.1/2.2)
  - `public_entry`: Outputs.Put, Outputs.Get, Outputs.EraseSubject
  - `parent_and_subtest`: TestOutputClaimsLedger / stores.pg-output-shared-ref-survives-erase
  - `test_file_and_lines`: adapter/postgres/output_claim_test.go:24
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 (container dsn) + schema `bind_outshared`
  - `asserted_invariant`: equal bytes yield one ref; erasing one claiming subject removes 0 refs and leaves the ref Get-able with exactly 1 surviving claim (claim-aware, not owner-scope, deletion)
  - `baseline_or_repair_reversal`: reversal keeps assertions and reverts EraseSubject's delete to owner-scope: `DELETE FROM output_record o WHERE (o.tenant = $1 AND o.subject = $2)` without the two `NOT EXISTS` claim clauses (outputs.go EraseSubject)
  - `red_command_and_artifact`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run '^TestOutputClaimsLedger$' ./...`, exit 1, `/tmp/pg-regression-evidence/red_output_ledger.log`
  - `red_failure`: output_claim_test.go:45 `EraseSubject u1 removed 1 refs, want 0: u2 still claims the ref` (contract violation, assertions intact)
  - `green_command_and_artifact`: G(TestOutputClaimsLedger), exit 0, `/tmp/pg-regression-evidence/green_output_ledger.log`
  - `green_terminal_pass`: `--- PASS: TestOutputClaimsLedger/stores.pg-output-shared-ref-survives-erase (5.29s)`, match_count 1
- **stores.pg-output-last-claim-deletes-ref**
  - `repair_group`: output claims (2.3)
  - `public_entry`: Outputs.Put via SessionLog.Delete (deleteSessionDependentsTx cascade)
  - `parent_and_subtest`: TestOutputClaimsLedger / stores.pg-output-last-claim-deletes-ref
  - `test_file_and_lines`: adapter/postgres/output_claim_test.go:59
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schema `bind_outlast`
  - `asserted_invariant`: deleting the last claiming session drops the claim and the bytes (Get → ErrOutputNotFound, claims 0)
  - `baseline_or_repair_reversal`: reversal keeps assertions and no-ops the cascade claim delete: `DELETE FROM output_claim WHERE false AND session_id = $1` (outputs.go deleteSessionDependentsTx)
  - `red_command_and_artifact`: same run as shared-ref record, exit 1, `/tmp/pg-regression-evidence/red_output_ledger.log`
  - `red_failure`: output_claim_test.go:79 `Get after last claim dropped: err=<nil>, want ErrOutputNotFound`
  - `green_command_and_artifact`: G(TestOutputClaimsLedger), exit 0, `/tmp/pg-regression-evidence/green_output_ledger.log`
  - `green_terminal_pass`: `--- PASS: TestOutputClaimsLedger/stores.pg-output-last-claim-deletes-ref (0.02s)`, match_count 1
- **stores.pg-output-legacy-claims-preserved**
  - `repair_group`: output claims (1.2 migration backfill + 2.2)
  - `public_entry`: postgres.Migrate up 4→5, Outputs.Get, Outputs.EraseSubject
  - `parent_and_subtest`: TestOutputClaimsLedger / stores.pg-output-legacy-claims-preserved
  - `test_file_and_lines`: adapter/postgres/output_claim_test.go:86
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schema `bind_outlegacy`, seeded session_record/output_record/run_record rows migrated 4→5 (fixtures preserved; no synthetic session invented)
  - `asserted_invariant`: upgrade backfills 2 session claims on a shared ref and 1 legacy owner claim on an unattributed ref; owner-scope erase of a still-claimed owner removes 0; the orphan's only owner erases exactly 1
  - `baseline_or_repair_reversal`: reversal keeps assertions and disables the 00005 backfill inserts (`WHERE false` on both INSERT...SELECT in adapter/postgres/migrations/00005_output_claims.sql)
  - `red_command_and_artifact`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run 'TestOutputClaimsLedger/stores.pg-output-legacy-claims-preserved' ./...`, exit 1, `/tmp/pg-regression-evidence/red_output-legacy-claims-preserved.log`
  - `red_failure`: output_claim_test.go:127 `session claims on reconstructed ref = 0, want 2`
  - `green_command_and_artifact`: G(TestOutputClaimsLedger), exit 0, `/tmp/pg-regression-evidence/green_output_ledger.log`
  - `green_terminal_pass`: `--- PASS: TestOutputClaimsLedger/stores.pg-output-legacy-claims-preserved (0.03s)`, match_count 1
- **stores.pg-output-put-erase-race**
  - `repair_group`: output claims (2.1)
  - `public_entry`: Outputs.Put racing Outputs.EraseSubject
  - `parent_and_subtest`: TestOutputClaimsLedger / stores.pg-output-put-erase-race
  - `test_file_and_lines`: adapter/postgres/output_claim_test.go:154
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schema `bind_outrace`, 25 concurrent Put/EraseSubject rounds (concurrency preserved)
  - `asserted_invariant`: after the race every Put/EraseSubject pair returns nil, the ref stays Get-able and carries exactly 1 claim (ref-keyed advisory lock serialises claim insert against last-claim removal)
  - `baseline_or_repair_reversal`: reversal keeps assertions and skips the new claim-statement path: Put's `INSERT INTO output_claim ... VALUES (...) ON CONFLICT DO NOTHING` becomes `INSERT ... SELECT $1,$2,$3,$4 WHERE false` (claim insert stub); the bare lock removal alone was run and stayed green (window not hit in 25 rounds, recorded honestly) — the claim-stub reversal is the filed red
  - `red_command_and_artifact`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run 'TestOutputClaimsLedger/stores.pg-output-put-erase-race' ./...`, exit 1, `/tmp/pg-regression-evidence/red_output-put-erase-race.log`
  - `red_failure`: output_claim_test.go:186 `claims on ref after race = 0, want 1`
  - `green_command_and_artifact`: G(TestOutputClaimsLedger), exit 0, `/tmp/pg-regression-evidence/green_output_ledger.log`
  - `green_terminal_pass`: `--- PASS: TestOutputClaimsLedger/stores.pg-output-put-erase-race (0.03s)`, match_count 1
- **stores.pg-sessions-order-and-filters**
  - `repair_group`: session index (3.1)
  - `public_entry`: SessionLog.Sessions
  - `parent_and_subtest`: TestSessions / stores.pg-sessions-order-and-filters
  - `test_file_and_lines`: adapter/postgres/session_remediation_test.go:48
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schema `sessorder` (owner/kind/archived/LastActivity from SQL envelope, no `last_activity` column read)
  - `asserted_invariant`: SQL-computed LastActivity descending, session_id descending, primary+fork only, owner/filter scoping
  - `baseline_or_repair_reversal`: reversal keeps assertions and reverts the materialization order to `row_number() OVER (ORDER BY act ASC, session_id ASC)` (session.go materializeSessionCursor)
  - `red_command_and_artifact`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run 'TestSessions/stores.pg-sessions-order-and-filters' ./...`, exit 1, `/tmp/pg-regression-evidence/red_sessions-order-and-filters.log`
  - `red_failure`: session_remediation_test.go:85 `default page = [so-new so-fork so-old], want primaries and fork only, bumped oldest first`
  - `green_command_and_artifact`: G(TestSessions), exit 0, `/tmp/pg-regression-evidence/green_sessions.log`
  - `green_terminal_pass`: `--- PASS: TestSessions/stores.pg-sessions-order-and-filters (5.78s)`, match_count 1
- **stores.pg-sessions-cursor-stable**
  - `repair_group`: session index (3.2)
  - `public_entry`: SessionLog.Sessions (continuation)
  - `parent_and_subtest`: TestSessions / stores.pg-sessions-cursor-stable
  - `test_file_and_lines`: adapter/postgres/session_remediation_test.go:113
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schema `sessstable` (mid-listing activity bump preserved)
  - `asserted_invariant`: activity changes between pages must not move the snapshot boundary
  - `baseline_or_repair_reversal`: reversal keeps assertions and skips membership materialization (`FROM session_record WHERE false AND (<where>)` in materializeSessionCursor), so continuations see no frozen membership
  - `red_command_and_artifact`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run '^TestSessions$' ./...`, exit 1, `/tmp/pg-regression-evidence/red_sessions_batch.log`
  - `red_failure`: session_remediation_test.go:123 `page 1 = [] cursor "", want [ss-3] with a cursor`
  - `green_command_and_artifact`: G(TestSessions), exit 0, `/tmp/pg-regression-evidence/green_sessions.log`
  - `green_terminal_pass`: `--- PASS: TestSessions/stores.pg-sessions-cursor-stable (0.03s)`, match_count 1
- **stores.pg-sessions-snapshot-complete**
  - `repair_group`: session index (3.2)
  - `public_entry`: SessionLog.Sessions
  - `parent_and_subtest`: TestSessions / stores.pg-sessions-snapshot-complete
  - `test_file_and_lines`: adapter/postgres/session_remediation_test.go:145
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schema `sessbulk`, 1601 appended sessions, mid-listing bump at page 4 (rows and pagination preserved)
  - `asserted_invariant`: complete membership listed exactly once in snapshot order despite concurrent activity
  - `baseline_or_repair_reversal`: same materialization skip as cursor-stable
  - `red_command_and_artifact`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run '^TestSessions$' ./...`, exit 1, `/tmp/pg-regression-evidence/red_sessions_batch.log`
  - `red_failure`: session_remediation_test.go:185 `listed 0 sessions, want 1601`
  - `green_command_and_artifact`: G(TestSessions), exit 0, `/tmp/pg-regression-evidence/green_sessions.log`
  - `green_terminal_pass`: `--- PASS: TestSessions/stores.pg-sessions-snapshot-complete (0.24s)`, match_count 1
- **stores.pg-sessions-cursor-expiration**
  - `repair_group`: session index (3.2/3.3)
  - `public_entry`: SessionLog.Sessions (continuation)
  - `parent_and_subtest`: TestSessions / stores.pg-sessions-cursor-expiration
  - `test_file_and_lines`: adapter/postgres/session_remediation_test.go:197
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schema `sessexpire` + advClock advanced past stores.LeaseTTL (clock, not sleeps)
  - `asserted_invariant`: an expired continuation is refused with an error prefixed `list sessions: invalid cursor`
  - `baseline_or_repair_reversal`: reversal keeps assertions and drops the expiry/owner/filter/not-found refusals in readSessionCursorPage
  - `red_command_and_artifact`: batch run above, exit 1, `/tmp/pg-regression-evidence/red_sessions_batch.log`
  - `red_failure`: session_remediation_test.go:209 `expired continuation err = <nil>, want invalid cursor`
  - `green_command_and_artifact`: G(TestSessions), exit 0, `/tmp/pg-regression-evidence/green_sessions.log`
  - `green_terminal_pass`: `--- PASS: TestSessions/stores.pg-sessions-cursor-expiration (0.02s)`, match_count 1
- **stores.pg-sessions-cursor-pruned**
  - `repair_group`: session index (3.3)
  - `public_entry`: SessionLog.Sessions and SessionLog.Purge
  - `parent_and_subtest`: TestSessions / stores.pg-sessions-cursor-pruned
  - `test_file_and_lines`: adapter/postgres/session_remediation_test.go:213
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schema `sessprune` + advClock (expired headers and items counted via catalog)
  - `asserted_invariant`: Sessions prunes expired headers before a page; Purge prunes them inside its transaction even when it removes no sessions; items go with the header (no Maintain method)
  - `baseline_or_repair_reversal`: reversal keeps assertions and no-ops both prune deletes: `DELETE FROM session_cursor WHERE false AND expires_at <= $1` (session.go Sessions + Purge)
  - `red_command_and_artifact`: batch run above, exit 1, `/tmp/pg-regression-evidence/red_sessions_batch.log`
  - `red_failure`: session_remediation_test.go:226 `Sessions left 2 cursors, want the fresh one only`
  - `green_command_and_artifact`: G(TestSessions), exit 0, `/tmp/pg-regression-evidence/green_sessions.log`
  - `green_terminal_pass`: `--- PASS: TestSessions/stores.pg-sessions-cursor-pruned (0.03s)`, match_count 1
- **stores.pg-sessions-cursor-owner-scope**
  - `repair_group`: session index (3.2)
  - `public_entry`: SessionLog.Sessions (foreign-owner continuation)
  - `parent_and_subtest`: TestSessions / stores.pg-sessions-cursor-owner-scope
  - `test_file_and_lines`: adapter/postgres/session_remediation_test.go:245
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schema `sessscope` (owner boundary preserved)
  - `asserted_invariant`: a cursor another owner submits is refused with types.ErrSessionForbidden
  - `baseline_or_repair_reversal`: same refusal-drop reversal as cursor-expiration
  - `red_command_and_artifact`: batch run above, exit 1, `/tmp/pg-regression-evidence/red_sessions_batch.log`
  - `red_failure`: session_remediation_test.go:256 `foreign owner continuation err = <nil>, want ErrSessionForbidden`
  - `green_command_and_artifact`: G(TestSessions), exit 0, `/tmp/pg-regression-evidence/green_sessions.log`
  - `green_terminal_pass`: `--- PASS: TestSessions/stores.pg-sessions-cursor-owner-scope (0.03s)`, match_count 1
- **stores.pg-sessions-cursor-membership**
  - `repair_group`: session index (3.2)
  - `public_entry`: SessionLog.Sessions (continuation)
  - `parent_and_subtest`: TestSessions / stores.pg-sessions-cursor-membership
  - `test_file_and_lines`: adapter/postgres/session_remediation_test.go:260
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schema `sessmember`, direct `UPDATE session_record SET archived` mid-listing (migrated rows touched, filters frozen)
  - `asserted_invariant`: sessions appended and rows archived after materialization stay out of the frozen membership; no cursor at snapshot end
  - `baseline_or_repair_reversal`: same materialization skip as cursor-stable
  - `red_command_and_artifact`: batch run above, exit 1, `/tmp/pg-regression-evidence/red_sessions_batch.log`
  - `red_failure`: session_remediation_test.go:279 `page 2 = [], want frozen membership [sm-1]`
  - `green_command_and_artifact`: G(TestSessions), exit 0, `/tmp/pg-regression-evidence/green_sessions.log`
  - `green_terminal_pass`: `--- PASS: TestSessions/stores.pg-sessions-cursor-membership (0.02s)`, match_count 1
- **stores.pg-sessions-cursor-invalid**
  - `repair_group`: session index (3.2)
  - `public_entry`: SessionLog.Sessions (malformed/missing/filter-mismatched cursor)
  - `parent_and_subtest`: TestSessions / stores.pg-sessions-cursor-invalid
  - `test_file_and_lines`: adapter/postgres/session_remediation_test.go:286
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schema `sessinvalid`, `encodeTestCursor` helper for the missing id
  - `asserted_invariant`: malformed, missing and filter-mismatched cursors are refused with the `list sessions: invalid cursor` prefix
  - `baseline_or_repair_reversal`: same refusal-drop reversal as cursor-expiration (not-found no longer refused)
  - `red_command_and_artifact`: batch run above, exit 1, `/tmp/pg-regression-evidence/red_sessions_batch.log`
  - `red_failure`: session_remediation_test.go:299 `missing cursor err = <nil>, want invalid cursor`
  - `green_command_and_artifact`: G(TestSessions), exit 0, `/tmp/pg-regression-evidence/green_sessions.log`
  - `green_terminal_pass`: `--- PASS: TestSessions/stores.pg-sessions-cursor-invalid (0.02s)`, match_count 1
- **stores.pg-journal-concurrent-reserve**
  - `repair_group`: journal (4.1/4.2)
  - `public_entry`: Journal.Reserve and Journal.ReserveTx (same statement path)
  - `parent_and_subtest`: TestJournalReserveAtomic / stores.pg-journal-concurrent-reserve / pool + tx
  - `test_file_and_lines`: adapter/postgres/journal_reserve_test.go:19
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schemas `bind_journalrace`/`bind_journalracetx`, 10 concurrent reservers (concurrency preserved)
  - `asserted_invariant`: insert-first `ON CONFLICT (session_id, call_id) DO NOTHING RETURNING 1` yields exactly one `created=true` and one journal row through both entry points
  - `baseline_or_repair_reversal`: reversal keeps assertions and removes the `ON CONFLICT (session_id, call_id) DO NOTHING` clause (journal.go reserveOnTx)
  - `red_command_and_artifact`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run '^TestJournalReserveAtomic$' ./...`, exit 1, `/tmp/pg-regression-evidence/red_journal.log`
  - `red_failure`: journal_reserve_test.go:43 and :96 `Reserve/ReserveTx: journal reserve: ERROR: duplicate key value violates unique constraint "journal_record_pkey" (SQLSTATE 23505)` — the race surfaces a raw unique violation instead of a fresh-reserved miss
  - `green_command_and_artifact`: G(TestJournalReserveAtomic), exit 0, `/tmp/pg-regression-evidence/green_journal.log`
  - `green_terminal_pass`: `--- PASS: TestJournalReserveAtomic/stores.pg-journal-concurrent-reserve (2.06s)` with pool and tx subtests, match_count 3
- **stores.pg-operation-id-cross-session-race**
  - `repair_group`: operation-id uniqueness (5.1)
  - `public_entry`: Runs.Start
  - `parent_and_subtest`: TestRunsRemediation / stores.pg-operation-id-cross-session-race
  - `test_file_and_lines`: adapter/postgres/runs_remediation_test.go:35
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + advClock (`newRemediationRuns`), cross-session Start race on one operation id
  - `asserted_invariant`: the (tenant, operation_id) advisory lock taken before the session lock makes run_record_operation the authority and the loser receives stores.OperationExistsError, never a raw 23505
  - `baseline_or_repair_reversal`: reversal keeps assertions and skips the duplicate lookup under the operation lock (`if false` replacing `if r.OperationID != ""`, runs.go Start); the lock-skip alone was run first and stayed green (race window not hit) — the lookup-skip reversal is the filed red
  - `red_command_and_artifact`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run 'TestRunsRemediation/stores.pg-operation-id-cross-session-race' ./...`, exit 1, `/tmp/pg-regression-evidence/red_operation-id-cross-session-race.log`
  - `red_failure`: runs_remediation_test.go:71 `loser 1 err = start run r-cross-b: ERROR: duplicate key ... "run_record_operation" (SQLSTATE 23505), want OperationExistsError`
  - `green_command_and_artifact`: G(TestRunsRemediation), exit 0, `/tmp/pg-regression-evidence/green_runs.log`
  - `green_terminal_pass`: `--- PASS: TestRunsRemediation/stores.pg-operation-id-cross-session-race (4.73s)`, match_count 1
- **stores.pg-notice-claim-exclusive**
  - `repair_group`: notices (6.1)
  - `public_entry`: Runs.Notices
  - `parent_and_subtest`: TestNoticesRemediation / stores.pg-notice-claim-exclusive
  - `test_file_and_lines`: adapter/postgres/runs_notices_remediation_test.go:40
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + advClock, concurrent dispatchers over seeded run_notice rows
  - `asserted_invariant`: `SELECT ... WHERE claim IS NULL OR claim <= $cutoff ... FOR UPDATE SKIP LOCKED` + update-only-locked-rows: concurrent dispatchers receive disjoint notice sets, no duplicates
  - `baseline_or_repair_reversal`: reversal keeps assertions and makes the claim filter tautological (`OR claim > $1`), so already-claimed rows are returned to later dispatchers (runs_notices.go Notices)
  - `red_command_and_artifact`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run '^TestNoticesRemediation$' ./...`, exit 1, `/tmp/pg-regression-evidence/red_notices.log`
  - `red_failure`: runs_notices_remediation_test.go:68 `notice nt-5c09736d14c58623 claimed by dispatchers 0 and 2` (and five more duplicate-delivery lines)
  - `green_command_and_artifact`: G(TestNoticesRemediation), exit 0, `/tmp/pg-regression-evidence/green_notices.log`
  - `green_terminal_pass`: `--- PASS: TestNoticesRemediation/stores.pg-notice-claim-exclusive (2.65s)`, match_count 1
- **stores.pg-cascade-in-one-transaction**
  - `repair_group`: cascade (7.1/7.2)
  - `public_entry`: SessionLog.Delete, SessionLog.Purge, SessionLog.EraseSessions
  - `parent_and_subtest`: TestSessionCascadeRemediation / stores.pg-cascade-in-one-transaction
  - `test_file_and_lines`: adapter/postgres/session_remediation_test.go:375
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + schemas `bind_cascadecommit`/`bind_cascaderollback`, seeded session+run+output+checkpoint rows, injected failing dependent (rollback case preserved)
  - `asserted_invariant`: the tx-seam (`deleteSessionDependentsTx`) makes the session and every dependent row commit or roll back as one: commit path leaves all counts 0, any failing dependent rolls back leaving all counts 1 through Delete, Purge and EraseSessions
  - `baseline_or_repair_reversal`: reversal keeps assertions and forces every dependent through the non-transactional pool fallback (`ok && false` on the sessionDependentTx type assertion, session.go removeSession)
  - `red_command_and_artifact`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run '^TestSessionCascadeRemediation$' ./...`, exit 1, `/tmp/pg-regression-evidence/red_cascade.log`
  - `red_failure`: session_remediation_test.go:390 `run_record rows = 0, want 1` — the pool-fallback dependent's deletion stayed committed after the injected failure
  - `green_command_and_artifact`: G(TestSessionCascadeRemediation), exit 0, `/tmp/pg-regression-evidence/green_cascade.log`
  - `green_terminal_pass`: `--- PASS: TestSessionCascadeRemediation/stores.pg-cascade-in-one-transaction (2.70s)`, match_count 1
- **stores.pg-run-for-session-active**
  - `repair_group`: run discovery (5.2)
  - `public_entry`: Runs.RunForSession
  - `parent_and_subtest`: TestRunsRemediation / stores.pg-run-for-session-active
  - `test_file_and_lines`: adapter/postgres/runs_remediation_test.go:82
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + advClock (`newRemediationRuns`)
  - `asserted_invariant`: the most recent run in state Running/Resuming with a non-null lease expiring after the store clock is returned decoded
  - `baseline_or_repair_reversal`: reversal keeps assertions and inverts the lease predicate (`lease_expires <= $4`, runs.go RunForSession) plus swallows ErrNoRows
  - `red_command_and_artifact`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run '^TestRunsRemediation$' ./...`, exit 1, `/tmp/pg-regression-evidence/red_runs_remediation.log`
  - `red_failure`: runs_remediation_test.go:94 `RunForSession = (, 0), want (r-live, Running)`
  - `green_command_and_artifact`: G(TestRunsRemediation), exit 0, `/tmp/pg-regression-evidence/green_runs.log`
  - `green_terminal_pass`: `--- PASS: TestRunsRemediation/stores.pg-run-for-session-active (0.03s)`, match_count 1
- **stores.pg-run-for-session-none**
  - `repair_group`: run discovery (5.2)
  - `public_entry`: Runs.RunForSession
  - `parent_and_subtest`: TestRunsRemediation / stores.pg-run-for-session-none
  - `test_file_and_lines`: adapter/postgres/runs_remediation_test.go:101
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + advClock
  - `asserted_invariant`: no eligible row yields stores.ErrRunNotFound, not a zero Run
  - `baseline_or_repair_reversal`: same reversal as run-for-session-active (ErrNoRows branch returns a zero Run with nil error)
  - `red_command_and_artifact`: batch run above, exit 1, `/tmp/pg-regression-evidence/red_runs_remediation.log`
  - `red_failure`: runs_remediation_test.go:108 `expired lease err = <nil>, want ErrRunNotFound`
  - `green_command_and_artifact`: G(TestRunsRemediation), exit 0, `/tmp/pg-regression-evidence/green_runs.log`
  - `green_terminal_pass`: `--- PASS: TestRunsRemediation/stores.pg-run-for-session-none (0.03s)`, match_count 1
- **stores.pg-run-for-session-ineligible**
  - `repair_group`: run discovery (5.2)
  - `public_entry`: Runs.RunForSession
  - `parent_and_subtest`: TestRunsRemediation / stores.pg-run-for-session-ineligible
  - `test_file_and_lines`: adapter/postgres/runs_remediation_test.go:112
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + advClock (terminal-state and expired-lease rows seeded)
  - `asserted_invariant`: terminal-state runs and expired leases are never returned (state + lease_expires eligibility)
  - `baseline_or_repair_reversal`: same reversal as run-for-session-active (inverted lease predicate returns ineligible rows)
  - `red_command_and_artifact`: batch run above, exit 1, `/tmp/pg-regression-evidence/red_runs_remediation.log`
  - `red_failure`: runs_remediation_test.go:121 `terminal run err = <nil>, want ErrRunNotFound`
  - `green_command_and_artifact`: G(TestRunsRemediation), exit 0, `/tmp/pg-regression-evidence/green_runs.log`
  - `green_terminal_pass`: `--- PASS: TestRunsRemediation/stores.pg-run-for-session-ineligible (0.11s)`, match_count 1
- **stores.pg-fresh-equals-upgraded**
  - `repair_group`: fresh-upgraded (1.1/1.2)
  - `public_entry`: postgres.Migrate (fresh head vs version-by-version upgrade)
  - `parent_and_subtest`: TestMigrations / stores.pg-fresh-equals-upgraded
  - `test_file_and_lines`: adapter/postgres/migrate_bloat_test.go:18 (harness testkit/storetest/migrations.go:36)
  - `legal_fixture_path`: adapter/postgres/testenv_test.go:53 + scratch schemas `mig_<from>_<ts>` per path
  - `asserted_invariant`: the 00005 migration is additive-only, so fresh-at-head and upgraded-from-1 produce identical catalogs
  - `baseline_or_repair_reversal`: reversal attempted and recorded: both 00005 backfill inserts disabled with `WHERE false` (data-level reversal) — the test stayed green because the harness compares catalogs of empty installs, so backfill data drift is invisible to it; a source reversal of the shipped file set cannot make the two paths' catalogs differ
  - `red_command_and_artifact`: `timeout 12m go -C adapter/postgres test -tags full -timeout 10m -v -run '^TestMigrations$' ./...`, exit 0, `/tmp/pg-regression-evidence/red_fresh-equals-upgraded.log` — red NOT reproduced
  - `red_failure`: not reproduced; the scenario's regression depth for the backfill is carried by the stores.pg-output-legacy-claims-preserved red above, which fails on the same reversal
  - `green_command_and_artifact`: G(TestMigrations), exit 0, `/tmp/pg-regression-evidence/green_fresh_upgraded.log`
  - `green_terminal_pass`: `--- PASS: TestMigrations/stores.pg-fresh-equals-upgraded (3.14s)`, match_count 1

Boundary note: all 20 records are reconstructions dated 2026-10-11 on the current tree; none claims historical test-first. Scratch cleanup intentionally left in place under `/tmp/pg-regression-evidence/` as the artifact trail.