# Tasks: m0-core

Scope: the 21 M0 capabilities named in `proposal.md`. Order follows dependency and the proof → identity → verification → scaffold order. Per-scenario milestone ownership lives in `openspec/scenarios.json` as `deferred_to` (ADR-0135); the rows below are the scope this plan was reviewed at, and the chunks under each row are what you land.

## How to run this plan

- A **chunk** is the executable unit: one package or type-cluster, at most four scenario IDs, its own files, one literal verify command. Work chunks in order within a row.
- A chunk is done when its `verify:` command is green and `task lint` and `task test` are green for the packages it touched. The subtest that satisfies a scenario is named exactly by its ID (`t.Run("flow.plain-invoke", …)`); the `-run` expression in a verify command names the *test function* the chunk introduces.
- `task spec:coverage` reports every registered scenario without a subtest and every subtest named like an unregistered ID, and fails on the latter. `task spec:coverage --gate <milestone>` additionally fails when a scenario with no later `deferred_to` has no subtest — that is the milestone exit check, not the per-chunk check (ADR-0135).
- Chunks marked **GATE** need the named project command as well.
- `core/` is a package tree (ADR-0139): the driver is `core/` with package clause `gohan`, the shared vocabulary and ports are in `core/types`, and each enum lives in its own package. Imports run downward only; no leaf package imports `core/` or `core/runtime`. A chunk's `core/` label and file path say where its declarations land — the package table in `openspec/changes/m0-core/design.md` decides which leaf, so `core/store_session.go` lands as `core/stores/session.go` and `core/credential.go` as `core/types/credential.go` — and `./core/...` in a verify command covers the whole tree.
- Row 1 carries no scenario: it lands the module, the tooling and the coverage gate every later row's definition of done depends on. It cannot require green lint and tests, because the first compilable unit arrives in row 2.

## A. Module and types

1. [ ] Create the root module (`github.com/victorzhuk/gohan`, go 1.27, Apache-2.0), `go.work`, Taskfile with `spec:types`, `spec:coverage` (scenarios.json vs `go test -list`), `lint`, `test`, `bench`; golangci-lint + depguard config encoding the core budget rule; `.github/workflows/ci.yml` with jobs `spec` (`spec:types`, `spec:coverage`), `lint`, `test` (root + adapter matrix, path-filtered, `-short`), `conformance` (each adapter × root `testkit`), `bench` (gate, pull requests only, reference runner), `examples` (offline from cassettes); all required checks on `main`; `LICENSE` (Apache-2.0), `README.md`. — no scenarios

- [ ] 1.1 `core`: Scaffold `github.com/victorzhuk/gohan` with Go 1.27 and a minimal `.gitignore`; retain completed `git init`, branch `master`, and `origin` = `git@github.com:victorzhuk/gohan.git`.
  - files: `go.mod`, `.gitignore`
  - scenarios: none
  - verify: `go mod edit -json`

- [ ] 1.2 `core`: Commit `go.work`/`go.work.sum` as development wiring, never version authority; add Apache-2.0 `LICENSE` and `README.md`.
  - files: `go.work`, `go.work.sum`, `LICENSE`, `README.md`
  - scenarios: none
  - verify: `go work edit -json`

- [ ] 1.3 `core`: Add Taskfile targets `spec:types`, `spec:coverage`, `lint`, `test`, `bench`, pinned tooling and golangci-lint + depguard enforcement of the core budget rule.
  - files: `Taskfile.yml`, `.golangci.yml`, `go.mod`
  - scenarios: none
  - verify: `task --list-all`

- [ ] 1.4 `core`: Implement `spec:coverage` under ADR-0135: discover actual scenario subtests, reject unregistered IDs and missing IDs without later `deferred_to`; use execution JSON because `go test -list` does not enumerate subtests. **GATE**
  - files: `tools/spec_coverage.py`, `tools/spec_coverage_test.py`, `Taskfile.yml`
  - scenarios: none
  - verify: `timeout 2m python3 -m unittest discover -s tools -p 'spec_coverage_test.py'`

- [ ] 1.5 `core`: Add CI checks `spec`, `lint`, `test`, `bench` required on `master`; path-filter the empty M0 adapter matrix, run tests with `-short`, and gate PR benchmarks on the reference runner; add `api:check` only at M4; defer conformance/examples jobs until their suites exist; row 1 cannot require green lint/test because row 2 introduces the first compilable unit.
  - files: `.github/workflows/ci.yml`, `README.md`
  - scenarios: none
  - verify: `timeout 2m actionlint .github/workflows/ci.yml`

2. [ ] `core`: message model — `Message`, `Block` kinds incl. `Compaction`, `Origin`, `Role`, `ModelRequest`/`ModelChunk`/`Usage`, `ToolUse`/`ToolResult`/`Outcome`. Round-trip property tests for the block model. — `messages.order-preserved`, `messages.typed-deltas`, `messages.duplicate-keys-in-tool-args`

- [ ] 2.1 `core`: Implement `Message`, `Role`, `BlockBase` with `BlockOrigin`, all `Block` kinds including `Compaction`, `ToolUse`, `ToolResult` and `Outcome`; preserve ordered blocks and reasoning signatures in round-trip property tests.
  - files: `core/message.go`, `core/message_test.go`
  - scenarios: `messages.order-preserved`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestMessageRoundTrip'`

- [ ] 2.2 `core`: Implement `ModelRequest`, `ModelChunk`, `Usage` and their supporting data types; preserve `DeltaReasoning`, `DeltaText` and `DeltaToolArgs` distinctions.
  - files: `core/model_types.go`, `core/model_types_test.go`
  - scenarios: `messages.typed-deltas`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestModelTypes'`

- [ ] 2.3 `core`: Validate complete `ToolUse.Args` with `json/v2`; reject duplicate JSON keys as `ToolResult` with `Failed`/`Permanent` for the later runtime to consume.
  - files: `core/tool_args.go`, `core/tool_args_test.go`
  - scenarios: `messages.duplicate-keys-in-tool-args`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolArgs'`

3. [ ] `core`: events and stop reasons — `Event` kinds, `EventMeta` with `Seq`, `StopReason`; error classes and sentinel errors from `docs/design/types.md`; `ErrorCode` catalog, `Problem`, `ProblemOf` (`errors.every-sentinel-has-code`, `errors.problem-of-unknown-is-internal`, `errors.detail-never-carries-provider-body`, `errors.retry-after-on-retryable`, `streams.terminal-error-event`, `streams.close-without-done-is-interrupted`). — `streams.monotonic-seq`

- [ ] 3.1 `core`: Implement `Event` kinds, `EventMeta`, `StopReason` and supporting event payload types; assign gapless run-local `Seq` starting at 1 through `Done.Seq`.
  - files: `core/event.go`, `core/event_test.go`
  - scenarios: `streams.monotonic-seq`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestEventSequence'`

- [ ] 3.2 `core`: Implement error classes, sentinel errors from `docs/design/types.md`, the `ErrorCode` catalog, `Problem` and `ProblemOf`; enforce catalog completeness and the unknown-error `gohan.internal` fallback. **GATE**
  - files: `core/errors.go`, `core/problem.go`, `core/problem_test.go`, `tools/gen_types_index.py`
  - scenarios: `errors.every-sentinel-has-code`, `errors.problem-of-unknown-is-internal`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestProblemCatalog' && task spec:types`

- [ ] 3.3 `core`: Render `Problem.Detail` from fixed templates and allow-listed fields; exclude provider bodies and preserve retryable `RetryAfter`, including lease remaining for `ErrRunActive`.
  - files: `core/problem.go`, `core/problem_detail_test.go`
  - scenarios: `errors.detail-never-carries-provider-body`, `errors.retry-after-on-retryable`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestProblemDetail'`

- [ ] 3.4 `core`: Define terminal stream failure contracts using `Problem`, the next `EventMeta.Seq` and `gohan.stream_interrupted`; test core representations without introducing the M4 HTTP/SSE adapter.
  - files: `core/stream_error.go`, `core/stream_error_test.go`
  - scenarios: `streams.terminal-error-event`, `streams.close-without-done-is-interrupted`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStreamErrors'`

4. [ ] `core`: `RunInfo`, `Principal`, `CostTags`, context keys and `(T, bool)` accessors (`WithPrincipal`/`PrincipalFrom`, `WithCredential`, `WithIdempotencyKey`, `RunInfoFrom`), seam check for `ErrNoPrincipal`, `CredentialSource` port, `RunMode`, `ReleaseID`/`Variant` fields. — `identity.no-principal`, `identity.no-principal-at-seam`, `identity.accessor-outside-run`, `identity.model-cannot-set-identity`, `identity.token-never-exported`, `identity.credential-not-on-principal`

- [ ] 4.1 `core`: Implement `RunInfo`, `Principal`, `CostTags`, `RunMode`, `ReleaseID`/`Variant`, private context keys and `(T, bool)` accessors, including zero-value access outside a run.
  - files: `core/identity.go`, `core/identity_test.go`
  - scenarios: `identity.accessor-outside-run`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestIdentityContext'`

- [ ] 4.2 `core`: Implement the shared principal seam check returning `ErrNoPrincipal` unless anonymous invocation is allowed; require refusal before events or store access for later `Send`, `Invoke` and `Resume` wiring.
  - files: `core/identity_seam.go`, `core/identity_seam_test.go`
  - scenarios: `identity.no-principal`, `identity.no-principal-at-seam`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestPrincipalSeam'`

- [ ] 4.3 `core`: Implement `Credential`, `WithCredential`, `CredentialFrom` and the `CredentialSource` port; keep tokens separate from `Principal` and exclude them from exported identity representations.
  - files: `core/credential.go`, `core/credential_test.go`
  - scenarios: `identity.token-never-exported`, `identity.credential-not-on-principal`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestCredentialIsolation'`

- [ ] 4.4 `core`: Keep `PrincipalFrom(ctx)` authoritative when input names `user_id` or `tenant`; define the identity exclusion contract consumed by row 6 `NewTool`/`ExcludeFields`, without adding tool construction to this row.
  - files: `core/identity_args.go`, `core/identity_args_test.go`
  - scenarios: `identity.model-cannot-set-identity`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestIdentityArguments'`

5. [ ] Run `task spec:types` and diff the index against the package's exported identifiers; fix drift in either direction. — no scenarios

- [ ] 5.2 `core`: Extend `tools/gen_types_index.py` so it fails when one name is declared twice with different kinds in the same package — the check that would have caught the eight collisions of ADR-0139 — and fails cleanly (not with an uncaught `ValueError`) when a spec heading it indexes by name is missing. **GATE**
  - files: `tools/gen_types_index.py`, `tools/gen_types_index_test.py`
  - scenarios: none
  - verify: `timeout 2m python3 -m unittest discover -s tools -p 'gen_types_index_test.py'`

- [ ] 5.1 `core`: Run the `spec:types` drift gate: regenerate `docs/design/types.md`, compare implemented exported identifiers with their capability contracts, and fix implementation or spec/index drift in either direction without requiring later-row identifiers to exist. **GATE**
  - files: `docs/design/types.md`, `tools/gen_types_index.py`
  - scenarios: none
  - verify: `task spec:types && git diff --exit-code -- docs/design/types.md`

## B. Ports and memory stores

6. [ ] `core`: `Model`, `Tool`, `Decider`, `EgressPolicy` + `ErrEgressPolicyRequired` + exfil derivation (`tools.egress-policy-required`, `build.exfil-derived-from-egress`), name grammar and collision check (`tools.name-grammar`, `tools.collision-fails-build`, `tools.reserved-names`, `tools.rename-is-new-tool`, `tools.tool-value-shared-and-concurrent`), `ToolSpec` (incl. `Effect`, `Trust`, `Capabilities`, `Verify`, `Deferred`, `Executor`), `Usage.ProviderToolCalls`, `Caps.ProviderTools`, `NewTool` with schema derivation and `ExcludeFields`. — `tools.unknown-tool`, `tools.invalid-args-on-raw-tool`, `tools.classified-error`, `tools.schema-from-tags`, `tools.untyped-args-rejected`, `tools.out-passthrough`, `tools.panic-recovered`, `tools.default-timeout`, `decider.rules-confidence-one`, `decider.schema-validated`

- [ ] 6.1 `core`: Declare `Model`, `Decider`, `Decision`, `Usage.ProviderToolCalls` and `Caps.ProviderTools`; test confidence and decision-schema contracts.
  - files: `core/model.go`, `core/decider.go`, `core/decider_test.go`
  - scenarios: `decider.rules-confidence-one`, `decider.schema-validated`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestDeciderContract'`

- [ ] 6.2 `core`: Declare `Tool`, `ToolSpec`, `Effect`, `Trust`, `Capabilities`, `Verify`, `Deferred` and `Executor`; keep tool-policy values in `std` under row 17.
  - files: `core/tool.go`, `core/tool_test.go`
  - scenarios: `tools.tool-value-shared-and-concurrent`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolConcurrentValue'`

- [ ] 6.3 `core`: Enforce tool name grammar, collision checks, reserved names and rename identity in `NewTool` and `Build`.
  - files: `core/tool_names.go`, `core/tool_names_test.go`
  - scenarios: `tools.name-grammar`, `tools.collision-fails-build`, `tools.reserved-names`, `tools.rename-is-new-tool`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolNames'`

- [ ] 6.4 `core`: Declare `EgressPolicy` and `ErrEgressPolicyRequired`; reject missing policies and derive `Capabilities.Exfil` at `Build`.
  - files: `core/tool_egress.go`, `core/tool_egress_test.go`
  - scenarios: `tools.egress-policy-required`, `build.exfil-derived-from-egress`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolEgressContract'`

- [ ] 6.5 `core`: Derive `NewTool` schemas with the `json`/`desc`/`enum`/`min`/`max`/`pattern` walker, `ExcludeFields` and untyped-argument rejection.
  - files: `core/tool_schema.go`, `core/tool_schema_test.go`
  - scenarios: `tools.schema-from-tags`, `tools.untyped-args-rejected`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolSchema'`

- [ ] 6.6 `core`: Reject unknown tools and invalid raw `Tool` arguments before `Call`; preserve classified errors in `ToolResult`.
  - files: `core/tool_call.go`, `core/tool_call_test.go`
  - scenarios: `tools.unknown-tool`, `tools.invalid-args-on-raw-tool`, `tools.classified-error`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolCallContract'`

- [ ] 6.7 `core`: Complete `NewTool` output mapping, panic recovery and effect-specific timeout application.
  - files: `core/tool_new.go`, `core/tool_new_test.go`
  - scenarios: `tools.out-passthrough`, `tools.panic-recovered`, `tools.default-timeout`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestNewToolExecution'`

7. [ ] `core`: `SessionLog` port + memory implementation with versioned append and `Message.ID` assignment; `SessionIndex` on the memory store, `Stack.Sessions`/`UpdateSession`; `Stores.ForkSession`. — `stores.sessions-listed-by-owner`, `stores.sessions-order-last-activity`, `stores.sessions-default-excludes-forks-children`, `stores.session-index-optional`, `stores.purge-respects-pinned-and-archived`, `identity.sessions-owner-checked`, `stores.fork-prefix`, `stores.fork-requires-no-lease`, `stores.fork-survives-parent-delete`, `stores.reconstruct-crosses-fork`, `permission.grants-not-inherited-on-fork`, `working-state.fork-copies-notes-and-state`, `stores.append-conflict`, `stores.delete-cascades`, `identity.session-forbidden-cross-tenant`, `identity.session-scope-read`

- [ ] 7.1 `core`: Implement `SessionLog`, its memory store, versioned append, `Message.ID`, `SessionIndex`, `Stack.Sessions`/`UpdateSession` and `Stores.ForkSession` with ownership and deletion boundaries.
  - files: `core/store_session.go`, `core/store_session_memory.go`, `core/session.go`, `core/store_session_test.go`
  - scenarios: `stores.append-conflict`, `stores.delete-cascades`, `identity.session-forbidden-cross-tenant`, `identity.session-scope-read`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionLogContract'`

- [ ] 7.2 `core`: Test `SessionIndex` owner listings, activity order, default kinds and optional-interface refusal.
  - files: `core/session_index_test.go`
  - scenarios: `stores.sessions-listed-by-owner`, `stores.sessions-order-last-activity`, `stores.sessions-default-excludes-forks-children`, `stores.session-index-optional`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionIndexContract'`

- [ ] 7.3 `core`: Test session purge protection, listing ownership and fork isolation for grants, notes and shared state.
  - files: `core/session_lifecycle_test.go`
  - scenarios: `stores.purge-respects-pinned-and-archived`, `identity.sessions-owner-checked`, `permission.grants-not-inherited-on-fork`, `working-state.fork-copies-notes-and-state`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionLifecycle'`

- [ ] 7.4 `core`: Test `Stores.ForkSession` prefix preservation, lease refusal, parent-deletion independence and reconstruction ancestry.
  - files: `core/session_fork_test.go`
  - scenarios: `stores.fork-prefix`, `stores.fork-requires-no-lease`, `stores.fork-survives-parent-delete`, `stores.reconstruct-crosses-fork`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionFork'`

8. [ ] `core`: `Checkpoints` port + memory implementation, single-use `Consume`, `Checkpoint` shape incl. `Child`, `Workspace`. — `stores.concurrent-consume`, `stores.no-secrets-stored`, `suspension.token-reuse`

- [ ] 8.1 `core`: Implement `Checkpoints`, its memory store and `Checkpoint` with `Child`/`Workspace`, atomic `Consume`, `PendingInput` and secret-free storage.
  - files: `core/store_checkpoint.go`, `core/store_checkpoint_memory.go`, `core/store_checkpoint_shape_test.go`
  - scenarios: `stores.no-secrets-stored`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestCheckpointStoredShape'`

- [ ] 8.2 `core`: Test single-use `Checkpoints.Consume` under ten concurrent callers and duplicate approval delivery.
  - files: `core/store_checkpoint_consume_test.go`
  - scenarios: `stores.concurrent-consume`, `suspension.token-reuse`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestCheckpointConsume'`

9. [ ] `core`: `Journal` port + memory implementation, `CallKey`, `Fingerprint`, `Reserve`/`Complete`, TTL. — `stores.concurrent-reserve`, `stores.journal-ttl`, `stores.replay-returns-recorded-result`

- [ ] 9.1 `core`: Implement `Journal`, its memory store, `CallKey`, `Fingerprint`, `Reserve`/`Complete`, `ByFingerprint` and store-clock TTL.
  - files: `core/store_journal.go`, `core/store_journal_memory.go`, `core/store_journal_reserve_test.go`
  - scenarios: `stores.concurrent-reserve`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestJournalReserve'`

- [ ] 9.2 `core`: Test `Journal` result expiry after run completion and recorded-result replay without tool execution.
  - files: `core/store_journal_lifecycle_test.go`
  - scenarios: `stores.journal-ttl`, `stores.replay-returns-recorded-result`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestJournalLifecycle'`

10. [ ] `core`: `Runs` port + memory implementation, leases, states incl. `Suspended`/`Resuming`, `Mode`; run mailbox (`Signal`/`Drain`, `Finish` atomic with pending steers); notice outbox (`RunNotice`, `Notifier`, `Notices`/`AckNotice`, written with `Finish`/`Suspend` — `stores.notice-written-with-finish`, `streams.notice-thin-no-content`). — `stores.lease-exclusivity`, `stores.reclaim-race`, `recovery.no-double-run`, `stores.signal-cancel-cross-pod`, `stores.mailbox-full`

- [ ] 10.1 `core`: Implement `Runs` states, `Suspended`/`Resuming`, `Mode`, operation lookup and memory state transitions.
  - files: `core/store_runs.go`, `core/store_runs_test.go`
  - scenarios: `recovery.no-double-run`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRunsState'`

- [ ] 10.2 `core`: Implement `Runs` leases, `Heartbeat`, store-clock expiry and atomic `Reclaim` in memory.
  - files: `core/store_runs_leases.go`, `core/store_runs_leases_test.go`
  - scenarios: `stores.lease-exclusivity`, `stores.reclaim-race`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRunsLeases'`

- [ ] 10.3 `core`: Implement the `Signal`/`Drain` mailbox and atomic `Finish` refusal with pending steers.
  - files: `core/store_runs_mailbox.go`, `core/store_runs_mailbox_test.go`
  - scenarios: `stores.signal-cancel-cross-pod`, `stores.mailbox-full`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRunsMailbox'`

- [ ] 10.4 `core`: Implement the thin `RunNotice` outbox, `Notifier`, atomic `Finish`/`Suspend` writes and `Notices`/`AckNotice` claims.
  - files: `core/store_runs_notices.go`, `core/store_runs_notices_test.go`
  - scenarios: `stores.notice-written-with-finish`, `streams.notice-thin-no-content`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRunsNotices'`

11. [ ] `core`: `AuditLog` (chain-written, checksums, hash chain) and `EventLog` (`Seq`, `Read`, `Expire`) ports + memory implementations; `RetentionPolicy`/`RetentionSource`, `Stack.Maintain`, `SessionMeta.Hold`, `ErrSessionHeld`, `Ephemeral()`. — `stores.chain-written-only`, `stores.no-content`, `stores.hash-chain`, `stores.append-failure-is-fatal-to-the-step`, `stores.decision-trail`, `stores.retention-purge-by-tier`, `stores.retention-zero-deletes-at-finish`, `stores.hold-blocks-delete-and-purge`, `stores.purge-audited`, `redaction.erase-reports-held`, `working-state.notes-follow-working-retention`, `identity.hold-requires-scope`

- [ ] 11.1 `core`: Implement `AuditLog` and its memory store with restricted writers, checksums, hash chains and fatal append failures.
  - files: `core/store_audit.go`, `core/store_audit_test.go`
  - scenarios: `stores.chain-written-only`, `stores.no-content`, `stores.hash-chain`, `stores.append-failure-is-fatal-to-the-step`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestAuditLog'`

- [ ] 11.2 `core`: Implement the ordered `AuditLog` decision trail through `Reconstruct`.
  - files: `core/store_audit_reconstruct.go`, `core/store_audit_reconstruct_test.go`
  - scenarios: `stores.decision-trail`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestAuditReconstruct'`

- [ ] 11.3 `core`: Implement `EventLog` and its memory ring buffer with `Seq`, ordered historical/live `Read` and `Expire`.
  - files: `core/store_events.go`, `core/store_events_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./core/... -run 'TestEventLog'`

- [ ] 11.4 `core`: Implement `RetentionPolicy`/`RetentionSource`, `Stack.Maintain`, audited tier purges and the `Ephemeral()` hook without core policy defaults.
  - files: `core/store_retention.go`, `core/store_retention_test.go`
  - scenarios: `stores.retention-purge-by-tier`, `stores.retention-zero-deletes-at-finish`, `stores.purge-audited`, `working-state.notes-follow-working-retention`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStoreRetention'`

- [ ] 11.5 `core`: Enforce `SessionMeta.Hold`, `ErrSessionHeld`, `session:hold` and held-session reporting during deletion, `Maintain` and erasure.
  - files: `core/store_hold.go`, `core/store_hold_test.go`
  - scenarios: `stores.hold-blocks-delete-and-purge`, `redaction.erase-reports-held`, `identity.hold-requires-scope`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionHold'`

12. [ ] `core`: `FeedbackStore` port + memory implementation, `Stack.Feedback`, `FeedbackRecorded` (`flow.feedback-owner-checked`, `flow.feedback-idempotent-per-name`, `flow.feedback-never-in-context`, `stores.feedback-cascades-on-erase`, `streams.feedback-recorded-event`); `OutputStore` (content-addressed blobs, `InlineBlobBytes`, `Caps.Blobs` checks, URL rule; `messages.blob-stored-by-ref`, `messages.blob-content-addressed`, `messages.url-only-from-user`, `messages.blob-too-large`, `build.blob-caps`, `guards.blob-guard-input`), `NotesStore` (keyed by `NotesKey`, session scope only in M0) ports + memory implementations; schema versions + upcaster registry; `storetest` suites for all seven ports (`Schemas` included). — `working-state.notes-versioned`, `working-state.output-paging`, `stores.native-checkpoint-after-adapter-upgrade`, `stores.graph-checkpoint-incompatible`

- [ ] 12.1 `core`: Implement `FeedbackStore`, its memory store and owner-checked `Stack.Feedback` with versioned overwrite and isolated comments/corrections.
  - files: `core/store_feedback.go`, `core/store_feedback_test.go`
  - scenarios: `flow.feedback-owner-checked`, `flow.feedback-idempotent-per-name`, `flow.feedback-never-in-context`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestFeedbackStore'`

- [ ] 12.2 `core`: Implement `FeedbackStore` deletion/erasure cascades and thin `FeedbackRecorded` writes with the next `EventLog` sequence.
  - files: `core/store_feedback_lifecycle.go`, `core/store_feedback_lifecycle_test.go`
  - scenarios: `stores.feedback-cascades-on-erase`, `streams.feedback-recorded-event`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestFeedbackLifecycle'`

- [ ] 12.3 `core`: Implement `OutputStore`, its content-addressed memory blobs, `InlineBlobBytes`, persisted references and paged output access.
  - files: `core/store_outputs.go`, `core/store_outputs_test.go`, `std/outputs/read_output.go`, `std/outputs/read_output_test.go`
  - scenarios: `messages.blob-stored-by-ref`, `messages.blob-content-addressed`, `working-state.output-paging`
  - verify: `go test -short -timeout 2m ./core/... ./std/outputs/ -run 'TestOutputStore'`

- [ ] 12.4 `core`: Enforce `OutputStore` URL provenance, `Caps.Blobs` limits and `Build` checks; supply blob metadata to guards and reject MIME mismatches.
  - files: `core/store_blob_checks.go`, `core/store_blob_checks_test.go`, `std/guard/blob.go`, `std/guard/blob_test.go`
  - scenarios: `messages.url-only-from-user`, `messages.blob-too-large`, `build.blob-caps`, `guards.blob-guard-input`
  - verify: `go test -short -timeout 2m ./core/... ./std/guard/ -run 'TestBlobChecks'`

- [ ] 12.5 `core`: Implement `NotesStore` and its memory store keyed by `NotesKey`, with session scope and version-conflict semantics.
  - files: `core/store_notes.go`, `core/store_notes_test.go`
  - scenarios: `working-state.notes-versioned`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestNotesStore'`

- [ ] 12.6 `core`: Implement stored `SchemaVersion` fields, pure read-time upcasters and checkpoint version compatibility without in-place rewrites.
  - files: `core/store_schema.go`, `core/store_schema_test.go`, `core/checkpoint_compatibility.go`, `core/checkpoint_compatibility_test.go`
  - scenarios: `stores.native-checkpoint-after-adapter-upgrade`, `stores.graph-checkpoint-incompatible`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStoreSchemas'`

- [ ] 12.7 `testkit/storetest`: Implement `SessionLog` conformance through injected port factories; bind the memory store from an external core test, not from the suite.
  - files: `testkit/storetest/session_log.go`, `testkit/storetest/session_log_test.go`, `core/storetest_session_log_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestSessionLog'`

- [ ] 12.8 `testkit/storetest`: Implement `Checkpoints` conformance through injected port factories; bind the memory store from an external core test, not from the suite.
  - files: `testkit/storetest/checkpoints.go`, `testkit/storetest/checkpoints_test.go`, `core/storetest_checkpoints_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestCheckpoints'`

- [ ] 12.9 `testkit/storetest`: Implement `Journal` conformance through injected port factories; bind the memory store from an external core test, not from the suite.
  - files: `testkit/storetest/journal.go`, `testkit/storetest/journal_test.go`, `core/storetest_journal_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestJournal'`

- [ ] 12.10 `testkit/storetest`: Implement `Runs` conformance through injected port factories and clocks; bind the memory store from an external core test, not from the suite.
  - files: `testkit/storetest/runs.go`, `testkit/storetest/runs_test.go`, `core/storetest_runs_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestRuns'`

- [ ] 12.11 `testkit/storetest`: Implement `AuditLog` conformance through injected port factories; bind the memory store from an external core test, not from the suite.
  - files: `testkit/storetest/audit_log.go`, `testkit/storetest/audit_log_test.go`, `core/storetest_audit_log_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestAuditLog'`

- [ ] 12.12 `testkit/storetest`: Implement `EventLog` conformance through injected port factories; bind the memory store from an external core test, not from the suite.
  - files: `testkit/storetest/event_log.go`, `testkit/storetest/event_log_test.go`, `core/storetest_event_log_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestEventLog'`

- [ ] 12.13 `testkit/storetest`: Implement `Schemas` conformance with recorded released-version fixtures and injected readers; bind memory readers from an external core test.
  - files: `testkit/storetest/schemas.go`, `testkit/storetest/schemas_test.go`, `testkit/storetest/schema_fixtures_test.go`, `core/storetest_schemas_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestSchemas'`

## C. Chains, guards, gate (governance before capability)

13. [ ] `core`: chain-as-data (`Step`, `StepKind` incl. `KindHedge`, `ToolChain`, `ModelChain`), canonical-order validation (hedge outside fallback, inside router), `StepError`, `PromptSet` type, `Explain`/`Explanation`. — `chains.empty-chains`, `chains.step-named-failure`, `chains.prompt-strings-accounted-for`, `chains.preset-is-copyable`, `chains.cache-replace-is-free`

- [ ] 13.1 `core`: Define `Step`, `StepKind` including `KindHedge`, `ToolChain`, `ModelChain`, canonical-order validation and `StepError`; preserve empty chains and free cache replacement.
  - files: `core/chain.go`, `core/chain_test.go`
  - scenarios: `chains.empty-chains`, `chains.step-named-failure`, `chains.cache-replace-is-free`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestChain'`

- [ ] 13.2 `core`: Define `PromptSet`, `Explain` and `Explanation`; account for prompt fields and validate copied preset chains without core defaults.
  - files: `core/explain.go`, `core/explain_test.go`
  - scenarios: `chains.prompt-strings-accounted-for`, `chains.preset-is-copyable`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestExplanation'`

14. [ ] `std`: permission gate skeleton — `ApprovalPolicy` per risk tier at `Resume` (`permission.self-approval-refused-high-risk`, `permission.approve-scope-required-medium`, `permission.ineligible-keeps-token`, `permission.quorum-two-approvers`, `permission.escalation-targets`, `permission.approval-audit-eligibility`, `permission.grant-inherits-policy`), scope check, `Deny` rules, `Ask` → `HumanApproval` with `ApprovalRequest` (incl. `ArgOrigins`, `DiffFromLast`), session grants, expiry, `MaxPendingApprovals`; `TaintHook` slot wired but empty. — `permission.grant-removes-the-repeat-ask`, `permission.fingerprint-change-misses-the-grant`, `permission.grant-does-not-cross-sessions-or-principals`, `permission.rich-request`, `permission.expiry-default`, `permission.queue-flood`, `chains.scope-before-decider`, `chains.denied-call-not-journaled`, `decider.failure-defaults-closed`

- [ ] 14.1 `core`: Implement the permission gate skeleton, scope-first hard `Deny`, closed decider failures and `Ask` → `HumanApproval`; wire the empty `TaintHook` slot.
  - files: `core/permission.go`, `core/permission_test.go`
  - scenarios: `chains.scope-before-decider`, `chains.denied-call-not-journaled`, `decider.failure-defaults-closed`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestPermissionGate'`

- [ ] 14.2 `core`: Define `ApprovalPolicy`, `ApprovalRequest`, `Eligibility` and `ApprovalPolicy.MaxPending`; preserve tokens on ineligible approval and carry `ArgOrigins`, `DiffFromLast` and eligibility audit data.
  - files: `core/approval.go`, `core/approval_test.go`, `core/limits.go`, `core/limits_test.go`
  - scenarios: `permission.ineligible-keeps-token`, `permission.rich-request`, `permission.approval-audit-eligibility`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestApprovalContract'`

- [ ] 14.3 `std/permission`: Supply risk-tier `ApprovalPolicy` defaults for `Resume`, separate high-risk originators, require medium-risk approval scope and enforce distinct-approver quorum.
  - files: `std/permission/policy.go`, `std/permission/policy_test.go`
  - scenarios: `permission.self-approval-refused-high-risk`, `permission.approve-scope-required-medium`, `permission.quorum-two-approvers`
  - verify: `go test -short -timeout 2m ./std/permission/ -run 'TestApprovalPolicy'`

- [ ] 14.4 `std/permission`: Implement policy-checked `ApproveScope` and session `Grant` matching by tool, fingerprint and subject.
  - files: `std/permission/grant.go`, `std/permission/grant_test.go`
  - scenarios: `permission.grant-inherits-policy`, `permission.grant-removes-the-repeat-ask`, `permission.fingerprint-change-misses-the-grant`, `permission.grant-does-not-cross-sessions-or-principals`
  - verify: `go test -short -timeout 2m ./std/permission/ -run 'TestSessionGrant'`

- [ ] 14.5 `std/permission`: Implement `RejectOnExpiry`, `EscalateOnExpiry` targets and `Waker` scheduling; enforce `ApprovalPolicy.MaxPending` per subject and tenant.
  - files: `std/permission/expiry.go`, `std/permission/expiry_test.go`, `std/permission/pending.go`, `std/permission/pending_test.go`
  - scenarios: `permission.expiry-default`, `permission.escalation-targets`, `permission.queue-flood`
  - verify: `go test -short -timeout 2m ./std/permission/ -run 'TestApprovalQueue'`

15. [ ] `std`: journal step with fingerprint pinning, cancel shield (`context.WithoutCancel` for `SideEffect`), read-back, uncertainty surfacing, `Verify` reconciliation. — `stores.same-transaction-journal`, `stores.late-commit`, `stores.read-back-offered`, `stores.uncertainty-surfaced`, `stores.fingerprint-after-compaction`, `stores.crash-window`, `tools.verify-reconciles-unknown`, `tools.verify-read-only`, `tools.verify-error`, `tools.verify-on-recover`, `streams.disconnect-during-side-effect`, `chains.read-only-tools-pay-nothing`

- [ ] 15.1 `std`: Implement the journal step with canonical fingerprinting, intent-key pinning and crash-window replay; reuse row 9's core `Fingerprint` and `CallKey`.
  - files: `std/journal.go`, `std/journal_test.go`
  - scenarios: `stores.late-commit`, `stores.fingerprint-after-compaction`, `stores.crash-window`
  - verify: `go test -short -timeout 2m ./std/ -run 'TestJournalIntent'`

- [ ] 15.2 `std`: Apply `context.WithoutCancel` only to `SideEffect`, persist results before cancellation and exempt `ReadOnly` tools from journal and shield work.
  - files: `std/shield.go`, `std/shield_test.go`
  - scenarios: `streams.disconnect-during-side-effect`, `chains.read-only-tools-pay-nothing`
  - verify: `go test -short -timeout 2m ./std/ -run 'TestCancelShield'`

- [ ] 15.3 `std`: Reconcile `Unknown` through read-only `Verify` after execution and on `Resume`/`Recover`; journal `/verify` calls without re-executing effects.
  - files: `std/verify.go`, `std/verify_test.go`
  - scenarios: `tools.verify-reconciles-unknown`, `tools.verify-read-only`, `tools.verify-error`, `tools.verify-on-recover`
  - verify: `go test -short -timeout 2m ./std/ -run 'TestVerifyReconciliation'`

- [ ] 15.4 `std`: Offer `ReadBack` after `Unknown` and surface unresolved `CallKey` entries through `UncertainOutcomeError` and `Done.Uncertain`.
  - files: `std/uncertainty.go`, `std/uncertainty_test.go`
  - scenarios: `stores.read-back-offered`, `stores.uncertainty-surfaced`
  - verify: `go test -short -timeout 2m ./std/ -run 'TestUncertainty'`

- note: `stores.same-transaction-journal` needs `adapter/postgres` and is deferred to M1 (`deferred_to` in `openspec/scenarios.json`).

16. [ ] `std`: guards — `GuardInput`, stages, rules-based input/context/description guards, `Buffered`/`Windowed` output, `Fallback`, fencing by `Origin`. — `guards.input-injection-blocked`, `guards.indirect-injection`, `guards.windowed-output`, `guards.intermediate-turns-unguarded`, `guards.notes-poisoning-blocked`, `guards.provider-output-fenced`, `guards.origin-not-caller-settable`, `tools.poisoned-description`, `decider.observable`

- [ ] 16.1 `std/guard`: Implement rules-based input, tool-result and description guards using core `GuardInput` and stages; record observable decider decisions.
  - files: `std/guard/rules.go`, `std/guard/rules_test.go`, `std/guard/description.go`, `std/guard/description_test.go`
  - scenarios: `guards.input-injection-blocked`, `guards.indirect-injection`, `tools.poisoned-description`, `decider.observable`
  - verify: `go test -short -timeout 2m ./std/guard/ -run 'TestGuardRules'`

- [ ] 16.2 `std/guard`: Implement context guards and fencing by chain-assigned `Origin`; reject poisoned notes and fence provider output with `PromptSet` fields.
  - files: `std/guard/context.go`, `std/guard/context_test.go`, `std/guard/origin.go`, `std/guard/origin_test.go`
  - scenarios: `guards.notes-poisoning-blocked`, `guards.provider-output-fenced`, `guards.origin-not-caller-settable`
  - verify: `go test -short -timeout 2m ./std/guard/ -run 'TestContextProvenance'`

- [ ] 16.3 `std/guard`: Implement `Buffered` and `Windowed` user-facing output guards with `Fallback`; leave intermediate tool-call turns unguarded.
  - files: `std/guard/output.go`, `std/guard/output_test.go`
  - scenarios: `guards.windowed-output`, `guards.intermediate-turns-unguarded`
  - verify: `go test -short -timeout 2m ./std/guard/ -run 'TestOutputGuard'`

17. [ ] `std`: tool policy — `Trusted`/`Untrusted`, `MaxEffect`, pinned manifest with hash drift, `ToolFilter` (narrow-only, deterministic), `Deferred` + `search_tools`. — `tools.rug-pull`, `tools.untrusted-effect-cap`, `tools.tool-filter-per-turn`, `tools.not-assembled-until-discovered`, `tools.activation-persists-across-resume`, `tools.governed-while-deferred`, `messages.deterministic-tool-filter-on-replay`

- [ ] 17.1 `core`: Declare `ToolPolicy`, `Trusted`, `Untrusted` and `MaxEffect`; apply the configured effect cap before gate evaluation.
  - files: `core/tool_policy.go`, `core/tool_policy_test.go`
  - scenarios: `tools.untrusted-effect-cap`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolPolicy'`

- [ ] 17.2 `std`: Implement pinned tool manifests, hash-drift checks and narrow-only per-turn `ToolFilter`; enforce deterministic filtered sets on `Replay`.
  - files: `std/manifest.go`, `std/manifest_test.go`, `std/tool_filter.go`, `std/tool_filter_test.go`
  - scenarios: `tools.rug-pull`, `tools.tool-filter-per-turn`, `messages.deterministic-tool-filter-on-replay`
  - verify: `go test -short -timeout 2m ./std/ -run 'TestToolManifestAndFilter'`

- [ ] 17.3 `std/toolsearch`: Implement `Deferred` discovery through `search_tools`, persist activation across resume and preserve scope checks after discovery.
  - files: `std/toolsearch/search.go`, `std/toolsearch/search_test.go`, `std/toolsearch/activation.go`, `std/toolsearch/activation_test.go`
  - scenarios: `tools.not-assembled-until-discovered`, `tools.activation-persists-across-resume`, `tools.governed-while-deferred`
  - verify: `go test -short -timeout 2m ./std/toolsearch/ -run 'TestDeferredTools'`

## D. Model side

18. [ ] `core`: `ModelProfile` (incl. `Keys`), `ProviderKeySource`/`ProviderCredential`/`ErrNoProviderKey`, `Build` key validation (`model.tenant-key-selected`, `model.tenant-key-missing-fails-closed`, `model.no-fallback-on-auth-error`, `model.key-validated-at-build`, `identity.tenant-for-key-from-ctx`), `Caps`, `Pricing`, error class normalisation, `LatencyClass`. `std`: `retry.Exponential`/`RetryAfter`, `std/limit` local limiter, circuit breaker, `Fallback` before first chunk, per-endpoint wrapping. — `model.429-fails-over-without-retry`, `model.5xx-retries-then-fails-over`, `model.breaker-opens`, `model.early-break-releases`, `model.cancel-returns-promptly`, `model.first-chunk-timeout-transient`, `model.idle-timeout-permanent`, `model.partial-terminal-on-replay`, `chains.no-retry-after-first-chunk`, `chains.fallback-charged`, `chains.per-endpoint-limits`

- [ ] 18.1 `std/keys`: Add env- and map-backed `ProviderKeySource`, `ProviderCredential`, `ErrNoProviderKey`; select `ModelProfile.Keys` from `PrincipalFrom(ctx)` and refuse auth fallback.
  - files: `std/keys/keys.go`, `std/keys/keys_test.go`
  - scenarios: `model.tenant-key-selected`, `model.tenant-key-missing-fails-closed`, `model.no-fallback-on-auth-error`, `identity.tenant-for-key-from-ctx`
  - verify: `go test -short -timeout 2m ./std/keys/ -run 'TestProviderKeys'`

- [ ] 18.2 `core`: Validate platform credentials through `ProviderKeyValidator` during `Build`.
  - files: `core/build_keys.go`, `core/build_keys_test.go`
  - scenarios: `model.key-validated-at-build`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestBuildProviderKeys'`

- [ ] 18.3 `std/retry`: Add `retry.Exponential` and `RetryAfter`; retry transient failures before the first chunk only, then expose exhaustion to `Fallback`.
  - files: `std/retry/retry.go`, `std/retry/retry_test.go`
  - scenarios: `model.5xx-retries-then-fails-over`, `chains.no-retry-after-first-chunk`
  - verify: `go test -short -timeout 2m ./std/retry/ -run 'TestRetryPolicy'`

- [ ] 18.4 `std/limit`: Add the local limiter and `MaxInFlight` bulkhead with independent per-endpoint wrapping.
  - files: `std/limit/model.go`, `std/limit/model_test.go`
  - scenarios: `chains.per-endpoint-limits`
  - verify: `go test -short -timeout 2m ./std/limit/ -run 'TestEndpointLimits'`

- [ ] 18.5 `std/route`: Add circuit breaker, static/by-`LatencyClass` routing and `Fallback` before the first chunk; charge the successful endpoint's usage.
  - files: `std/route/route.go`, `std/route/breaker.go`, `std/route/fallback.go`, `std/route/route_test.go`
  - scenarios: `model.breaker-opens`, `model.429-fails-over-without-retry`, `chains.fallback-charged`
  - verify: `go test -short -timeout 2m ./std/route/ -run 'TestEndpointRouting'`

- [ ] 18.6 `core`: Add `ModelProfile`, `Caps`, `Pricing`, `LatencyClass` and error class normalisation; enforce model iterator release, cancellation and first-chunk/idle timeout semantics.
  - files: `core/model_profile.go`, `core/model_stream.go`, `core/model_profile_test.go`, `core/model_stream_test.go`
  - scenarios: `model.early-break-releases`, `model.cancel-returns-promptly`, `model.first-chunk-timeout-transient`, `model.idle-timeout-permanent`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestModelStream'`

- [ ] 18.7 `core`: Exclude terminal partial assistant messages with `FinishError` from provider history on replay and continuation.
  - files: `core/model_history.go`, `core/model_history_test.go`
  - scenarios: `model.partial-terminal-on-replay`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestModelTerminalHistory'`

19. [ ] `std`: `StablePrefix` assembler, slots, `ContextProvider`, `CacheBreak` rules, `Truncate` projection (persisted compaction is M2), `std/tokens.Heuristic` + `ContextBudget`. — `model.budget-reserves-output`, `model.heuristic-deterministic`, `model.token-counter-optional`, `assembly.prefix-stability`, `assembly.tool-order`, `assembly.truncate-policy`, `model.context-re-fit-on-fallback`

- [ ] 19.1 `std`: Add `StablePrefix` assembly, slots, `ContextProvider` integration and `CacheBreak` rules with stable tool ordering.
  - files: `std/assembly.go`, `std/assembly_test.go`
  - scenarios: `assembly.prefix-stability`, `assembly.tool-order`
  - verify: `go test -short -timeout 2m ./std/ -run 'TestStablePrefix'`

- [ ] 19.2 `core`: Add `ContextBudget` from `openspec/specs/model/spec.md` §Contract/6.5; reserve output and margin, and use optional `TokenCounter` only at the compaction decision.
  - files: `core/context_budget.go`, `core/context_budget_test.go`
  - scenarios: `model.budget-reserves-output`, `model.token-counter-optional`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestContextBudget'`

- [ ] 19.3 `std/tokens`: Add deterministic `Heuristic` from `openspec/specs/model/spec.md` §Contract/Token budget.
  - files: `std/tokens/heuristic.go`, `std/tokens/heuristic_test.go`
  - scenarios: `model.heuristic-deterministic`
  - verify: `go test -short -timeout 2m ./std/tokens/ -run 'TestHeuristic'`

- [ ] 19.4 `std/context`: Add `Truncate` projection from `openspec/specs/context/spec.md` §Contract/Two stages with different persistence; preserve whole turns and prefix, and re-fit each fallback profile without persisted compaction.
  - files: `std/context/truncate.go`, `std/context/truncate_test.go`
  - scenarios: `assembly.truncate-policy`, `model.context-re-fit-on-fallback`
  - verify: `go test -short -timeout 2m ./std/context/ -run 'TestTruncate'`

20. [ ] `std/structured`: `Partial[Out]` (`structured-output.result-delta-partial`, `structured-output.partial-never-validated`), `ToolSchema`, `ValidateRepair`, `ReasonFirst`, app-side validation, strict schema derivation, refusal-as-JSON, truncated-args handling. — `structured-output.validate-and-repair`, `structured-output.bounds-validated-after-constrained-decoding`, `structured-output.refusal-as-json`, `structured-output.truncated-tool-args`, `structured-output.reason-first`, `structured-output.strict-schema`

- [ ] 20.1 `std/structured`: Add `Partial[Out]` (validated) and `PartialView[Out]` returning `PartialValue` (deep-partial, never validated) for accumulated `ResultDelta` text; prevent partial values from validation, storage or `Done.Result`.
  - files: `std/structured/partial.go`, `std/structured/partial_test.go`
  - scenarios: `structured-output.result-delta-partial`, `structured-output.partial-never-validated`
  - verify: `go test -short -timeout 2m ./std/structured/ -run 'TestPartial'`

- [ ] 20.2 `std/structured`: Add `ValidateRepair`, app-side bounds validation and refusal-as-JSON classification with bounded repair turns.
  - files: `std/structured/validate.go`, `std/structured/repair.go`, `std/structured/validate_test.go`
  - scenarios: `structured-output.validate-and-repair`, `structured-output.bounds-validated-after-constrained-decoding`, `structured-output.refusal-as-json`
  - verify: `go test -short -timeout 2m ./std/structured/ -run 'TestValidateRepair'`

- [ ] 20.3 `std/structured`: Add `ToolSchema`, strict schema derivation and `ReasonFirst` request configuration with `Explain` reporting.
  - files: `std/structured/schema.go`, `std/structured/reason_first.go`, `std/structured/schema_test.go`
  - scenarios: `structured-output.strict-schema`, `structured-output.reason-first`
  - verify: `go test -short -timeout 2m ./std/structured/ -run 'TestStructuredStrategy'`

- [ ] 20.4 `std/structured`: Reject truncated tool arguments before execution; send a truncation result and retry once with larger `MaxTokens`.
  - files: `std/structured/truncated.go`, `std/structured/truncated_test.go`
  - scenarios: `structured-output.truncated-tool-args`
  - verify: `go test -short -timeout 2m ./std/structured/ -run 'TestTruncatedToolArgs'`

21. [ ] `core`: `Build` — option set, strategy resolution (flow > profile > caps), impossible-combination rejection, provider-tool capability check (`build.provider-tool-unsupported`), resolved-matrix log, `ReleaseManifest` + `ID`, fidelity gate. — `build.impossible-combination`, `build.resolved-matrix`, `model.incompatible-fallback-rejected-at-build`, `messages.fidelity-gate-at-build`, `messages.reasoning-dropped-across-providers`

- [ ] 21.1 `core`: Add `Build` options including `SequentialTools()` and `MaxParallelTools`; resolve flow > profile > caps and reject impossible combinations, unsupported provider tools and incompatible fallback profiles.
  - files: `core/build.go`, `core/build_options.go`, `core/build_test.go`
  - scenarios: `build.impossible-combination`, `build.provider-tool-unsupported`, `model.incompatible-fallback-rejected-at-build`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestBuildStrategies'`

- [ ] 21.2 `core`: Add the `Build` fidelity gate and cross-provider reasoning projection without editing opaque reasoning blocks.
  - files: `core/build_fidelity.go`, `core/fidelity_projection.go`, `core/build_fidelity_test.go`
  - scenarios: `messages.fidelity-gate-at-build`, `messages.reasoning-dropped-across-providers`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestBuildFidelity'`

- [ ] 21.3 `core`: Compute `ReleaseManifest` and `ID`; expose the manifest and log the resolved flow × profile × strategy matrix once at startup. **GATE**
  - files: `core/release_manifest.go`, `core/build_matrix.go`, `core/build_matrix_test.go`
  - scenarios: `build.resolved-matrix`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestBuildResolvedMatrix' && task spec:types && task spec:coverage && task lint && task test`

## E. Runtime, flow, suspension

22. [ ] `core`: `Stepper`, `Runtime`, `State`, `Drive`/`DriveResume`; native runtime at effect granularity with the tool batch protocol (`MaxParallelTools`, `SequentialTools()`). — `runtime.batch-gate-first`, `runtime.batch-limit-before-execute`, `runtime.readonly-parallel`, `runtime.side-effects-sequential`, `runtime.batch-ask-after-allowed`, `runtime.batch-one-result-per-call`, `runtime.parallel-tools-cap`, `runtime.sequential-tools-hint`, `runtime.plain-answer`, `runtime.tool-round-trip`, `runtime.parallel-calls-ordering`, `runtime.max-turns`, `runtime.cancellation`, `runtime.message-round-trip`, `runtime.suspend-order`, `runtime.append-before-tool`, `runtime.done-after-finish`

- [ ] 22.1 `core`: Implement `Stepper`, `Runtime`, illustrative `State`, and `Drive`/`DriveResume` at effect granularity; preserve `Message` blocks and history versions.
  - files: `core/runtime.go`, `core/drive.go`, `core/runtime_test.go`
  - scenarios: `runtime.plain-answer`, `runtime.message-round-trip`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRuntimeState'`

- [ ] 22.2 `core`: Implement the tool batch protocol: gate and reserve limits before execution, process `Ask` after allowed calls, and produce one result per call.
  - files: `core/runtime_batch.go`, `core/runtime_batch_test.go`
  - scenarios: `runtime.batch-gate-first`, `runtime.batch-limit-before-execute`, `runtime.batch-ask-after-allowed`, `runtime.batch-one-result-per-call`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRuntimeBatchProtocol'`

- [ ] 22.3 `core`: Execute `ReadOnly` calls concurrently and side effects sequentially; consume row 21's `MaxParallelTools` and `SequentialTools()` options.
  - files: `core/runtime_schedule.go`, `core/runtime_schedule_test.go`
  - scenarios: `runtime.readonly-parallel`, `runtime.side-effects-sequential`, `runtime.parallel-tools-cap`, `runtime.sequential-tools-hint`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRuntimeScheduling'`

- [ ] 22.4 `core`: Implement the per-turn `Drive` loop, ordered tool round trips, `MaxTurns`, and cancellation without further component calls.
  - files: `core/drive_turn.go`, `core/drive_turn_test.go`
  - scenarios: `runtime.tool-round-trip`, `runtime.parallel-calls-ordering`, `runtime.max-turns`, `runtime.cancellation`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRuntimeTurns'`

- [ ] 22.5 `core`: Enforce `Drive` lifecycle ordering: append before the gate, checkpoint before suspension, and `Runs.Finish` before terminal `Done`.
  - files: `core/drive_lifecycle.go`, `core/drive_lifecycle_test.go`
  - scenarios: `runtime.suspend-order`, `runtime.append-before-tool`, `runtime.done-after-finish`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRuntimeLifecycle'`

23. [ ] `core`: `Flow[In, Out]`, `FlowFunc`, `Conversation` (incl. `Continue`, `Steer` + runtime drain at safe points — `flow.steer-applied-at-boundary`, `flow.steer-preserves-adjacency`, `flow.steer-after-final-reply-runs-turn`, `flow.steer-no-active-run`, `identity.steer-root-only`; takeover — `SessionControl`, `OriginOperator`, `TakeOver`/`HandBack`/`OperatorSend`, `StopHandedOff`: `flow.takeover-pauses-agent`, `flow.operator-send-origin`, `flow.send-during-human-control-no-run`, `permission.takeover-requires-scope`, `stores.control-in-session-index`; `flow.continue-without-input`, `flow.regenerate-is-fork-and-continue`), `AbortError`, `RunLimits` (turns, tool calls, cost, wall clock, soft ratio); `std/flow.Extract` and `Classify`; typed `Done.Result`. — `flow.plain-invoke`, `flow.not-suspendable`, `flow.extract-recipe`, `flow.classify-recipe`, `flow.typed-result-on-conversation`, `flow.cancel-other-request`, `flow.send-during-active-run`, `flow.idempotent-send`, `limits.hard-cost-abort`, `limits.wall-clock`, `limits.unbounded-rejected`, `limits.cost-accumulates`

- [ ] 23.1 `core`: Implement `Flow[In, Out]`, `FlowFunc`, `AbortError`, and typed `Done.Result` with matching canonical JSON in the final assistant message.
  - files: `core/flow.go`, `core/flow_test.go`
  - scenarios: `flow.plain-invoke`, `flow.not-suspendable`, `flow.typed-result-on-conversation`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestFlowContract'`

- [ ] 23.2 `core`: Implement `Conversation.Send` and `Cancel`, live-run refusal, idempotent reattachment, and `StopHandedOff` without starting a run under human control.
  - files: `core/conversation.go`, `core/conversation_test.go`
  - scenarios: `flow.cancel-other-request`, `flow.send-during-active-run`, `flow.idempotent-send`, `flow.send-during-human-control-no-run`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestConversationSend'`

- [ ] 23.3 `core`: Implement `Conversation.Continue` without input and regeneration through `Stores.ForkSession` followed by `Continue`.
  - files: `core/conversation_continue.go`, `core/conversation_continue_test.go`
  - scenarios: `flow.continue-without-input`, `flow.regenerate-is-fork-and-continue`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestConversationContinue'`

- [ ] 23.4 `core`: Implement `Conversation.Steer` and runtime drain at safe points; preserve tool adjacency and run another turn when `Finish` finds pending steers.
  - files: `core/conversation_steer.go`, `core/drive_mailbox.go`, `core/conversation_steer_test.go`
  - scenarios: `flow.steer-applied-at-boundary`, `flow.steer-preserves-adjacency`, `flow.steer-after-final-reply-runs-turn`, `flow.steer-no-active-run`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestConversationSteer'`

- [ ] 23.5 `core`: Enforce root-only `Steer` ownership and takeover scope checks; expose `SessionControl` filtering through the session index.
  - files: `core/session_control.go`, `core/session_control_test.go`
  - scenarios: `identity.steer-root-only`, `permission.takeover-requires-scope`, `stores.control-in-session-index`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionControl'`

- [ ] 23.6 `core`: Implement `TakeOver`/`HandBack`/`OperatorSend`, safe-point takeover, pending-token expiry, audited control transitions, and `OriginOperator` messages without model calls.
  - files: `core/session_takeover.go`, `core/session_takeover_test.go`
  - scenarios: `flow.takeover-pauses-agent`, `flow.operator-send-origin`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionTakeover'`

- [ ] 23.7 `core`: Implement `RunLimits` for turns, tool calls, cost, wall clock, and soft ratio; reject unbounded runs and accumulate tree cost.
  - files: `core/run_limits.go`, `core/run_limits_test.go`
  - scenarios: `limits.hard-cost-abort`, `limits.wall-clock`, `limits.unbounded-rejected`, `limits.cost-accumulates`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRunLimits'`

- [ ] 23.8 `std/flow`: Implement `Extract` and `Classify` as governed single-call recipes with typed validation and no tools.
  - files: `std/flow/extract.go`, `std/flow/extract_test.go`, `std/flow/classify.go`, `std/flow/classify_test.go`
  - scenarios: `flow.extract-recipe`, `flow.classify-recipe`
  - verify: `go test -short -timeout 2m ./std/flow/ -run 'TestFlowRecipes'`

24. [ ] `core`: suspension — `SuspendError`, reasons incl. `AwaitingInput`, `ResumeInput` constructors, `Waker` port, `Replay` resume, token mismatch/consumed/expired, checkpoint incompatibility. — `suspension.approve-on-another-pod`, `suspension.reject-and-edit`, `suspension.async-tool`, `suspension.scheduled`, `suspension.mismatch`, `identity.credentials-on-resume`, `identity.resume-inside-run-refused`, `identity.approver-from-transport-only`

- [ ] 24.1 `core`: implement `SuspendError`, suspension reasons including `AwaitingInput`, `ResumeInput` constructors and approval `Replay`; restore originator credentials before tool execution.
  - files: `core/suspension.go`, `core/suspension_test.go`, `core/resume.go`, `core/resume_test.go`
  - scenarios: `suspension.approve-on-another-pod`, `suspension.reject-and-edit`, `identity.credentials-on-resume`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestApprovalResume'`

- [ ] 24.2 `core`: implement `AwaitingTool` delivery through `Replay` and `Scheduled` suspension through the `Waker` port.
  - files: `core/suspension_delivery.go`, `core/suspension_delivery_test.go`
  - scenarios: `suspension.async-tool`, `suspension.scheduled`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSuspensionDelivery'`

- [ ] 24.3 `core`: enforce token mismatch, consumption, expiry and checkpoint compatibility before execution; refuse resume inside a run and derive `ResumeInput.Approver` only from transport identity.
  - files: `core/resume_validation.go`, `core/resume_validation_test.go`
  - scenarios: `suspension.mismatch`, `identity.resume-inside-run-refused`, `identity.approver-from-transport-only`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestResumeValidation'`

25. [ ] `core`: run trees (`RootRunID`, `ParentRunID`, `Depth`), tree budget, `FlowAsTool` typed wrapper (full sub-flow contract is M3). — `identity.tree-budget`, `identity.nested-spans-and-recovery`

- [ ] 25.1 `core`: implement minimal typed `FlowAsTool`, `RootRunID`/`ParentRunID`/`Depth`, hub `MaxCost` accounting, `Checkpoint.Child`, tree recovery and spans under root `invoke_agent`; exclude nested suspension, `Collect`/`FailFast`, parallel children, per-child session history and sub-flow limits beyond the tree budget (`subflows`, M3).
  - files: `core/flow_as_tool.go`, `core/flow_as_tool_test.go`, `core/run_tree.go`, `core/run_tree_test.go`
  - scenarios: `identity.tree-budget`, `identity.nested-spans-and-recovery`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRunTree'`

26. [ ] `core`: streams — attached/detached runs, `ToolArgsDelta`/`ResultDelta` previews (`model.tool-args-delta-kind`, `streams.tool-args-delta-preview-only`, `streams.tool-args-delta-coalesced-in-log`, `tools.args-validated-at-completion`), heartbeat goroutine, stream buffer, consumer-stall preemption/detach, `EventLog`-backed reattach, `SharedState`/`SetSharedState`, `StateChanged`, `ReasoningDelta` gating. — `streams.disconnect-during-model-call`, `streams.heartbeat-independent-of-consumer`, `streams.slow-consumer-no-idle-retry`, `streams.consumer-stall-preempts`, `streams.consumer-stall-detaches-with-log`, `streams.stream-buffer-bound`, `streams.detached-reconnect`, `streams.detached-requires-log`, `streams.error-tuple-terminal`, `streams.preflight-error-sole-tuple`, `streams.collect-helpers`, `working-state.shared-state-restored`, `working-state.shared-state-versioned`, `working-state.notes-survive-compaction` (against `Truncate`; compaction proper in M2)

- [ ] 26.1 `core`: implement attached cancellation, detached lifetime and `EventLog`-backed `Attach` with ordered catch-up and live delivery; reject detached runs without an `EventLog`.
  - files: `core/stream_lifetime.go`, `core/stream_lifetime_test.go`, `core/attach.go`, `core/attach_test.go`
  - scenarios: `streams.disconnect-during-model-call`, `streams.detached-reconnect`, `streams.detached-requires-log`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStreamLifetime'`

- [ ] 26.2 `core`: implement terminal error tuples and sole preflight failures for stream seams; implement `Collect`, `Last` and `Drain`.
  - files: `core/stream_errors.go`, `core/stream_errors_test.go`, `core/stream_helpers.go`, `core/stream_helpers_test.go`
  - scenarios: `streams.error-tuple-terminal`, `streams.preflight-error-sole-tuple`, `streams.collect-helpers`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStreamErrorProtocol'`

- [ ] 26.3 `core`: map `DeltaToolArgs` to preview-only `ToolArgsDelta`, emit `ResultDelta` previews, coalesce detached deltas in `EventLog` and validate complete arguments before execution.
  - files: `core/stream_previews.go`, `core/stream_previews_test.go`, `core/tool_args_completion.go`, `core/tool_args_completion_test.go`
  - scenarios: `model.tool-args-delta-kind`, `streams.tool-args-delta-preview-only`, `streams.tool-args-delta-coalesced-in-log`, `tools.args-validated-at-completion`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStreamPreviews'`

- [ ] 26.4 `core`: implement run-owned lease heartbeat and bounded `StreamBuffer`; measure idle timeout on provider reads and block full buffers without loss, reordering or retry.
  - files: `core/stream_heartbeat.go`, `core/stream_heartbeat_test.go`, `core/stream_buffer.go`, `core/stream_buffer_test.go`
  - scenarios: `streams.heartbeat-independent-of-consumer`, `streams.slow-consumer-no-idle-retry`, `streams.stream-buffer-bound`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStreamProtections'`

- [ ] 26.5 `core`: enforce `ConsumerStall` through safe-point `Preempted` suspension and `Continue()`; implement `OnStall(Detach)` with `EventLog` reattachment.
  - files: `core/stream_stall.go`, `core/stream_stall_test.go`
  - scenarios: `streams.consumer-stall-preempts`, `streams.consumer-stall-detaches-with-log`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestConsumerStall'`

- [ ] 26.6 `core`: implement typed `SharedState`/`SetSharedState`, session metadata persistence and resume restoration; hold `StateChanged`/`PatchOp` shapes and emit supplied patches without core diffing.
  - files: `core/shared_state.go`, `core/shared_state_test.go`
  - scenarios: `working-state.shared-state-restored`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSharedStateRestore'`

- [ ] 26.7 `std/state`: compute RFC 6902 JSON Patch for `SetSharedState`; integrate monotonically increasing versions and one core-emitted `StateChanged` per update.
  - files: `std/state/state.go`, `std/state/state_test.go`
  - scenarios: `working-state.shared-state-versioned`
  - verify: `go test -short -timeout 2m ./std/state/ -run 'TestSharedStateVersioned'`

- [ ] 26.8 `std/context`: verify notes remain available through the `SlotSession` provider after `Truncate` hides earlier messages; exclude persisted compaction until M2.
  - files: `std/context/truncate_notes_test.go`
  - scenarios: `working-state.notes-survive-compaction`
  - verify: `go test -short -timeout 2m ./std/context/ -run 'TestNotesSurviveTruncate'`

- [ ] 26.9 `core`: gate `ReasoningDelta` on `Caps.ReasoningVisible`; retain opaque reasoning at message completion without adding deferred AG-UI scenarios.
  - files: `core/stream_reasoning.go`, `core/stream_reasoning_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./core/... -run 'TestReasoningDeltaGating'`

27. [ ] `core`: `Recover`, `Inspect`, stale-lease reaper (`Stale(staleAfter)` by store clock), `Resuming` re-drive; `memory.WithNow`; monotonic wall-clock budget. — `recovery.pod-dies-mid-turn`, `recovery.pod-dies-inside-a-side-effect`, `recovery.headless-recovery-impossible`, `tools.inspect-from-another-pod`, `stores.stale-by-store-clock`, `stores.skewed-caller-cannot-reclaim`, `stores.checkpoint-expiry-store-clock`, `stores.memstore-clock-jump`, `limits.wall-clock-monotonic`

- [ ] 27.1 `core`: implement idempotent `Recover`, stale-lease reclaim, journal `Replay`, persisted `Resuming` input re-drive and failed headless recovery.
  - files: `core/recover.go`, `core/recover_test.go`
  - scenarios: `recovery.pod-dies-mid-turn`, `recovery.pod-dies-inside-a-side-effect`, `recovery.headless-recovery-impossible`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRecover'`

- [ ] 27.2 `core`: implement owner-checked `Inspect` from stores only, exposing current turn, last `Seq`, pending calls, cost and remaining limits across pods.
  - files: `core/inspect.go`, `core/inspect_test.go`
  - scenarios: `tools.inspect-from-another-pod`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestInspect'`

- [ ] 27.3 `core`: implement store-clock `Stale(staleAfter)`, lease reclaim and checkpoint expiry with injectable memory time (`memory.WithNow` semantics within the single core package).
  - files: `core/store_clock.go`, `core/store_clock_test.go`, `core/store_runs.go`, `core/store_checkpoints.go`
  - scenarios: `stores.stale-by-store-clock`, `stores.skewed-caller-cannot-reclaim`, `stores.checkpoint-expiry-store-clock`, `stores.memstore-clock-jump`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStoreClock'`

- [ ] 27.4 `core`: enforce `MaxWallClock` using elapsed monotonic time independently of store timestamps and system wall-clock jumps.
  - files: `core/wall_clock.go`, `core/wall_clock_test.go`
  - scenarios: `limits.wall-clock-monotonic`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestWallClockMonotonic'`

27a. [ ] `core`: graceful shutdown and health — `Stack.Shutdown`/`Ready`/`Health` (`runtime.readyz-false-on-schema-skew`, `runtime.readyz-false-during-shutdown`), `ErrShuttingDown`, safe-point preemption with `Preempted` + `Continue()`, `Runs.Preempted` (memory store), `Recover` preempted-first. — `runtime.shutdown-rejects-new`, `runtime.shutdown-preempts-at-safe-point`, `runtime.shutdown-side-effect-completes`, `runtime.shutdown-partial-model-call-dropped`, `runtime.preempted-resume-continue`, `runtime.shutdown-grace-exhausted`, `recovery.preempted-before-stale`

- [ ] 27a.1 `core`: implement idempotent `Stack.Shutdown`, `Ready`/`Health`, `ErrShuttingDown`, readiness checks, pending-write flush and deadline failure with `ErrShutdownIncomplete`/`ShutdownIncomplete`.
  - files: `core/shutdown.go`, `core/shutdown_test.go`, `core/health.go`, `core/health_test.go`
  - scenarios: `runtime.shutdown-rejects-new`, `runtime.readyz-false-on-schema-skew`, `runtime.readyz-false-during-shutdown`, `runtime.shutdown-grace-exhausted`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStackShutdownHealth'`

- [ ] 27a.2 `core`: preempt attached and detached runs at persisted safe points with `Preempted`; complete shielded side effects, discard partial model calls and resume through `Continue()` without approval.
  - files: `core/preemption.go`, `core/preemption_test.go`, `core/preemption_resume.go`, `core/preemption_resume_test.go`
  - scenarios: `runtime.shutdown-preempts-at-safe-point`, `runtime.shutdown-side-effect-completes`, `runtime.shutdown-partial-model-call-dropped`, `runtime.preempted-resume-continue`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSafePointPreemption'`

- [ ] 27a.3 `core`: implement memory `Runs.Preempted` through optional `PreemptedLister`; make `Recover` resume preempted runs before stale runs and skip tokens already consumed by clients.
  - files: `core/store_runs_preempted.go`, `core/store_runs_preempted_test.go`, `core/recover_preempted.go`, `core/recover_preempted_test.go`
  - scenarios: `recovery.preempted-before-stale`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRecoverPreemptedFirst'`

## F. Telemetry, tests, examples, performance

28. [ ] `std/telemetry`: OTel spans (`invoke_agent`, `chat`, `execute_tool`, `gohan.guard`, `gohan.decide`), metrics incl. TTFT/TPOT, `Convention` layer with `GenAI` preset, `release`/`variant` attributes, loop-detection metric. — `telemetry.same-tree-on-all-backends`, `telemetry.ttft-and-tpot`, `telemetry.loop-detection`, `telemetry.rename-is-config`, `telemetry.canonical-keys`, `telemetry.forbidden-label`, `telemetry.no-content-in-logs`

- [ ] 28.1 `core`: Emit OTel `invoke_agent`, `chat`, `execute_tool`, `gohan.guard`, `gohan.decide` spans and canonical `gohan.*` keys from governed chains; expose the shared backend-tree fixture.
  - files: `core/telemetry.go`, `core/telemetry_test.go`
  - scenarios: `telemetry.same-tree-on-all-backends`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestTelemetrySpanTree'`

- [ ] 28.2 `std/telemetry`: Implement `Convention`, `ContentMapping`, `GenAI` and `Langfuse` presets; map core canonical keys without changing core when convention names change.
  - files: `std/telemetry/convention.go`, `std/telemetry/convention_test.go`
  - scenarios: `telemetry.canonical-keys`, `telemetry.rename-is-config`
  - verify: `go test -short -timeout 2m ./std/telemetry/ -run 'TestTelemetryConvention'`

- [ ] 28.3 `std/telemetry`: Record TTFT/TPOT and loop-detection metrics with `release`/`variant`; enforce the metric-label allowlist and `WithTenantLabel()` at `Build`.
  - files: `std/telemetry/metrics.go`, `std/telemetry/metrics_test.go`
  - scenarios: `telemetry.ttft-and-tpot`, `telemetry.loop-detection`, `telemetry.forbidden-label`
  - verify: `go test -short -timeout 2m ./std/telemetry/ -run 'TestTelemetryMetrics'`

- [ ] 28.4 `core`: Implement `WithLogger` and per-run canonical attributes; exclude content, credentials and `Raw` values at every log level.
  - files: `core/logging.go`, `core/logging_test.go`
  - scenarios: `telemetry.no-content-in-logs`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestTelemetryLogging'`

29. [ ] `testkit/gohantest`: `ScriptedModel` (itself passing `conformance.Model`), `Recorder`/`Replayer` (`Strict`, `ByTurn`, `Rerecord`), fakes, fault injection, leak profile; `conformance` suites for runtime, chain, flow, model with the provider fixture set. — `telemetry.replay-strictness`, `runtime.foreign-tool-under-native`

- [ ] 29.1 `testkit/gohantest`: Implement illustrative `ScriptedModel` canned turns, request assertions, usage and timed deltas; pass `conformance.Model` with the provider fixture set.
  - files: `testkit/gohantest/scripted_model.go`, `testkit/gohantest/scripted_model_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/gohantest/ -run 'TestScriptedModel'`

- [ ] 29.2 `testkit/gohantest`: Fix cassette behaviour to `testing.md`, not illustrative `Recorder`/`Replayer`/`Rerecord` names: assembled-request hash plus profile `Version` key, JSON `version`/`calls`/`key`/`chunks`/`at_ms`/`chunk`/`usage`, timed replay, `Strict`/`ByTurn`/`Rerecord`, and `GOHAN_CASSETTES` selection.
  - files: `testkit/gohantest/cassette.go`, `testkit/gohantest/cassette_test.go`
  - scenarios: `telemetry.replay-strictness`
  - verify: `go test -short -timeout 2m ./testkit/gohantest/ -run 'TestCassetteModes'`

- [ ] 29.3 `testkit/gohantest`: Implement hand-written fakes, fault injection and the goroutine-leak profile used by conformance suites.
  - files: `testkit/gohantest/fakes.go`, `testkit/gohantest/fakes_test.go`, `testkit/gohantest/faults.go`, `testkit/gohantest/faults_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/gohantest/ -run 'TestFakesAndFaults'`

- [ ] 29.4 `testkit/conformance`: Implement `Model` conformance and the provider fixture set for error classes, usage, version, fidelity, streaming, cancellation and `Raw` round-trip.
  - files: `testkit/conformance/model.go`, `testkit/conformance/model_test.go`, `testkit/conformance/fixtures.go`, `testkit/conformance/fixtures_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/conformance/ -run 'TestModelConformance'`

- [ ] 29.5 `testkit/conformance`: Implement `Runtime` and `Chain` suites with leak checks and the imported eino `InvokableTool` fixture under native through the full governed tool chain.
  - files: `testkit/conformance/runtime.go`, `testkit/conformance/runtime_test.go`, `testkit/conformance/chain.go`, `testkit/conformance/chain_test.go`
  - scenarios: `runtime.foreign-tool-under-native`
  - verify: `go test -short -timeout 2m ./testkit/conformance/ -run 'TestRuntimeAndChainConformance'`

- [ ] 29.6 `testkit/conformance`: Implement `Flow` conformance for invocation and suspension with memory stores, scripted models and leak checks. **GATE**
  - files: `testkit/conformance/flow.go`, `testkit/conformance/flow_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/conformance/ -run 'TestFlowConformance' && task spec:coverage`

30. [ ] `std/tool/exec` host runner (argv, env allowlist, caps, process-group kill, `AllowHostExec` warning). — `tools.timeout-kills-group`, `tools.output-cap`, `tools.env-allowlist`, `tools.large-output-stored`, `tools.notes-survive-reset`

- [ ] 30.1 `std/tool/exec`: Implement `exec.New` with typed argv, an empty-default env allowlist, working directory, process-group timeout kill, effect-aware outcomes and the `AllowHostExec` build warning.
  - files: `std/tool/exec/exec.go`, `std/tool/exec/exec_test.go`, `std/tool/exec/process.go`, `std/tool/exec/process_test.go`
  - scenarios: `tools.timeout-kills-group`, `tools.env-allowlist`
  - verify: `go test -short -timeout 2m ./std/tool/exec/ -run 'TestExecHostRunner'`

- [ ] 30.2 `std/tool/exec`: Cap stdout/stderr independently with truncation markers; verify `MaxOutput` excerpts, full-content `Ref` retrieval and `gohan.output.stored` through `std/outputs`.
  - files: `std/tool/exec/output.go`, `std/tool/exec/output_test.go`
  - scenarios: `tools.output-cap`, `tools.large-output-stored`
  - verify: `go test -short -timeout 2m ./std/tool/exec/ -run 'TestExecOutput'`

- [ ] 30.3 `std/notes`: Verify `notes_write` survives run reset and truncated history; assemble the saved notes into the next run's `SlotSession`.
  - files: `std/notes/reset_test.go`
  - scenarios: `tools.notes-survive-reset`
  - verify: `go test -short -timeout 2m ./std/notes/ -run 'TestNotesSurviveReset'`

31. [ ] `examples/quickstart` and `examples/excursions` S1 (native runtime, memory stores, scripted model) green offline. — acceptance for the `flow`, `runtime`, `permission` paths above

- [ ] 31.1 `examples/quickstart`: Establish the `examples/` module and offline quickstart with native runtime, memory stores and scripted model; wire `task examples:test`.
  - files: `examples/go.mod`, `examples/quickstart/main.go`, `examples/quickstart/main_test.go`, `Taskfile.yml`
  - scenarios: none
  - verify: `go -C examples test -short -timeout 2m ./quickstart/ -run 'TestQuickstartOffline' && task examples:test`

- [ ] 31.2 `examples/excursions`: Implement S1 offline in the `examples/` module with native runtime, memory stores, scripted model and fixture-backed tools; verify flow, runtime and permission acceptance through `task examples:test`.
  - files: `examples/excursions/main.go`, `examples/excursions/main_test.go`, `examples/excursions/fixtures.go`, `examples/excursions/README.md`
  - scenarios: none
  - verify: `go -C examples test -short -timeout 2m ./excursions/ -run 'TestExcursionsS1Offline' && task examples:test`

32. [ ] Performance baselines on the reference machine, frozen; benchmark gate wired into CI. — `performance.chain-overhead-within-budget`, `performance.prefix-build-allocation-free`, `performance.regression-gate`

- [ ] 32.1 `core`: Add `BenchmarkToolChain_ReadOnly`, raw-call comparison and allocation contracts; freeze tool-chain and model-chain overhead baselines on the reference CI runner.
  - files: `core/chain_benchmark_test.go`, `core/performance_baselines.json`
  - scenarios: `performance.chain-overhead-within-budget`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestChainPerformanceBudget' -bench 'Benchmark(ToolChain_ReadOnly|ToolCall_Raw|ModelChain)' -benchmem`

- [ ] 32.2 `core`: Enforce zero allocations for unchanged assembler prefix builds with `testing.AllocsPerRun` and a prefix benchmark.
  - files: `core/assembly_benchmark_test.go`
  - scenarios: `performance.prefix-build-allocation-free`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestPrefixBuildAllocationFree' -bench 'BenchmarkPrefixBuild' -benchmem`

- [ ] 32.3 `core`: Wire the reference-runner CI regression gate: alternating base/head measurements, three rounds, fastest round, 5% tolerance, SHA-cached verdicts and allocation-increase rejection. **GATE**
  - files: `core/performance_gate_test.go`, `tools/performance_gate.go`, `.github/workflows/performance.yml`, `docs/design/performance-baselines.md`
  - scenarios: `performance.regression-gate`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestPerformanceRegressionGate' && task test`

33. [ ] M0.5: API review document; `docs/design/compatibility.md` applied (interface kinds in `types.md`, `task api:check` job, optional-interface fallbacks `stores.optional-preempted-lister-fallback`, `working-state.memory-store-optional`); the three acceptance processes' offline scenarios (`engines`) green against memory stores; core types/ports declared v1-candidate. — `engines.*` offline subset

- [ ] 33.1 `core`: Apply `compatibility.md` optional-interface behaviour for absent `PreemptedLister` and `MemoryStore`, reusing the existing fallback scenario bindings.
  - files: `core/recover.go`, `core/recover_optional_test.go`, `core/working_state.go`, `core/working_state_optional_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./core/... -run 'TestOptionalInterfaceFallbacks'`

- [ ] 33.2 `core`: Record every exported interface as a port or handle in `types.md`; implement `task api:check` and its CI job with v0 reporting and v1 incompatibility rejection. **GATE**
  - files: `tools/gen_types_index.py`, `docs/design/types.md`, `Taskfile.yml`, `.github/workflows/api.yml`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./core/... -run 'TestOptionalInterfaceFallbacks' && task spec:types && task api:check`

- [ ] 33.3 `examples/kafka-refunds`: Implement offline refund delivery and redelivery in the `examples/` module against memory stores; deduplicate `OperationID` and enforce the live refund kill flag after approval.
  - files: `examples/kafka-refunds/main.go`, `examples/kafka-refunds/main_test.go`, `examples/kafka-refunds/README.md`
  - scenarios: `engines.redelivery-returns-the-same-result`, `engines.live-deny-beats-approval`
  - verify: `go -C examples test -short -timeout 2m ./kafka-refunds/ -run 'TestKafkaRefundsOffline' && task examples:test`

- [ ] 33.4 `examples/temporal-travel`: Implement offline activity/signal composition in the `examples/` module against memory stores; recover consumed input exactly once, preserve frozen flags and replay by `Step` re-execution.
  - files: `examples/temporal-travel/main.go`, `examples/temporal-travel/main_test.go`, `examples/temporal-travel/README.md`
  - scenarios: `engines.crash-after-consume`, `engines.frozen-flag-on-replay`, `engines.replay-is-step-re-execution`
  - verify: `go -C examples test -short -timeout 2m ./temporal-travel/ -run 'TestTemporalTravelOffline' && task examples:test`

- [ ] 33.5 `examples/camunda-invoice`: Implement offline job/user-task composition in the `examples/` module against memory stores; handle duplicate workers, suspended leases, revoked resume authority and stale-control suspension then denial.
  - files: `examples/camunda-invoice/main.go`, `examples/camunda-invoice/main_test.go`, `examples/camunda-invoice/README.md`
  - scenarios: `engines.duplicate-workers`, `engines.suspended-runs-are-not-reclaimed`, `engines.revoked-authority-on-resume`, `engines.stale-control-state`
  - verify: `go -C examples test -short -timeout 2m ./camunda-invoice/ -run 'TestCamundaInvoiceOffline' && task examples:test`

- [ ] 33.6 `core`: Publish the M0.5 API review document after all three offline acceptance processes pass; review port/handle kinds, optional-interface fallbacks and async contracts. **GATE**
  - files: `docs/design/api-review-m0-5.md`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./core/... -run 'TestOptionalInterfaceFallbacks' && task examples:test && task api:check && task spec:types && task spec:coverage`

- [ ] 33.7 `core`: Declare core types and ports v1-candidate after the API review; document the freeze marker without releasing `v1.0.0` before M2. **GATE**
  - files: `docs/design/compatibility.md`, `docs/design/api-review-m0-5.md`, `CHANGELOG.md`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./core/... -run 'TestOptionalInterfaceFallbacks' && task spec:types && task api:check && task lint && task test && task examples:test`

- [ ] 33.90 `core/stores`: Deliver the two optional-interface fallbacks and their documented behaviour when the type assertion fails: `PreemptedLister` for `Runs` and `MemoryStore` for the notes store.
  - files: `core/stores/optional.go`
  - scenarios: `stores.optional-preempted-lister-fallback`, `working-state.memory-store-optional`
  - verify: `go test -short -timeout 2m ./core/stores/ -run 'TestOptionalInterfaces'`

## Corrections applied by the 2026-10-04 review

The rows above keep their reviewed scope; these are the places the review corrected them, with the record that owns each change.

- Row 1: branch is `master`; `git init`, the `origin` remote and `.gitignore` already exist; `go.work` and `go.work.sum` are committed as development wiring and are never the version authority. CI carries `spec`, `lint`, `test` and `bench` on `master` only — `conformance` and `examples` arrive with their suites, `api:check` with `adapter/httpapi` in M4, and the adapter matrix is empty for all of M0, so it is path-filtered. `task test:full` and `task examples:test` are named by `AGENTS.md` and land with rows 29 and 31.
- Row 1: `task spec:coverage` has no implementation and no owner — the target and its script are this row's work (ADR-0135).
- Row 14: the gate skeleton, `ApprovalPolicy`, `ApprovalRequest` and `ApprovalPolicy.MaxPending` are core (`docs/design/architecture.md` §4.2a, `permission/spec.md`); only the tier defaults, grants and expiry policy are `std/permission`. The pending-approval cap is scoped per subject and tenant, so it is a policy value, not a `RunLimits` field.
- Row 15: `stores.same-transaction-journal` is deferred to M1 with `adapter/postgres` (ADR-0016).
- Row 17: `ToolPolicy`, `Trusted`/`Untrusted` and `MaxEffect` are declared in core; the pinned manifest, hash-drift check and `ToolFilter` are `std`; `Deferred` plus `search_tools` is `std/toolsearch`.
- Row 18: `std/keys` supplies the provider-key sources.
- Row 19: `Truncate` is `std/context` (M0 creates the package for the projection alone; persisted compaction is M2), `ContextBudget` is core, `std/tokens.Heuristic` is std.
- Row 21: `MaxParallelTools` and `SequentialTools()` are `Build` options; the runtime consumes them.
- Row 22: the runtime reads those two options rather than declaring them.
- Row 25: `FlowAsTool` in M0 is the typed wrapper only — cost charged against the hub's `MaxCost`, `Checkpoint.Child`, tree recovery, nested spans. Excluded until `subflows` (M3): nested suspension, `Collect`/`FailFast`, parallel children, per-child session history, sub-flow limits beyond the tree budget.
- Row 26: `StateChanged` diffing is `std/state` (JSON Patch); core holds the event shape and emits the supplied patch.
- Row 28: `gohan.guard` and `gohan.decide` are span names, not scenario IDs; canonical keys are emitted by core, the convention layer is `std/telemetry`.
- Row 29: `Recorder`/`Replayer`/`Rerecord` are illustrative names — the behaviour is fixed by `docs/design/testing.md` (assembled-request hash plus profile `Version` as the key, the cassette shape, the three modes, `GOHAN_CASSETTES`).
- Row 30: `MaxOutput` is 64 KiB (`tools/spec.md` defaults rule, ADR-0106; the 16 KiB sites were corrected by ADR-0137).
- Row 33 (M0.5): ADR-0136 gives M0.5 the offline paths of the three acceptance processes as `examples/{kafka-refunds,temporal-travel,camunda-invoice}` over core, std and the memory stores; the nine `engines.*` scenario IDs are enumerated in its chunks and `engines.*` is not a scenario ID.
- Every chunk: `core` is a package tree (ADR-0139), so a file path names its package; verify commands use `./core/...`.

## Deferred

Ownership is per scenario in `openspec/scenarios.json` (`deferred_to`), set where a milestone deliberately leaves a scenario red (ADR-0135). Tally at M0: 280 of 512 scenarios are in the chunks above; the other 232 name a later milestone (M0.5 9, M1 86, M2 36, M3 56, M4 45). No scenario of an M0 capability is left unassigned.
