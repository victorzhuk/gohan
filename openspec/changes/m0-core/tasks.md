# Tasks: m0-core

Scope: the 21 M0 capabilities named in `proposal.md`. Order follows dependency and the proof → identity → verification → scaffold order. Per-scenario milestone ownership lives in `openspec/scenarios.json` as `deferred_to` (ADR-0135); the rows below are the scope this plan was reviewed at, and the chunks under each row are what you land.

## How to run this plan

- A **chunk** is the executable unit: one package or type-cluster, at most four scenario IDs, its own files, one literal verify command. Work chunks in order within a row.
- A chunk is done when its `verify:` command is green and `task lint` and `task test` are green for the packages it touched. The subtest that satisfies a scenario is named exactly by its ID (`t.Run("flow.plain-invoke", …)`); the `-run` expression in a verify command names the *test function* the chunk introduces.
- `task spec:coverage` reports every registered scenario without a subtest and every subtest named like an unregistered ID, and fails on the latter. `task spec:coverage --gate <milestone>` additionally fails when a scenario with no later `deferred_to` has no subtest — that is the milestone exit check, not the per-chunk check (ADR-0135).
- Chunks marked **GATE** need the named project command as well.
- `core/` is a package tree (ADR-0139): the driver is `core/` with package clause `gohan`, the shared vocabulary and ports are in `core/types`, and each enum lives in its own package. Imports run downward only; no leaf package imports `core/` or `core/runtime`. A chunk's `core/` label and file path say where its declarations land — the package table in `openspec/changes/m0-core/design.md` decides which leaf, so `core/store_session.go` lands as `core/stores/session.go` and `core/credential.go` as `core/types/credential.go` — and `./core/...` in a verify command covers the whole tree.
- Row 1 carries no scenario: it lands the module, the tooling and the coverage gate every later row's definition of done depends on. Its floor is green `task lint`, `go vet` and `task test` over the package declarations it creates; row 2 is the first row with scenarios, because `core/types` holds the first declarations a scenario names.
- Row 3 lands the whole shared vocabulary: the 32 sentinels, the 12 typed errors, the model error classes, the 14 event payloads and the seven types those payloads reference (`CallKey`, `PatchOp`, `FeedbackTarget`, `FeedbackSource`, `ResumeToken`, `SuspendReason`, `GuardStage`). A later row uses them; it declares only what row 3 does not, and the corrections list records the split per row.
- The driver package declares no shared type and re-exports no const (ADR-0139 rule 3). The last step of every vocabulary row adds the type aliases for what that row landed into `core/aliases.go`, so the documented call sites (`gohan.Message`, `gohan.Caps`, `gohan.Flow`) stay valid without ever recreating the name collisions the split removed.

## A. Module and types

1. [x] Create the root module (`github.com/victorzhuk/gohan`, go 1.27, Apache-2.0), `go.work`, Taskfile with `spec:types`, `spec:coverage` (scenarios.json vs the `go test -json` execution stream), `lint`, `test`, `bench`; golangci-lint + depguard config encoding the core budget rule; `.github/workflows/ci.yml` with jobs `spec` (`spec:types`, `spec:coverage`), `lint`, `test` (root + adapter matrix, path-filtered, race-detected, `-short`), `security` (`govulncheck`), `conformance` (each adapter × root `testkit`), `bench` (gate, pull requests only, reference runner), `examples` (offline from cassettes); all required checks on `master`; `LICENSE` (Apache-2.0), `README.md`. — no scenarios

- [x] 1.1 `core`: Scaffold `github.com/victorzhuk/gohan` with Go 1.27 and a minimal `.gitignore`; declare the first two packages, `core/doc.go` (package `gohan`) and `core/types/doc.go`, so lint, vet and test have a compilable unit from the start; retain completed `git init`, branch `master`, and `origin` = `git@github.com:victorzhuk/gohan.git`.
  - files: `go.mod`, `.gitignore`, `core/doc.go`, `core/types/doc.go`
  - scenarios: none
  - verify: `go mod edit -json`

- [x] 1.2 `core`: Commit `go.work` as development wiring, never version authority (`go.work.sum` stays ignored: it is reproducible per checkout); `LICENSE` and `README.md` already exist — the README gains the task-target list.
  - files: `go.work`, `README.md`
  - scenarios: none
  - verify: `go work edit -json`

- [x] 1.3 `core`: Add Taskfile targets `spec:types`, `spec:coverage`, `lint`, `test`, `bench`, pinned tooling and golangci-lint + depguard enforcement of the core budget rule.
  - files: `Taskfile.yml`, `.golangci.yml`, `go.mod`
  - scenarios: none
  - verify: `task --list-all`

- [x] 1.4 `core`: Implement `spec:coverage` under ADR-0135: discover actual scenario subtests, reject unregistered IDs and missing IDs without later `deferred_to`; use execution JSON because `go test -list` does not enumerate subtests. **GATE**
  - files: `tools/spec_coverage.py`, `tools/spec_coverage_test.py`, `Taskfile.yml`
  - scenarios: none
  - verify: `timeout 2m python3 -m unittest discover -s tools -p 'spec_coverage_test.py'`

- [x] 1.5 `core`: Add CI checks `spec`, `lint`, `test`, `security` (`govulncheck`) and `bench` required on `master`; run the test leg race-detected with `-short`; path-filter the empty M0 adapter matrix and gate PR benchmarks on the reference runner; add `api:check` only at M4; defer conformance/examples jobs until their suites exist.
  - files: `.github/workflows/ci.yml`, `README.md`
  - scenarios: none
  - verify: `timeout 2m actionlint .github/workflows/ci.yml`

2. [x] `core`: message model — `Message`, `Block` kinds incl. `Compaction`, `Origin`, `Role`, `ModelRequest`/`ModelChunk`/`Usage`, `ToolUse`/`ToolResult`/`Outcome`. Round-trip property tests for the block model. — `messages.order-preserved`, `messages.typed-deltas`, `messages.duplicate-keys-in-tool-args`

- [x] 2.1 `core`: Implement `Message`, `Role`, `BlockBase` with `BlockOrigin`, all `Block` kinds including `Compaction`, `ToolUse`, `ToolResult` and `Outcome`; preserve ordered blocks and reasoning signatures in round-trip property tests.
  - files: `core/types/message.go`, `core/types/message_test.go`
  - scenarios: `messages.order-preserved`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestMessageRoundTrip'`

- [x] 2.2 `core`: Implement `DeltaKind`, `FinishReason`, `ModelChunk` and `Usage`; preserve `DeltaReasoning`, `DeltaText` and `DeltaToolArgs` distinctions. `ModelRequest`, `ModelOptions`, `ToolChoice` and `AffinityKeyStrategy` land in 18.7 instead: the request shape is `{System []Block, Tools []ToolSpec, Messages []Message, Options ModelOptions}` and `ToolSpec` arrives with row 12.
  - files: `core/types/model_types.go`, `core/types/model_types_test.go`
  - scenarios: `messages.typed-deltas`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestModelTypes'`

- [x] 2.3 `core`: Validate complete `ToolUse.Args` with `json/v2`; reject duplicate JSON keys as `ToolResult` with `Failed`/`Permanent` for the later runtime to consume (`ValidateToolArgs`, `ToolArgsError{Reason}`, `ArgsErrorResult`).
  - files: `core/types/tool_args.go`, `core/types/tool_args_test.go`
  - scenarios: `messages.duplicate-keys-in-tool-args`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolArgs'`

3. [x] `core`: events and stop reasons — `Event` kinds, `EventMeta` with `Seq`, `StopReason`; error classes and sentinel errors from `docs/design/types.md`; `ErrorCode` catalog, `Problem`, `ProblemOf` (`errors.every-sentinel-has-code`, `errors.problem-of-unknown-is-internal`, `errors.detail-never-carries-provider-body`, `errors.retry-after-on-retryable`, `streams.terminal-error-event`, `streams.close-without-done-is-interrupted`). — `streams.monotonic-seq`

- [x] 3.1 `core`: Implement `StopReason`, `EventMeta`, the sealed `Event` interface, all fourteen payload types, `NoticeKind`/`RunNotice`/`Notifier`, `Done` and `CallKey`; `StateChanged` and `FeedbackRecorded` bring `PatchOp`, `FeedbackTarget` and `FeedbackSource` with them.
  - files: `core/types/event.go`, `core/types/event_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./core/... -run 'TestEventTypes'`

- [x] 3.2 `core`: Implement error classes, sentinel errors from `docs/design/types.md`, the `ErrorCode` catalog, `Problem` and `ProblemOf`, plus `ResumeToken`, `SuspendReason` and `GuardStage` with their members, which the `Suspended` and `GuardBlocked` payloads need; enforce catalog completeness and the unknown-error `gohan.internal` fallback. **GATE**
  - files: `core/types/errors.go`, `core/types/problem.go`, `core/types/problem_test.go`, `tools/gen_types_index.py`, `tools/gen_types_index_test.py`
  - scenarios: `errors.every-sentinel-has-code`, `errors.problem-of-unknown-is-internal`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestProblemCatalog' && task spec:types && timeout 2m python3 -m unittest discover -s tools -p 'gen_types_index_test.py'`

- [x] 3.3 `core`: Render `Problem.Detail` from fixed templates and allow-listed fields; exclude provider bodies and preserve retryable `RetryAfter`, including lease remaining for `ErrRunActive`.
  - files: `core/problem.go`, `core/problem_detail_test.go`
  - scenarios: `errors.detail-never-carries-provider-body`, `errors.retry-after-on-retryable`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestProblemDetail'`

- [x] 3.4 `core`: Define terminal stream failure contracts using `Problem`, the next `EventMeta.Seq` and `gohan.stream_interrupted`; test core representations without introducing the M4 HTTP/SSE adapter.
  - files: `core/stream_error.go`, `core/stream_error_test.go`
  - scenarios: `streams.terminal-error-event`, `streams.close-without-done-is-interrupted`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStreamErrors'`

4. [x] `core`: `RunInfo`, `Principal`, `CostTags`, context keys and `(T, bool)` accessors (`WithPrincipal`/`PrincipalFrom`, `WithCredential`, `WithIdempotencyKey`, `RunInfoFrom`), seam check for `ErrNoPrincipal`, `CredentialSource` port, `RunMode`, `ReleaseID`/`Variant` fields. — `identity.no-principal`, `identity.no-principal-at-seam`, `identity.accessor-outside-run`, `identity.model-cannot-set-identity`, `identity.token-never-exported`, `identity.credential-not-on-principal`

- [x] 4.1 `core`: Implement `RunInfo`, `Principal`, `CostTags`, `RunMode`, `ReleaseID`/`Variant`, private context keys and `(T, bool)` accessors, including zero-value access outside a run.
  - files: `core/identity.go`, `core/identity_test.go`
  - scenarios: `identity.accessor-outside-run`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestIdentityContext'`

- [x] 4.2 `core`: Implement the shared principal seam check returning `ErrNoPrincipal` unless anonymous invocation is allowed; require refusal before events or store access for later `Send`, `Invoke` and `Resume` wiring.
  - files: `core/identity_seam.go`, `core/identity_seam_test.go`
  - scenarios: `identity.no-principal`, `identity.no-principal-at-seam`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestPrincipalSeam'`

- [x] 4.3 `core`: Implement `Credential`, `WithCredential`, `CredentialFrom` and the `CredentialSource` port; keep tokens separate from `Principal` and exclude them from exported identity representations.
  - files: `core/credential.go`, `core/credential_test.go`
  - scenarios: `identity.token-never-exported`, `identity.credential-not-on-principal`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestCredentialIsolation'`

- [x] 4.4 `core`: Keep `PrincipalFrom(ctx)` authoritative when input names `user_id` or `tenant`; define the identity exclusion contract consumed by row 6 `NewTool`/`ExcludeFields`, without adding tool construction to this row.
  - files: `core/identity_args.go`, `core/identity_args_test.go`
  - scenarios: `identity.model-cannot-set-identity`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestIdentityArguments'`

5. [x] Run `task spec:types` and diff the index against the package's exported identifiers; fix drift in either direction. — no scenarios

- [x] 5.2 `core`: Extend `tools/gen_types_index.py` so it fails when one name is declared twice with different kinds in the same package — the check that would have caught the eight collisions of ADR-0139 — and fails cleanly (not with an uncaught `ValueError`) when a spec heading it indexes by name is missing. **GATE**
  - files: `tools/gen_types_index.py`, `tools/gen_types_index_test.py`
  - scenarios: none
  - verify: `timeout 2m python3 -m unittest discover -s tools -p 'gen_types_index_test.py'`

- [x] 5.1 `core`: Run the `spec:types` drift gate: regenerate `docs/design/types.md`, compare implemented exported identifiers with their capability contracts, and fix implementation or spec/index drift in either direction without requiring later-row identifiers to exist. **GATE**
  - files: `docs/design/types.md`, `tools/gen_types_index.py`
  - scenarios: none
  - verify: `task spec:types && git diff --exit-code -- docs/design/types.md`

## B. Ports and memory stores

6. [x] `core`: `Model`, `Tool`, `Decider`, `EgressPolicy` + `ErrEgressPolicyRequired` + exfil derivation (`tools.egress-policy-required`, `build.exfil-derived-from-egress`), name grammar and collision check (`tools.name-grammar`, `tools.collision-fails-build`, `tools.reserved-names`, `tools.rename-is-new-tool`, `tools.tool-value-shared-and-concurrent`), `ToolSpec` (incl. `Effect`, `Trust`, `Capabilities`, `Verify`, `Deferred`, `Executor`), `Usage.ProviderToolCalls`, `Caps.ProviderTools`, `NewTool` with schema derivation and `ExcludeFields`. — `tools.unknown-tool`, `tools.invalid-args-on-raw-tool`, `tools.classified-error`, `tools.schema-from-tags`, `tools.untyped-args-rejected`, `tools.out-passthrough`, `tools.panic-recovered`, `tools.default-timeout`, `decider.rules-confidence-one`, `decider.schema-validated`

- [x] 6.1 `core`: Declare `Decider` and `Decision` from `openspec/specs/decider/spec.md` (both generic and dependency-free); test the confidence and decision-schema contracts. The `Model` port moves to 18.6, because its signature needs `ModelProfile` and `ModelRequest`; `Usage.ProviderToolCalls` already landed in 2.2, and `Caps.ProviderTools` is a field of the `Caps` that 18.6 declares.
  - files: `core/types/decider.go`, `core/types/decider_test.go`
  - scenarios: `decider.rules-confidence-one`, `decider.schema-validated`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestDeciderContract'`

- [x] 6.2 `core`: Declare the tool vocabulary — `Tool`, `ToolSpec`, `Effect`, `Trust`, `Capabilities`, `Executor`, `RiskTier`, `PrivateRanges`, `EgressPolicy`, `Verify`, `Deferred` — keeping tool-policy values in `std` under row 17. `EgressPolicy` belongs here because `ToolSpec` carries one.
  - files: `core/types/tool.go`, `core/types/tool_test.go`
  - scenarios: `tools.tool-value-shared-and-concurrent`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolConcurrentValue'`

- [x] 6.3 `core`: Enforce tool name grammar, collision checks, reserved names and rename identity in `NewTool` and `Build`.
  - files: `core/tool_names.go`, `core/tool_names_test.go`
  - scenarios: `tools.name-grammar`, `tools.collision-fails-build`, `tools.reserved-names`, `tools.rename-is-new-tool`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolNames'`

- [x] 6.4 `core`: Require an `EgressPolicy` on a tool that reaches the network (`ErrEgressPolicyRequired`) and derive `Capabilities.Exfil` at `Build`, using the `ToolSpec` and `Capabilities` that 6.2 declares.
  - files: `core/types/tool_egress.go`, `core/types/tool_egress_test.go`
  - scenarios: `tools.egress-policy-required`, `build.exfil-derived-from-egress`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolEgressContract'`

- [x] 6.5 `core`: Derive `NewTool` schemas with the `json`/`desc`/`enum`/`min`/`max`/`pattern` walker, `ExcludeFields` and untyped-argument rejection.
  - files: `core/tool_schema.go`, `core/tool_schema_test.go`
  - scenarios: `tools.schema-from-tags`, `tools.untyped-args-rejected`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolSchema'`

- [x] 6.6 `core`: Reject unknown tools and invalid raw `Tool` arguments before `Call`; preserve classified errors in `ToolResult`.
  - files: `core/tool_call.go`, `core/tool_call_test.go`
  - scenarios: `tools.unknown-tool`, `tools.invalid-args-on-raw-tool`, `tools.classified-error`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolCallContract'`

- [x] 6.7 `core`: Complete `NewTool` output mapping, panic recovery and effect-specific timeout application.
  - files: `core/tool_new.go`, `core/tool_new_test.go`
  - scenarios: `tools.out-passthrough`, `tools.panic-recovered`, `tools.default-timeout`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestNewToolExecution'`

7. [x] `core`: `SessionLog` port + memory implementation with versioned append and `Message.ID` assignment; `SessionIndex` on the memory store, `Stack.Sessions`/`UpdateSession`; `Stores.ForkSession`. — `stores.sessions-listed-by-owner`, `stores.sessions-order-last-activity`, `stores.sessions-default-excludes-forks-children`, `stores.session-index-optional`, `stores.purge-respects-pinned-and-archived`, `identity.sessions-owner-checked`, `stores.fork-prefix`, `stores.fork-requires-no-lease`, `stores.fork-survives-parent-delete`, `stores.reconstruct-crosses-fork`, `permission.grants-not-inherited-on-fork`, `working-state.fork-copies-notes-and-state`, `stores.append-conflict`, `stores.delete-cascades`, `identity.session-forbidden-cross-tenant`, `identity.session-scope-read`

- [x] 7.1 `core`: Implement `SessionLog`, its memory store, versioned append, `Message.ID`, `SessionIndex`, the index queries, fork and the deletion cascade with ownership boundaries. The `Stack.Sessions`/`UpdateSession` and `Stores.ForkSession`/`DeleteSession` façades land with row 21, where those containers are built, so the behaviour lives on the memory store and the tests assert it there; the cascade reaches the other stores through injected dependents, because rows 8-12 have not landed them yet.
  - files: `core/stores/session.go`, `core/stores/session_memory.go`, `core/stores/session_test.go`
  - scenarios: `stores.append-conflict`, `stores.delete-cascades`, `identity.session-forbidden-cross-tenant`, `identity.session-scope-read`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionLogContract'`

- [x] 7.2 `core`: Test `SessionIndex` owner listings, activity order, default kinds and optional-interface refusal.
  - files: `core/stores/session_index_test.go`
  - scenarios: `stores.sessions-listed-by-owner`, `stores.sessions-order-last-activity`, `stores.sessions-default-excludes-forks-children`, `stores.session-index-optional`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionIndexContract'`

- [x] 7.3 `core`: Test session purge protection, listing ownership and fork isolation for grants, notes and shared state.
  - files: `core/stores/session_lifecycle_test.go`
  - scenarios: `stores.purge-respects-pinned-and-archived`, `identity.sessions-owner-checked`, `permission.grants-not-inherited-on-fork`, `working-state.fork-copies-notes-and-state`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionLifecycle'`

- [x] 7.4 `core`: Test `Stores.ForkSession` prefix preservation, lease refusal, parent-deletion independence and reconstruction ancestry.
  - files: `core/stores/session_fork_test.go`
  - scenarios: `stores.fork-prefix`, `stores.fork-requires-no-lease`, `stores.fork-survives-parent-delete`, `stores.reconstruct-crosses-fork`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionFork'`

8. [x] `core`: `Checkpoints` port + memory implementation, single-use `Consume`, `Checkpoint` shape incl. `Child`, `Workspace`. — `stores.concurrent-consume`, `stores.no-secrets-stored`, `suspension.token-reuse`

- [x] 8.1 `core`: Implement `Checkpoints`, its memory store and `Checkpoint` with `Child`/`Workspace`, atomic `Consume`, `PendingInput` and secret-free storage.
  - files: `core/store_checkpoint.go`, `core/store_checkpoint_memory.go`, `core/store_checkpoint_shape_test.go`
  - scenarios: `stores.no-secrets-stored`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestCheckpointStoredShape'`

- [x] 8.2 `core`: Test single-use `Checkpoints.Consume` under ten concurrent callers and duplicate approval delivery.
  - files: `core/store_checkpoint_consume_test.go`
  - scenarios: `stores.concurrent-consume`, `suspension.token-reuse`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestCheckpointConsume'`

9. [x] `core`: `Journal` port + memory implementation, `Fingerprint`, `Reserve`/`Complete`, TTL. — `stores.concurrent-reserve`, `stores.journal-ttl`, `stores.replay-returns-recorded-result`

- [x] 9.1 `core`: Implement `Journal`, its memory store, `Fingerprint`, `Reserve`/`Complete`, `ByFingerprint` and store-clock TTL, keyed by the row-3 `CallKey`.
  - files: `core/stores/journal.go`, `core/stores/journal_memory.go`, `core/stores/journal_reserve_test.go`
  - scenarios: `stores.concurrent-reserve`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestJournalReserve'`

- [x] 9.2 `core`: Test `Journal` result expiry after run completion and recorded-result replay without tool execution.
  - files: `core/stores/journal_lifecycle_test.go`
  - scenarios: `stores.journal-ttl`, `stores.replay-returns-recorded-result`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestJournalLifecycle'`

10. [x] `core`: `Runs` port + memory implementation, leases, states incl. `Suspended`/`Resuming`, `Mode`; run mailbox (`Signal`/`Drain`, `Finish` atomic with pending steers); notice outbox (`RunNotice`, `Notifier`, `Notices`/`AckNotice`, written with `Finish`/`Suspend` — `stores.notice-written-with-finish`, `streams.notice-thin-no-content`). — `stores.lease-exclusivity`, `stores.reclaim-race`, `recovery.no-double-run`, `stores.signal-cancel-cross-pod`, `stores.mailbox-full`

- [x] 10.1 `core`: Implement `Runs` states, `Suspended`/`Resuming`, `Mode`, operation lookup and memory state transitions.
  - files: `core/stores/runs.go`, `core/stores/runs_test.go`
  - scenarios: `recovery.no-double-run`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRunsState'`

- [x] 10.2 `core`: Implement `Runs` leases, `Heartbeat`, store-clock expiry and atomic `Reclaim` in memory.
  - files: `core/store_runs_leases.go`, `core/store_runs_leases_test.go`
  - scenarios: `stores.lease-exclusivity`, `stores.reclaim-race`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRunsLeases'`

- [x] 10.3 `core`: Implement the `Signal`/`Drain` mailbox and atomic `Finish` refusal with pending steers.
  - files: `core/store_runs_mailbox.go`, `core/store_runs_mailbox_test.go`
  - scenarios: `stores.signal-cancel-cross-pod`, `stores.mailbox-full`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRunsMailbox'`

- [x] 10.4 `core`: Implement the thin `RunNotice` outbox, `Notifier`, atomic `Finish`/`Suspend` writes and `Notices`/`AckNotice` claims.
  - files: `core/store_runs_notices.go`, `core/store_runs_notices_test.go`
  - scenarios: `stores.notice-written-with-finish`, `streams.notice-thin-no-content`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRunsNotices'`

11. [x] `core`: `AuditLog` (chain-written, checksums, hash chain) and `EventLog` (`Seq`, `Read`, `Expire`) ports + memory implementations; `RetentionPolicy`/`RetentionSource`, `Stack.Maintain`, `SessionMeta.Hold`, `ErrSessionHeld`, `Ephemeral()`. — `stores.chain-written-only`, `stores.no-content`, `stores.hash-chain`, `stores.append-failure-is-fatal-to-the-step`, `stores.decision-trail`, `stores.retention-purge-by-tier`, `stores.retention-zero-deletes-at-finish`, `stores.hold-blocks-delete-and-purge`, `stores.purge-audited`, `redaction.erase-reports-held`, `working-state.notes-follow-working-retention`, `identity.hold-requires-scope`

- [x] 11.1 `core`: Implement `AuditLog` and its memory store with restricted writers, checksums, hash chains and fatal append failures.
  - files: `core/store_audit.go`, `core/store_audit_test.go`
  - scenarios: `stores.chain-written-only`, `stores.no-content`, `stores.append-failure-is-fatal-to-the-step`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestAuditLog'`

- [x] 11.2 `core`: Implement the ordered `AuditLog` decision trail through `Reconstruct`.
  - files: `core/store_audit_reconstruct.go`, `core/store_audit_reconstruct_test.go`
  - scenarios: `stores.decision-trail`, `stores.hash-chain`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestAuditReconstruct'`

- [x] 11.3 `core`: Implement `EventLog` and its memory ring buffer with `Seq`, ordered historical/live `Read` and `Expire`.
  - files: `core/store_events.go`, `core/store_events_test.go`
  - scenarios: `streams.monotonic-seq`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestEventLog'`

- [x] 11.4 `core`: Implement `RetentionPolicy`/`RetentionSource`, `Stack.Maintain`, audited tier purges and the `Ephemeral()` hook without core policy defaults.
  - files: `core/store_retention.go`, `core/store_retention_test.go`
  - scenarios: `stores.retention-purge-by-tier`, `stores.retention-zero-deletes-at-finish`, `stores.purge-audited`, `working-state.notes-follow-working-retention`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStoreRetention'`

- [x] 11.5 `core`: Enforce `SessionMeta.Hold`, `ErrSessionHeld`, `session:hold` and held-session reporting during deletion, `Maintain` and erasure.
  - files: `core/store_hold.go`, `core/store_hold_test.go`
  - scenarios: `stores.hold-blocks-delete-and-purge`, `redaction.erase-reports-held`, `identity.hold-requires-scope`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionHold'`

12. [x] `core`: `FeedbackStore` port + memory implementation, `Stack.Feedback`, `FeedbackRecorded` (`flow.feedback-owner-checked`, `flow.feedback-idempotent-per-name`, `flow.feedback-never-in-context`, `stores.feedback-cascades-on-erase`, `streams.feedback-recorded-event`); `OutputStore` (content-addressed blobs, `InlineBlobBytes`, `Caps.Blobs` checks, URL rule; `messages.blob-stored-by-ref`, `messages.blob-content-addressed`, `messages.url-only-from-user`, `messages.blob-too-large`, `build.blob-caps`, `guards.blob-guard-input`), `NotesStore` (keyed by `NotesKey`, session scope only in M0) ports + memory implementations; schema versions + upcaster registry; `storetest` suites for all seven ports (`Schemas` included). — `working-state.notes-versioned`, `working-state.output-paging`, `stores.native-checkpoint-after-adapter-upgrade`, `stores.graph-checkpoint-incompatible`

- [x] 12.1 `core`: Implement `FeedbackStore`, its memory store and owner-checked `Stack.Feedback` with versioned overwrite and isolated comments/corrections.
  - files: `core/store_feedback.go`, `core/store_feedback_test.go`
  - scenarios: `flow.feedback-owner-checked`, `flow.feedback-idempotent-per-name`, `flow.feedback-never-in-context`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestFeedbackStore'`

- [x] 12.2 `core`: Implement `FeedbackStore` deletion/erasure cascades and thin `FeedbackRecorded` writes with the next `EventLog` sequence.
  - files: `core/store_feedback_lifecycle.go`, `core/store_feedback_lifecycle_test.go`
  - scenarios: `stores.feedback-cascades-on-erase`, `streams.feedback-recorded-event`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestFeedbackLifecycle'`

- [x] 12.3 `core`: Implement `OutputStore`, its content-addressed memory blobs, `InlineBlobBytes`, persisted references and paged output access.
  - files: `core/store_outputs.go`, `core/store_outputs_test.go`, `std/outputs/read_output.go`, `std/outputs/read_output_test.go`
  - scenarios: `messages.blob-stored-by-ref`, `messages.blob-content-addressed`, `working-state.output-paging`
  - verify: `go test -short -timeout 2m ./core/... ./std/outputs/ -run 'TestOutputStore'`

- [x] 12.4 `core`: Enforce `OutputStore` URL provenance, `Caps.Blobs` limits and `Build` checks; supply blob metadata to guards and reject MIME mismatches.
  - files: `core/store_blob_checks.go`, `core/store_blob_checks_test.go`, `std/guard/blob.go`, `std/guard/blob_test.go`
  - scenarios: `messages.url-only-from-user`, `messages.blob-too-large`, `build.blob-caps`, `guards.blob-guard-input`
  - verify: `go test -short -timeout 2m ./core/... ./std/guard/ -run 'TestBlobChecks'`

- [x] 12.5 `core`: Implement `NotesStore` and its memory store keyed by `NotesKey`, with session scope and version-conflict semantics.
  - files: `core/store_notes.go`, `core/store_notes_test.go`
  - scenarios: `working-state.notes-versioned`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestNotesStore'`

- [x] 12.6 `core`: Implement stored `SchemaVersion` fields, pure read-time upcasters and checkpoint version compatibility without in-place rewrites.
  - files: `core/store_schema.go`, `core/store_schema_test.go`, `core/checkpoint_compatibility.go`, `core/checkpoint_compatibility_test.go`
  - scenarios: `stores.native-checkpoint-after-adapter-upgrade`, `stores.graph-checkpoint-incompatible`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStoreSchemas'`

- [x] 12.7 `testkit/storetest`: Implement `SessionLog` conformance through injected port factories; bind the memory store from an external core test, not from the suite.
  - files: `testkit/storetest/session_log.go`, `testkit/storetest/session_log_test.go`, `core/storetest_session_log_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestSessionLog'`

- [x] 12.8 `testkit/storetest`: Implement `Checkpoints` conformance through injected port factories; bind the memory store from an external core test, not from the suite.
  - files: `testkit/storetest/checkpoints.go`, `testkit/storetest/checkpoints_test.go`, `core/storetest_checkpoints_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestCheckpoints'`

- [x] 12.9 `testkit/storetest`: Implement `Journal` conformance through injected port factories; bind the memory store from an external core test, not from the suite.
  - files: `testkit/storetest/journal.go`, `testkit/storetest/journal_test.go`, `core/storetest_journal_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestJournal'`

- [x] 12.10 `testkit/storetest`: Implement `Runs` conformance through injected port factories and clocks; bind the memory store from an external core test, not from the suite.
  - files: `testkit/storetest/runs.go`, `testkit/storetest/runs_test.go`, `core/storetest_runs_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestRuns'`

- [x] 12.11 `testkit/storetest`: Implement `AuditLog` conformance through injected port factories; bind the memory store from an external core test, not from the suite.
  - files: `testkit/storetest/audit_log.go`, `testkit/storetest/audit_log_test.go`, `core/storetest_audit_log_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestAuditLog'`

- [x] 12.12 `testkit/storetest`: Implement `EventLog` conformance through injected port factories; bind the memory store from an external core test, not from the suite.
  - files: `testkit/storetest/event_log.go`, `testkit/storetest/event_log_test.go`, `core/storetest_event_log_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestEventLog'`

- [x] 12.13 `testkit/storetest`: Implement `Schemas` conformance with recorded released-version fixtures and injected readers; bind memory readers from an external core test.
  - files: `testkit/storetest/schemas.go`, `testkit/storetest/schemas_test.go`, `testkit/storetest/schema_fixtures_test.go`, `core/storetest_schemas_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./testkit/storetest/ ./core/... -run 'TestStoretestSchemas'`

## C. Chains, guards, gate (governance before capability)

13. [x] `core`: chain-as-data (`Step`, `StepKind` incl. `KindHedge`, `ToolChain`, `ModelChain`), canonical-order validation (hedge outside fallback, inside router), `StepError`, `PromptSet` type, `Explain`/`Explanation`. — `chains.empty-chains`, `chains.step-named-failure`, `chains.prompt-strings-accounted-for`, `chains.preset-is-copyable`, `chains.cache-replace-is-free`

- [x] 13.1 `core`: Define `Step`, `StepKind` including `KindHedge`, `ToolChain`, `ModelChain`, canonical-order validation and `StepError`; preserve empty chains and free cache replacement.
  - files: `core/chains/chain.go`, `core/chains/chain_test.go`
  - scenarios: `chains.empty-chains`, `chains.step-named-failure`, `chains.cache-replace-is-free`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestChain'`
  - note: path resolved through the design package table: `core/chains`, package `chains`, which imports `core/types` only; `ModelFunc`/`ModelMiddleware`/`ModelChain` are deferred to 18.7 because they need `ModelRequest` (18.7) - 13.1 declares the tool half of the chain; `StepError` is an alias of `types.StepError`, never a redeclaration; the canonical-order validator gets an invented exported name (the spec is prose) and the ordering rule is asserted as a property of the validator, not of a runtime

- [x] 13.2 `core`: Define `PromptSet`, `Explain` and `Explanation`; account for prompt fields and validate copied preset chains without core defaults.
  - files: `core/chains/explain.go`, `core/chains/explain_test.go`, `std/presets.go`, `std/presets_test.go`
  - scenarios: `chains.prompt-strings-accounted-for`, `chains.preset-is-copyable`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestExplanation'`
  - note: `std/presets.go` joins the chunk because `chains.prompt-strings-accounted-for` needs `std.Interactive()`/`std.DefaultPrompts` and no other chunk declares them (design.md gives presets to the `std` root package); `Explanation.Release`/`Skills` names `ReleaseManifest` (21.3) - assert the fields, defer the wiring; `ExplainHandler` waits for the driver entry (row 21/22)

14. [x] `std`: permission gate skeleton — `ApprovalPolicy` per risk tier at `Resume` (`permission.self-approval-refused-high-risk`, `permission.approve-scope-required-medium`, `permission.ineligible-keeps-token`, `permission.quorum-two-approvers`, `permission.escalation-targets`, `permission.approval-audit-eligibility`, `permission.grant-inherits-policy`), scope check, `Deny` rules, `Ask` → `HumanApproval` with `ApprovalRequest` (incl. `ArgOrigins`, `DiffFromLast`), session grants, expiry, `MaxPendingApprovals`; `TaintHook` slot wired but empty. — `permission.grant-removes-the-repeat-ask`, `permission.fingerprint-change-misses-the-grant`, `permission.grant-does-not-cross-sessions-or-principals`, `permission.rich-request`, `permission.expiry-default`, `permission.queue-flood`, `chains.scope-before-decider`, `chains.denied-call-not-journaled`, `decider.failure-defaults-closed`

- [x] 14.1 `core`: Implement the permission gate skeleton, scope-first hard `Deny`, closed decider failures and `Ask` → `HumanApproval`; wire the empty `TaintHook` slot.
  - files: `core/permission/gate.go`, `core/permission/gate_test.go`, `core/types/taint.go`
  - scenarios: `chains.scope-before-decider`, `chains.denied-call-not-journaled`, `decider.failure-defaults-closed`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestPermissionGate'`
  - note: path resolved to the design package table: `core/permission`, package `permission` (the spec signature is `permission.Gate`); the empty `TaintHook` slot needs its vocabulary (`ArgTaint`, `TaintAction`, `TaintDenied`) declared in `core/types/taint.go` because no chunk owns the taint capability - taint behaviour stays M1; `chains.denied-call-not-journaled` asserts against `core/stores.Journal` (the journal step is 15.1); `decider.failure-defaults-closed` asserts a closed Deny, not runtime emission

- [x] 14.2 `core`: Define `ApprovalPolicy`, `ApprovalRequest`, `Eligibility` and `ApprovalPolicy.MaxPending`; preserve tokens on ineligible approval and carry `ArgOrigins`, `DiffFromLast` and eligibility audit data.
  - files: `core/permission/approval.go`, `core/permission/approval_test.go`
  - scenarios: `permission.ineligible-keeps-token`, `permission.rich-request`, `permission.approval-audit-eligibility`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestApprovalContract'`
  - note: `core/limits.go` dropped: `MaxPending` is an `ApprovalPolicy` field, not a `RunLimits` field; the approval audit kinds (approval, granted_by_scope) are added to the landed `core/stores/audit.go` by this chunk, which is their single writer; `permission.rich-request` asserts the `ApprovalRequest` builder (`ArgOrigins`, `DiffFromLast`), not the `Suspended` emission (21/22)

- [x] 14.3 `std/permission`: Supply risk-tier `ApprovalPolicy` defaults for `Resume`, separate high-risk originators, require medium-risk approval scope and enforce distinct-approver quorum.
  - files: `std/permission/policy.go`, `std/permission/policy_test.go`
  - scenarios: `permission.self-approval-refused-high-risk`, `permission.approve-scope-required-medium`, `permission.quorum-two-approvers`
  - verify: `go test -short -timeout 2m ./std/permission/ -run 'TestApprovalPolicy'`
  - note: the policy evaluator is tested directly: `Resume` is 24.1, so the scenarios' `Resume`-level THENs are asserted against the evaluator's decision and the deferral recorded

- [x] 14.4 `std/permission`: Implement policy-checked `ApproveScope` and session `Grant` matching by tool, fingerprint and subject.
  - files: `std/permission/grant.go`, `std/permission/grant_test.go`, `core/tool_fingerprint.go`
  - scenarios: `permission.grant-inherits-policy`, `permission.grant-removes-the-repeat-ask`, `permission.fingerprint-change-misses-the-grant`, `permission.grant-does-not-cross-sessions-or-principals`
  - verify: `go test -short -timeout 2m ./std/permission/ -run 'TestSessionGrant'`
  - note: grants are stored through an injected `GrantStore` interface with a memory implementation (`std/permission` may not reach session metadata, which has no port); `ToolSpec.FingerprintFields` and `WithFingerprintFields` (tools spec:112, prose only) are declared here because `permission.fingerprint-change-misses-the-grant` needs them, and this chunk is their single writer; `MaxGrantTTL` and the grant audit record land here

- [x] 14.5 `std/permission`: Implement `RejectOnExpiry`, `EscalateOnExpiry` targets and `Waker` scheduling; enforce `ApprovalPolicy.MaxPending` per subject and tenant.
  - files: `std/permission/expiry.go`, `std/permission/expiry_test.go`, `std/permission/pending.go`, `std/permission/pending_test.go`
  - scenarios: `permission.expiry-default`, `permission.escalation-targets`, `permission.queue-flood`
  - verify: `go test -short -timeout 2m ./std/permission/ -run 'TestApprovalQueue'`
  - note: `Waker` (24.2) arrives as a narrow consumer-owned interface declared in `std/permission`; `ExpiryVerdict`/`ExpiryTarget` naming follows the spec prose; `permission.expiry-default` asserts the policy decision and the injected target call, not the `Failed(Permanent)` result (22) or the `gohan.approval.expired` problem (28)

15. [x] `std`: journal step with fingerprint pinning, cancel shield (`context.WithoutCancel` for `SideEffect`), read-back, uncertainty surfacing, `Verify` reconciliation. — `stores.same-transaction-journal`, `stores.late-commit`, `stores.read-back-offered`, `stores.uncertainty-surfaced`, `stores.fingerprint-after-compaction`, `stores.crash-window`, `tools.verify-reconciles-unknown`, `tools.verify-read-only`, `tools.verify-error`, `tools.verify-on-recover`, `streams.disconnect-during-side-effect`, `chains.read-only-tools-pay-nothing`

- [x] 15.1 `std`: Implement the journal step with canonical fingerprinting, intent-key pinning and crash-window replay; reuse row 9's core `Fingerprint` and `CallKey`.
  - files: `std/journal.go`, `std/journal_test.go`
  - scenarios: `stores.late-commit`, `stores.fingerprint-after-compaction`, `stores.crash-window`
  - verify: `go test -short -timeout 2m ./std/ -run 'TestJournalIntent'`
  - note: `std/` root is package `std` (first file; `std/presets.go` from 13.2 is its sibling and is sequenced before this row); the canonical fingerprint helper is invented and frozen here (`CanonicalFingerprint(tool, args)` per stores spec:305) because the spec gives a formula and no function; key pinning reuses the journal port's `ByFingerprint`/`Entry.Key`; `stores.crash-window` asserts the store-level Reserved -> re-execute branch, never real process death; the three journal metrics are row 28

- [x] 15.2 `std`: Apply `context.WithoutCancel` only to `SideEffect`, persist results before cancellation and exempt `ReadOnly` tools from journal and shield work.
  - files: `std/shield.go`, `std/shield_test.go`
  - scenarios: `streams.disconnect-during-side-effect`, `chains.read-only-tools-pay-nothing`
  - verify: `go test -short -timeout 2m ./std/ -run 'TestCancelShield'`

- [x] 15.3 `std`: Reconcile `Unknown` through read-only `Verify` after execution and on `Resume`/`Recover`; journal `/verify` calls without re-executing effects.
  - files: `std/verify.go`, `std/verify_test.go`
  - scenarios: `tools.verify-reconciles-unknown`, `tools.verify-read-only`, `tools.verify-error`, `tools.verify-on-recover`
  - verify: `go test -short -timeout 2m ./std/ -run 'TestVerifyReconciliation'`
  - note: `tools.verify-on-recover` drives `Recover` (27.1) and `Resume` (24.1), which do not exist: assert the reconciliation function at the std level and record the wiring deferral; the `/verify` journal key is minted by the caller - the `Journal` port has no field for it

- [x] 15.4 `std`: Offer `ReadBack` after `Unknown` and surface unresolved `CallKey` entries through `UncertainOutcomeError` and `Done.Uncertain`.
  - files: `std/uncertainty.go`, `std/uncertainty_test.go`
  - scenarios: `stores.read-back-offered`, `stores.uncertainty-surfaced`
  - verify: `go test -short -timeout 2m ./std/ -run 'TestUncertainty'`
  - note: `stores.uncertainty-surfaced` names `Flow.Invoke`/`Conversation.Done` (23.1/23.2): assert the returned `UncertainOutcomeError` and `Done.Uncertain` value, and record the emission deferral; `ReadBack` hint rendering uses `PromptSet` fields from 13.2 (sequenced before)

- note: `stores.same-transaction-journal` needs `adapter/postgres` and is deferred to M1 (`deferred_to` in `openspec/scenarios.json`).

16. [x] `std`: guards — `GuardInput`, stages, rules-based input/context/description guards, `Buffered`/`Windowed` output, `Fallback`, fencing by `Origin`. — `guards.input-injection-blocked`, `guards.indirect-injection`, `guards.windowed-output`, `guards.intermediate-turns-unguarded`, `guards.notes-poisoning-blocked`, `guards.provider-output-fenced`, `guards.origin-not-caller-settable`, `tools.poisoned-description`, `decider.observable`

- [x] 16.1 `std/guard`: Implement rules-based input, tool-result and description guards using core `GuardInput` and stages; record observable decider decisions.
  - files: `core/guards/guard.go`, `core/guards/guard_test.go`, `std/guard/rules.go`, `std/guard/rules_test.go`, `std/guard/description.go`, `std/guard/description_test.go`
  - scenarios: `guards.input-injection-blocked`, `guards.indirect-injection`, `tools.poisoned-description`, `decider.observable`
  - verify: `go test -short -timeout 2m ./std/guard/ -run 'TestGuardRules'`
  - note: `GuardInput`, `GuardAction`, `GuardVerdict`, `Guard`, `OutputMode` and `Fallback` are declared by this chunk in `core/guards` (design package table) because no other chunk owns them; `GuardStage` and `GuardBlockedError` already live in `core/types` from row 3 and are referenced, not moved - the design table's placement is superseded by the landed code; `tools.poisoned-description` asserts the checker returns `ErrToolDescription` (`Build` is 21.1); `decider.observable` asserts the recorded decision (the span is 28.1); `std/guard/blob.go` from 12.4 is not touched and its names are not redeclared

- [x] 16.2 `std/guard`: Implement context guards and fencing by chain-assigned `Origin`; reject poisoned notes and fence provider output with `PromptSet` fields.
  - files: `std/guard/context.go`, `std/guard/context_test.go`, `std/guard/origin.go`, `std/guard/origin_test.go`
  - scenarios: `guards.notes-poisoning-blocked`, `guards.provider-output-fenced`, `guards.origin-not-caller-settable`
  - verify: `go test -short -timeout 2m ./std/guard/ -run 'TestContextProvenance'`
  - note: fencing is asserted on given inputs with `PromptSet` from 13.2; the assembler path (`SlotSession`, 19.1) is the caller, so `guards.provider-output-fenced` asserts the fence function; `guards.origin-not-caller-settable` asserts that a guard refuses an input whose `Origin` claims a chain-assigned kind, and the chain-side overwrite is recorded as a runtime deferral; the `gohan.guard.context_rejected` counter is row 28

- [x] 16.3 `std/guard`: Implement `Buffered` and `Windowed` user-facing output guards with `Fallback`; leave intermediate tool-call turns unguarded.
  - files: `std/guard/output.go`, `std/guard/output_test.go`
  - scenarios: `guards.windowed-output`, `guards.intermediate-turns-unguarded`
  - verify: `go test -short -timeout 2m ./std/guard/ -run 'TestOutputGuard'`
  - note: `guards.windowed-output` asserts the `Buffered`/`Windowed` behaviour and the `GuardVerdict` it returns; the `Done(guard_blocked)` emission is 23.2 - record the deferral; the `Interactive -> Windowed 64` default uses `std.Interactive()` from 13.2 (sequenced before); the guards `Fallback` type is distinct from the model `Fallback` concept and must not be aliased into the driver

17. [x] `std`: tool policy — `Trusted`/`Untrusted`, `MaxEffect`, pinned manifest with hash drift, `ToolFilter` (narrow-only, deterministic), `Deferred` + `search_tools`. — `tools.rug-pull`, `tools.untrusted-effect-cap`, `tools.tool-filter-per-turn`, `tools.not-assembled-until-discovered`, `tools.activation-persists-across-resume`, `tools.governed-while-deferred`, `messages.deterministic-tool-filter-on-replay`

- [x] 17.1 `core`: Declare `ToolPolicy`, `Trusted`, `Untrusted` and `MaxEffect`; apply the configured effect cap before gate evaluation.
  - files: `core/tool_policy.go`, `core/tool_policy_test.go`
  - scenarios: `tools.untrusted-effect-cap`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestToolPolicy'`
  - note: `ToolPolicy` stays in the driver package (`gohan`) because its `DescribeGuard` field is a `guards.Guard` and `core/types` is the floor: it may not import `core/guards`; `Trusted`/`Untrusted` already live in `core/types/tool.go` and are referenced, not redeclared; the cap is applied before gate evaluation, so this chunk lands after 14.1 in the same rung; the `gohan.tool.effect_capped` metric is row 28

- [x] 17.2 `std`: Implement pinned tool manifests, hash-drift checks and narrow-only per-turn `ToolFilter`; enforce deterministic filtered sets on `Replay`.
  - files: `std/manifest.go`, `std/manifest_test.go`, `std/tool_filter.go`, `std/tool_filter_test.go`
  - scenarios: `tools.rug-pull`, `tools.tool-filter-per-turn`, `messages.deterministic-tool-filter-on-replay`
  - verify: `go test -short -timeout 2m ./std/ -run 'TestToolManifestAndFilter'`
  - note: `depends_on` 19.1 dropped: the assembler is this chunk's *consumer*, not its dependency - expose the narrow-only filter as a pure function taking the per-turn context; `WithPinnedManifest` is a `Build` option (21.1) and the drift check is exposed as a pure function; `Replay` returning `ErrToolSetDrift` is 22 - assert the check; the manifest hash algorithm and the pinned-file shape are invented and frozen here (the spec declares neither)

- [x] 17.3 `std/toolsearch`: Implement `Deferred` discovery through `search_tools`, persist activation across resume and preserve scope checks after discovery.
  - files: `std/toolsearch/search.go`, `std/toolsearch/search_test.go`, `std/toolsearch/activation.go`, `std/toolsearch/activation_test.go`
  - scenarios: `tools.not-assembled-until-discovered`, `tools.activation-persists-across-resume`, `tools.governed-while-deferred`
  - verify: `go test -short -timeout 2m ./std/toolsearch/ -run 'TestDeferredTools'`
  - note: the active tool set is persisted in `Checkpoint.Data` (`map[string]any`), which exists - no store shape change; `tools.not-assembled-until-discovered` asserts the deferred set is absent from the assembled set until discovered, via the filter from 17.2 (sequenced before), not through the assembler (19.1) or `Explain` (13.2 supplies the accounting); `tools.governed-while-deferred` reuses the scope check from 14.1; `MaxToolContextShare` is prose - the `Build` share warning is 21 territory

## D. Model side

18. [x] `core`: `ModelProfile` (incl. `Keys`), `ProviderKeySource`/`ProviderCredential`/`ErrNoProviderKey`, `Build` key validation (`model.tenant-key-selected`, `model.tenant-key-missing-fails-closed`, `model.no-fallback-on-auth-error`, `model.key-validated-at-build`, `identity.tenant-for-key-from-ctx`), `Caps`, `Pricing`, error class normalisation, `LatencyClass`. `std`: `retry.Exponential`/`RetryAfter`, `std/limit` local limiter, circuit breaker, `Fallback` before first chunk, per-endpoint wrapping. — `model.429-fails-over-without-retry`, `model.5xx-retries-then-fails-over`, `model.breaker-opens`, `model.early-break-releases`, `model.cancel-returns-promptly`, `model.first-chunk-timeout-transient`, `model.idle-timeout-permanent`, `model.partial-terminal-on-replay`, `chains.no-retry-after-first-chunk`, `chains.fallback-charged`, `chains.per-endpoint-limits`

- [x] 18.1 `std/keys`: Add env- and map-backed `ProviderKeySource`, `ProviderCredential`, `ErrNoProviderKey`; select `ModelProfile.Keys` from `PrincipalFrom(ctx)` and refuse auth fallback.
  - files: `std/keys/keys.go`, `std/keys/keys_test.go`, `core/types/model_keys.go`
  - scenarios: `model.tenant-key-selected`, `model.tenant-key-missing-fails-closed`, `model.no-fallback-on-auth-error`, `identity.tenant-for-key-from-ctx`
  - verify: `go test -short -timeout 2m ./std/keys/ -run 'TestProviderKeys'`
  - note: the ports (`ProviderCredential`, `ProviderKeySource`, `ProviderKeyValidator`) live in the type floor, not in `std/keys`, because the design table puts ports in `core/types` and `core/build_keys.go` must reach them without importing `std`; `ProviderKeyValidator` is declared here because the spec names it and no chunk owned it; `ErrNoProviderKey` already exists in `core/types/errors.go:58` and is referenced, never redeclared; `std/keys` implements the env- and map-backed source; the scenarios' `Usage.KeyID` and provider-class halves have no adapter in M0 - assert the credential the source returns and record the rest; `depends_on` 18.6

- [x] 18.2 `core`: Validate platform credentials through `ProviderKeyValidator` during `Build`.
  - files: `core/build_keys.go`, `core/build_keys_test.go`
  - scenarios: `model.key-validated-at-build`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestBuildProviderKeys'`
  - note: `Build` itself is 21.1, so this chunk exposes the validation seam as a function over profiles and a source; `depends_on` 18.1, not 21.1 - the wiring is recorded, not implemented; the depguard rule forbids `core` importing `std`, so the seam takes the floor's `ProviderKeySource` interface and 21.1 passes `std/keys` in

- [x] 18.3 `std/retry`: Add `retry.Exponential` and `RetryAfter`; retry transient failures before the first chunk only, then expose exhaustion to `Fallback`.
  - files: `std/retry/retry.go`, `std/retry/retry_test.go`
  - scenarios: `model.5xx-retries-then-fails-over`, `chains.no-retry-after-first-chunk`
  - verify: `go test -short -timeout 2m ./std/retry/ -run 'TestRetryPolicy'`
  - note: `retry.Exponential` and `RetryAfter` have no Go shape in any spec: freeze both, and return the model middleware the floor declares in 18.7; retry applies before the first chunk only and exhaustion surfaces to the router's fallback (18.5) - assert the policy's attempt sequence, and record the failover half; `depends_on` 18.7

- [x] 18.4 `std/limit`: Add the local limiter and `MaxInFlight` bulkhead with independent per-endpoint wrapping.
  - files: `std/limit/model.go`, `std/limit/model_test.go`
  - scenarios: `chains.per-endpoint-limits`
  - verify: `go test -short -timeout 2m ./std/limit/ -run 'TestEndpointLimits'`
  - note: the limiter takes its per-endpoint ceiling as a constructor argument: `ModelProfile.MaxInFlight` (18.6) and its wiring are the route/Build layers' job, so this chunk never reads a profile; per-endpoint independence is the scenario's assertion; `LimitExceededError` already exists in the floor; `depends_on` 18.6

- [x] 18.5 `std/route`: Add circuit breaker, static/by-`LatencyClass` routing and `Fallback` before the first chunk; charge the successful endpoint's usage.
  - files: `std/route/route.go`, `std/route/breaker.go`, `std/route/fallback.go`, `std/route/route_test.go`
  - scenarios: `model.breaker-opens`, `model.429-fails-over-without-retry`, `chains.fallback-charged`
  - verify: `go test -short -timeout 2m ./std/route/ -run 'TestEndpointRouting'`
  - note: no `Router`, `Breaker` or `BreakerPolicy` interface exists in any spec: freeze the shapes, the open threshold, the window and the half-open delay, and record them; the scenarios' metric halves (`gohan.model.failover{class=...}`) are row 28 and the charging half of `chains.fallback-charged` needs `RunLimits` (23.7) - assert the routing decision, the fallback-once-per-chunk ordering and the endpoint choice; `depends_on` 18.3, 18.4, 18.6, 18.7

- [x] 18.6 `core`: Add the `Model` port, `ModelProfile` (with `Keys`), `KeyMode`, `Caps`, `Pricing`, `Fidelity`, `ProviderToolCap`, `CacheMode`, `CompactionMode`, the `TokenCounter` port and the budget value types; move `BlobCaps` to the type floor with an alias in `core/stores`.
  - files: `core/types/model_profile.go`, `core/types/model.go`, `core/types/fidelity.go`, `core/types/tokens.go`, `core/types/model_profile_test.go`, `core/types/tokens_test.go`, `core/stores/blob_checks.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./core/... -run 'TestModelProfile'`
  - note: this is the row's vocabulary root and carries no scenario - it exists so 18.1, 18.3, 18.4, 18.9, 19.2 and 19.3 have something to compile against; `Fidelity` is declared here because `Caps.Fidelity` needs it and no other chunk owns the type (messages spec:196); `TokenCounter` and the budget value types live in the floor because the driver declares no shared type and `core` may not import `std`; `BlobCaps` moves out of `core/stores/blob_checks.go` with an alias left behind, because `Caps.Blobs` needs it and the floor may not import `stores`; the stream semantics of the old 18.6 block moved to 18.9 so each dispatch stays inside one reviewable unit

- [x] 18.9 `core`: Enforce model iterator release, cancellation and first-chunk/idle timeout semantics.
  - files: `core/model_stream.go`, `core/model_stream_test.go`
  - scenarios: `model.early-break-releases`, `model.cancel-returns-promptly`, `model.first-chunk-timeout-transient`, `model.idle-timeout-permanent`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestModelStream'`
  - note: split out of the old 18.6 block; `testing/synctest` for the three timeout and cancellation scenarios; the transient/permanent class split is the spec's, so assert the class, not an emitted call

- [x] 18.7 `core`: Declare `ModelRequest`, `ModelOptions`, `ToolChoice` and `AffinityKeyStrategy` in `core/types/model_request.go` (`openspec/specs/model/spec.md` §6.5), now that `ToolSpec` (row 12) and the message vocabulary exist; keep the marshaling of an assembled request stable, because the record/replay key is a hash of it (`docs/design/testing.md`).
  - files: `core/types/model_request.go`, `core/types/model_request_test.go`, `core/types/middleware.go`, `core/chains/model_chain.go`, `core/chains/model_chain_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./core/... -run 'TestModelRequest'`
  - note: this chunk also lands the chain's model half that row 13 deferred: `ModelFunc` and `ModelMiddleware` join the tool half in the type floor (`core/types/middleware.go`) so a policy package can wrap a model call without importing a peer leaf, and `core/chains/model_chain.go` declares `ModelChain` plus the aliases; the assembled request's marshaling is frozen here because the record/replay key is a hash of it; verify selector covers both test functions

- [x] 18.8 `core`: Exclude terminal partial assistant messages with `FinishError` from provider history on replay and continuation.
  - files: `core/model_history.go`, `core/model_history_test.go`
  - scenarios: `model.partial-terminal-on-replay`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestModelTerminalHistory'`
  - note: renumbered from the plan's duplicate 18.7. The exclusion is a pure projection over a history slice; the `Flow`/`Conversation` emission that consumes it is row 23, so record that half. No `Message` field carries a finish state, so the projection reads a message meta key it freezes (`Meta["finish"]`, typed `FinishError`/`FinishReason`) - **row 23 must set that key when it emits a terminal partial**, and that key is the contract until a spec declares a real field

19. [x] `std`: `StablePrefix` assembler, slots, `ContextProvider`, `CacheBreak` rules, `Truncate` projection (persisted compaction is M2), `std/tokens.Heuristic` + `ContextBudget`. — `model.budget-reserves-output`, `model.heuristic-deterministic`, `model.token-counter-optional`, `assembly.prefix-stability`, `assembly.tool-order`, `assembly.truncate-policy`, `model.context-re-fit-on-fallback`

- [x] 19.1 `std`: Add `StablePrefix` assembly, slots, `ContextProvider` integration and `CacheBreak` rules with stable tool ordering.
  - files: `core/types/assembly.go`, `core/types/assembly_test.go`, `std/assembly.go`, `std/assembly_test.go`
  - scenarios: `assembly.prefix-stability`, `assembly.tool-order`
  - verify: `go test -short -timeout 2m ./std/ -run 'TestStablePrefix'`
  - note: `ContextSlot`, the slot constants and `ContextProvider` live in the floor, per the design table; the assembler must not redeclare `std.ToolFilter` (17.2 already exports one with a different shape) - take it as the floor's selector type and adapt at the call site; `CacheBreak` is placed twice, after `SlotStatic` and after `SlotSession`, per the assembly spec; prefix stability is a byte comparison of the assembled request; `depends_on` 18.7

- [x] 19.2 `core`: Add `ContextBudget` from `openspec/specs/model/spec.md` §Contract/6.5; reserve output and margin, and use optional `TokenCounter` only at the compaction decision.
  - files: `core/types/context_budget.go`, `core/types/context_budget_test.go`
  - scenarios: `model.budget-reserves-output`, `model.token-counter-optional`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestContextBudget'`
  - note: the driver declares no shared type, so the budget arithmetic's home is the floor, beside the budget value types 18.6 declares; the `TokenCounter` port is optional and consulted only at the compaction decision; `Reserved` has no spec formula - freeze it and the rounding; assert the 200000-8000-margin arithmetic the scenario pins; `depends_on` 18.6

- [x] 19.3 `std/tokens`: Add deterministic `Heuristic` from `openspec/specs/model/spec.md` §Contract/Token budget.
  - files: `std/tokens/heuristic.go`, `std/tokens/heuristic_test.go`
  - scenarios: `model.heuristic-deterministic`
  - verify: `go test -short -timeout 2m ./std/tokens/ -run 'TestHeuristic'`
  - note: implements the floor's `TokenCounter`; the image-token rule has no `Caps` field to read, so freeze the constant and record it; the heuristic must be deterministic across calls and independent of `std/context`; `depends_on` 18.6

- [x] 19.4 `std/context`: Add `Truncate` projection from `openspec/specs/context/spec.md` §Contract/Two stages with different persistence; preserve whole turns and prefix, and re-fit each fallback profile without persisted compaction.
  - files: `std/context/truncate.go`, `std/context/truncate_test.go`
  - scenarios: `assembly.truncate-policy`, `model.context-re-fit-on-fallback`
  - verify: `go test -short -timeout 2m ./std/context/ -run 'TestTruncate'`
  - note: the spec names the projection and no type: freeze `Truncate`'s shape and record it; `model.context-re-fit-on-fallback` asserts the pure re-fit call for a second profile, with the router (18.5) as the caller; `assembly.truncate-policy` asserts whole turns and the prefix survive and that the session log is untouched; keep test function names distinct from row 26's future file in this package; `depends_on` 19.1, 19.2
- [x] 21.4 `std`: Wire the presets into the driver's option set.
  - files: `std/presets.go`, `std/presets_test.go`
  - scenarios: none
  - verify: `go test -short -timeout 2m ./std/ -run 'TestPresetOptions'`
  - note: no chunk owned the adapter from the row-13 presets to the driver's options, and `build/spec.md:56` says presets *are* options; `std` may import the driver (only `core -> std` is denied), so the presets expose `func (p Preset) Options() []gohan.Option`; this chunk lands after 21.1 declares the option type and keeps the presets' structural placeholders honest - the pass-through middlewares are named as such

20. [x] `std/structured`: `Partial[Out]` (`structured-output.result-delta-partial`, `structured-output.partial-never-validated`), `ToolSchema`, `ValidateRepair`, `ReasonFirst`, app-side validation, strict schema derivation, refusal-as-JSON, truncated-args handling. — `structured-output.validate-and-repair`, `structured-output.bounds-validated-after-constrained-decoding`, `structured-output.refusal-as-json`, `structured-output.truncated-tool-args`, `structured-output.reason-first`, `structured-output.strict-schema`

- [x] 20.1 `std/structured`: Add `Partial[Out]` (validated) and `PartialView[Out]` returning `PartialValue` (deep-partial, never validated) for accumulated `ResultDelta` text; prevent partial values from validation, storage or `Done.Result`.
  - files: `std/structured/partial.go`, `std/structured/partial_test.go`
  - scenarios: `structured-output.result-delta-partial`, `structured-output.partial-never-validated`
  - verify: `go test -short -timeout 2m ./std/structured/ -run 'TestPartial'`
  - note: the spec declares a non-generic `PartialView(acc string) PartialValue` (spec:31): follow the spec, not the plan's `PartialView[Out]`; `ErrStructuredOutput` already exists in the floor and is referenced, never redeclared (a second sentinel breaks `errors.Is`); `ToolUse` carries no truncation field, so truncation is derived from the finish reason; the accumulator is asserted directly because `Extract[Out]` is 23.8 and `Done.Result` is 23.1; the refusal metric is row 28

- [x] 20.2 `std/structured`: Add `ValidateRepair`, app-side bounds validation and refusal-as-JSON classification with bounded repair turns.
  - files: `std/structured/validate.go`, `std/structured/repair.go`, `std/structured/validate_test.go`
  - scenarios: `structured-output.validate-and-repair`, `structured-output.bounds-validated-after-constrained-decoding`, `structured-output.refusal-as-json`
  - verify: `go test -short -timeout 2m ./std/structured/ -run 'TestValidateRepair'`
  - note: `ValidateRepair` has no spec shape: freeze it and record the name; `structured-output.refusal-as-json` asserts the classification, since no metric API exists (row 28); the repair turn is bounded here but executed by the Drive loop (22.4) - assert the decision and the `ToolResult`, and record the execution half

- [x] 20.3 `std/structured`: Add `ToolSchema`, strict schema derivation and `ReasonFirst` request configuration with `Explain` reporting.
  - files: `std/structured/schema.go`, `std/structured/reason_first.go`, `std/structured/schema_test.go`
  - scenarios: `structured-output.strict-schema`, `structured-output.reason-first`
  - verify: `go test -short -timeout 2m ./std/structured/ -run 'TestStructuredStrategy'`
  - note: no second schema walker: reuse the floor's `SchemaBuilder` from row 6.2; `strict: true` is emitted by no spec field, so freeze the flag and its emission here; `ReasonFirst` has no shape either - freeze it; the `Explain` half defers to the driver entry (row 21); `jsontext` has no tolerant or incomplete mode, so closing an unterminated string or container is hand-written and unit-tested

- [x] 20.4 `std/structured`: Reject truncated tool arguments before execution; send a truncation result and retry once with larger `MaxTokens`.
  - files: `std/structured/truncated.go`, `std/structured/truncated_test.go`
  - scenarios: `structured-output.truncated-tool-args`
  - verify: `go test -short -timeout 2m ./std/structured/ -run 'TestTruncatedToolArgs'`
  - note: the truncated-arguments rule is derived from the finish reason (`FinishMaxTokens`), because `ToolUse` has no field for it; the single retry with a larger output allowance is decided here and executed by the Drive loop (22.4) - assert the predicate and the truncation result, and record the execution half

21. [x] `core`: `Build` — option set, strategy resolution (flow > profile > caps), impossible-combination rejection, provider-tool capability check (`build.provider-tool-unsupported`), resolved-matrix log, `ReleaseManifest` + `ID`, fidelity gate. — `build.impossible-combination`, `build.resolved-matrix`, `model.incompatible-fallback-rejected-at-build`, `messages.fidelity-gate-at-build`, `messages.reasoning-dropped-across-providers`

- [x] 21.1 `core`: Add `Build` options including `SequentialTools()` and `MaxParallelTools`; resolve flow > profile > caps and reject impossible combinations, unsupported provider tools and incompatible fallback profiles.
  - files: `core/build.go`, `core/build_options.go`, `core/build_test.go`
  - scenarios: `build.impossible-combination`, `build.provider-tool-unsupported`, `model.incompatible-fallback-rejected-at-build`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestBuildStrategies'`
  - note: the driver may not import `std` (depguard denies it for all of `core/**`), so every option takes a floor type and the caller passes the policy value: `WithProviderKeys(types.ProviderKeySource)`, the router/retry/limiter as `WithModelMiddleware(...types.ModelMiddleware)`, `WithEstimator(types.TokenEstimator)`, `WithLogger(*slog.Logger)` (the D59 baseline decision, absent from the spec's option list); `WithPinnedManifest` needs the manifest value type moved to the floor (the `BlobCaps` pattern) with an alias left in `std/manifest.go`; options whose value types no spec declares (`Budget`, `Redactor`, `Sandbox`, `Flags`, `Detach`, `Skill`/`SkillSource`, `PromptRef`, `flowdef.Definition`) are deferred with their spec line and their names stay reserved - do not invent the types; `build.impossible-combination` asserts the resolver's own error, because the flow constructor is 23.1; `AllowDrop` stays a `std/flow` option per the messages spec, not a Build option; `MaxParallelTools` carries its own integer because `RunLimits` is 23.7; the `Stack` store façades (`Sessions`, `UpdateSession`, `ForkSession`, `DeleteSession`) move to row 22 and are recorded there

- [x] 21.2 `core`: Add the `Build` fidelity gate and cross-provider reasoning projection without editing opaque reasoning blocks.
  - files: `core/build_fidelity.go`, `core/fidelity_projection.go`, `core/build_fidelity_test.go`
  - scenarios: `messages.fidelity-gate-at-build`, `messages.reasoning-dropped-across-providers`, `model.undeclared-fidelity`, `build.opaque-compaction-fallback`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestBuildFidelity'`
  - note: this chunk takes two scenarios the plan left unowned: `model.undeclared-fidelity` and `build.opaque-compaction-fallback`; `messages.fidelity-gate-at-build` asserts the drop list per profile's `Caps.Fidelity`, since `gohan.block.dropped{kind}` is row 28

- [x] 21.3 `core`: Compute `ReleaseManifest` and `ID`; expose the manifest and log the resolved flow × profile × strategy matrix once at startup. **GATE**
  - files: `core/release_manifest.go`, `core/build_matrix.go`, `core/build_matrix_test.go`
  - scenarios: `build.resolved-matrix`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestBuildResolvedMatrix' && task spec:types && task spec:coverage && task lint && task test`
  - note: `release.id-deterministic` is assigned here - the plan named it in no chunk; `ReleaseManifest`'s skill, chain and definition sections have no source in M0 (skills and flowdef are later milestones): hash what exists, name the absent sections in the manifest, and record it; this chunk is the row's GATE and its verify already runs the spec targets

## E. Runtime, flow, suspension

22. [x] `core`: `RunLimits` and the `WithLimits` option; the native runtime - `Stepper`, `Runtime`, `State`, `Status`, `Drive`/`DriveResume` - at effect granularity with the tool batch protocol; the `Stack` store façades. - `limits.unbounded-rejected`, `runtime.plain-answer`, `runtime.batch-gate-first`, `runtime.batch-limit-before-execute`, `runtime.readonly-parallel`, `runtime.side-effects-sequential`, `runtime.batch-ask-after-allowed`, `runtime.batch-one-result-per-call`, `runtime.parallel-tools-cap`, `runtime.sequential-tools-hint`, `runtime.tool-round-trip`, `runtime.parallel-calls-ordering`, `runtime.max-turns`, `runtime.cancellation`, `runtime.suspend-order`, `runtime.append-before-tool`, `runtime.done-after-finish` (17 IDs, chunk-disjoint)

- note: the runtime capability's Go block is illustrative (`runtime/spec.md:16`), so the names below may change without an ADR; its batch rules, its ordering rules and every WHEN/THEN are normative. The runtime is a **leaf** package - `core/runtime/` per `design.md:24` - never the driver. `core/drive*.go` is the driver package `gohan`, which declares no shared type (ADR-0139 rule 3), so every vocabulary type it names gets an alias in `core/aliases.go`.
- note: neither `core/runtime` nor `core/drive*.go` may import `std` (depguard). Every std dependency arrives as a function value or an interface the caller passes: the assembler (`std/stableprefix.Assemble`), the journal, the cancel shield, the output verifier, the tool filter. `AssembleInput` moves from `std/assembly.go:15` into `core/types` with an alias left behind, because `AgentRun.Assemble` names it. The ctx seams the runtime needs (`RunInfoFrom`, `WithPrincipal`/`PrincipalFrom`, `WithIdempotencyKey`) live in the driver today and move to the floor for the same reason.
- note: `runtime.message-round-trip` (`runtime/spec.md:326`) requires conversion to eino's `AgenticMessage` and adk-go's `genai.Content`; M0 carries no adapter, so its owner becomes M2 in `scenarios.json`. `runtime.governed-components-in-eino-graph` was already `deferred_to: M2`.
- note: the component-event sink (`runtime/spec.md:86`) is declared by this row in `core/types`, beside the event payloads, and its shape is now normative in that spec (ADR-0143): a leaf runtime cannot reach the driver's notifier. The runtime emits runtime-originated events only - `TextDelta`, `ToolStarted`, `ToolFinished` and the turn counter arrive through the sink.

- [x] 22.1 `core`: Declare `RunLimits` in the floor with its zero-value defaults, and the `WithLimits` option that refuses an unbounded run at build time.
  - files: `core/types/limits.go`, `core/limits.go`, `core/limits_test.go`
  - scenarios: `limits.unbounded-rejected`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRunLimits'`
  - note: `design.md:14` puts the value type in `core/types`, and the spec's illustrative `AgentSpec` already carries `RunLimits` (`runtime/spec.md:23`). Fields: `limits/spec.md:19`; zero-value defaults: `limits/spec.md:44`. `limits.unbounded-rejected` (`limits/spec.md:57`) asserts that `Build` fails naming the flow when the limits are zero and no preset installs them - a build-time assertion, not a runtime one. Enforcement (hard-cost abort, wall clock, tree cost) is 23.7; the runtime's per-turn counting is 22.5.

- [x] 22.2 `core`: Implement `Stepper`, `Runtime`, illustrative `State`/`Status`, `Drive`/`DriveResume` at effect granularity; preserve `Message` blocks and history versions.
  - files: `core/runtime/runtime.go`, `core/types/assembly.go`, `core/types/sink.go`, `core/drive.go`, `core/runtime_test.go`
  - scenarios: `runtime.plain-answer`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRuntimeState'`
  - note: the declarations land in the leaf `core/runtime`; `Drive`/`DriveResume` are functions and may live in the driver package. This chunk also moves `AssembleInput` and the identity ctx seams into the floor, leaving aliases behind, and declares the `Sink` port plus its run-scoped ctx constructor (ADR-0143). The principal seam is `runtime/spec.md:99`: `PrincipalFrom(ctx)` or `ErrNoPrincipal` unless `AllowAnonymous`.

- [x] 22.3 `core`: Implement the tool batch protocol: gate and reserve limits before execution, decide the whole batch before the first call runs, answer `Ask` in call order, and produce one result per call.
  - files: `core/runtime/runtime_batch.go`, `core/permission/gate.go`, `core/runtime_batch_test.go`
  - scenarios: `runtime.batch-gate-first`, `runtime.batch-limit-before-execute`, `runtime.batch-ask-after-allowed`, `runtime.batch-one-result-per-call`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRuntimeBatchProtocol'`
  - note: `permission.Gate` returns only a `types.ToolMiddleware` today (`core/permission/gate.go:82`) and runs inside the call, which cannot deny before the first call executes - this chunk exports a decision seam on that package. `Deny` and `TaintDenied` produce `Failed(Permanent)` results at once (`runtime/spec.md:114`). `Ask` suspends one at a time in call order, `EditArgs` unchanged (`:116`), and the resume half ("on resume only the approved call executes") is 24.1. Frozen literals: the `not_executed:` reason prefix (`:124`) and `not_executed: rejected by <approver>`.

- [x] 22.4 `core`: Execute `ReadOnly` calls concurrently up to the resolved cap and side effects sequentially, journalled one at a time; consume row 21's options through the resolved strategy.
  - files: `core/runtime/runtime_schedule.go`, `core/runtime_schedule_test.go`
  - scenarios: `runtime.readonly-parallel`, `runtime.side-effects-sequential`, `runtime.parallel-tools-cap`, `runtime.sequential-tools-hint`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRuntimeScheduling'`
  - note: read the cap from `Stack.ResolveStrategies`' `StrategyPlan` (`core/build.go:61`; the options are `core/build_options.go:104` and `:92`), never from the config directly, and the profile term is `p.Caps.ParallelTools && !s.sequential`. Side effects run one at a time under the injected cancel shield with the journal reserve/complete in call order (`runtime/spec.md:115`). Results reach the next model call in **call** order, not completion order. `runtime.readonly-parallel` is a wall-clock assertion and needs `synctest.Test` with `synctest.Wait()` (pattern: `core/model_stream_test.go:98`); the cap assertion is an invariant - peak concurrency counted with an atomic, never measured with time.

- [x] 22.5 `core`: Implement the per-turn `Drive` loop, ordered tool round trips, `MaxTurns`, and cancellation without further component calls; execute the retry and repair turns designed by row 20.
  - files: `core/drive_turn.go`, `core/drive_turn_test.go`
  - scenarios: `runtime.tool-round-trip`, `runtime.parallel-calls-ordering`, `runtime.max-turns`, `runtime.cancellation`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRuntimeTurns'`
  - note: `runtime.max-turns` counts **model calls** (`runtime/spec.md:306`), and a steer-drain turn counts too (`:121`). Limits are enforced in the chains (`limits/spec.md:48`), so the loop must not double-count `MaxToolCalls`. Cancellation needs the iterator to yield `context.Canceled` and stop, an in-flight side effect to finish under the shield, and a `Runs.SignalCancel` to stop at the next safe point (`:310`). This chunk executes the retry and repair turns that 20.2 and 20.3 only decide.

- [x] 22.6 `core`: Enforce `Drive` lifecycle ordering: append before the gate, checkpoint before suspension, and `Runs.Finish` before terminal `Done`.
  - files: `core/drive_lifecycle.go`, `core/drive_lifecycle_test.go`
  - scenarios: `runtime.suspend-order`, `runtime.append-before-tool`, `runtime.done-after-finish`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestRuntimeLifecycle'`
  - note: `runtime/spec.md:127-130` fixes the suspend order (`Checkpoints.Put`, then `Runs.Suspend`, then the `Suspended` event, then the iterator ends); terminal `Done` is emitted last, after `Runs.Finish` (`:134`) and after verification of every `Uncertain` entry (`:131`, through the injected verifier). The append shape differs by turn: no calls or only `ReadOnly` calls append assistant and results once at step end; `SideEffect` and `Idempotent` append the assistant with its pending calls before the batch and advance `HistoryVersion`. `Runs.Finish` returns `ErrSignalsPending` while a steer is pending, which forces one more turn unless `MaxTurns` is reached.

- [x] 22.7 `core`: Declare the `Stores` value type and the `Stack` store façades, and expose the session log's fork through an optional interface.
  - files: `core/stores/stores.go`, `core/stack_stores.go`, `core/stack_stores_test.go`
  - scenarios: none of its own - it is asserted through its consumers, `flow.regenerate-is-fork-and-continue` (23.3) and `stores.control-in-session-index` (23.5)
  - verify: `go test -short -timeout 2m ./core/... -run 'TestStackStores'`
  - note: `stores/spec.md:54` models `Stores` as a value type and no Go declaration exists anywhere. It belongs in `core/stores` beside the ports it carries, **not** in the floor: a floor type cannot name a leaf's interfaces, and moving the frozen ports upward to satisfy it would invert the dependency direction (ADR-0139). `stores.md:313` makes `Stack.Sessions` the owner-scoped entry point. `MemorySessionLog.Fork` (`core/stores/session_memory.go:299`) is not on the `SessionLog` interface (`core/stores/session.go:28`), so expose it as an optional interface, which is the only way a port may grow (`docs/design/compatibility.md`) and what 23.3 needs.


23. [x] `core`: `Flow[In, Out]`, `FlowFunc`, `Conversation` (`Send`, `Cancel`, `Continue`, `Steer` with runtime drain at safe points; takeover through `SessionControl`, `TakeOver`/`HandBack`/`OperatorSend`, `StopHandedOff`), `RunLimits` enforcement, and `std/flow`'s `Extract` and `Classify`. - `flow.plain-invoke`, `flow.not-suspendable`, `flow.typed-result-on-conversation`, `flow.cancel-other-request`, `flow.send-during-active-run`, `flow.idempotent-send`, `flow.send-during-human-control-no-run`, `flow.continue-without-input`, `flow.regenerate-is-fork-and-continue`, `flow.steer-applied-at-boundary`, `flow.steer-preserves-adjacency`, `flow.steer-after-final-reply-runs-turn`, `flow.steer-no-active-run`, `identity.steer-root-only`, `permission.takeover-requires-scope`, `stores.control-in-session-index`, `flow.takeover-pauses-agent`, `flow.operator-send-origin`, `limits.hard-cost-abort`, `limits.wall-clock`, `limits.cost-accumulates`, `flow.extract-recipe`, `flow.classify-recipe` (23 IDs, chunk-disjoint)

- note: this row's chunks all sit in the driver package `gohan` and call row 22's `Drive` and row 21's `Stack`, except 23.7 (a `core/chains` leaf) and 23.8 (`std/flow`). The driver declares no shared type (ADR-0139 rule 3): every vocabulary type gets an alias in `core/aliases.go`.
- note: `docs/design/types.md` does not list the driver's generic types - `Flow`, `FlowFunc`, `Conversation`, `SessionControl` are declared in `flow/spec.md` and the driver's own files.

- [x] 23.1 `core`: Implement `Flow[In, Out]`, `FlowFunc`, and typed `Done.Result` with matching canonical JSON in the final assistant message.
  - files: `core/flow.go`, `core/flow_test.go`
  - scenarios: `flow.plain-invoke`, `flow.not-suspendable`, `flow.typed-result-on-conversation`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestFlowContract'`
  - note: `flow/spec.md:110` requires the value as one `Text` block of canonical JSON in the final assistant message, with `Done.Result` carrying the same bytes as `json.RawMessage`. `ErrNotSuspendable` is `flow/spec.md:80`; `AbortError` and `Done` already exist in the floor and are referenced, never redeclared. The `gohan.flow` span (`flow/spec.md`, `flow.plain-invoke`) belongs to telemetry (row 28): assert the returned events and record the span emission.

- [x] 23.2 `core`: Implement `Conversation.Send` and `Cancel`, live-run refusal, idempotent reattachment, and `StopHandedOff` without starting a run under human control.
  - files: `core/conversation.go`, `core/conversation_test.go`
  - scenarios: `flow.cancel-other-request`, `flow.send-during-active-run`, `flow.idempotent-send`, `flow.send-during-human-control-no-run`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestConversationSend'`
  - note: the constructor name is frozen here (the flow spec says `agent.New`, and no `agent` package exists in the package table): the driver exposes it. `Send` reads the control state **before** `Runs.Start`, or the hand-off branch records a run it must not. `Cancel` returns only once `Runs` shows the run finished or the lease TTL expires (`flow.md:103`), which needs a fake clock under `synctest`. The refusal errors (`ErrEmptyHistory`, `ErrSessionHandedOff`, `ErrRunNotActive`, `ErrRunActive`) are already in the floor. `Done(guard_blocked)` - deferred here by row 16 - is emitted by this chunk.

- [x] 23.3 `core`: Implement `Conversation.Continue` without input and regeneration through the session fork followed by `Continue`.
  - files: `core/conversation_continue.go`, `core/conversation_continue_test.go`
  - scenarios: `flow.continue-without-input`, `flow.regenerate-is-fork-and-continue`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestConversationContinue'`
  - note: depends on 22.7's optional fork interface - `MemorySessionLog.Fork` already refuses a fork while a run is active, which the scenario asserts. The regenerate scenario also asserts that the fork's first model call reports cached input tokens for the shared prefix, which is row 19's assembly projection: assert what the request carries and record any half the projection owns.

- [x] 23.4 `core`: Implement `Conversation.Steer` and the runtime drain at safe points; preserve tool adjacency and run another turn when the finish finds pending steers.
  - files: `core/conversation_steer.go`, `core/drive_mailbox.go`, `core/conversation_steer_test.go`
  - scenarios: `flow.steer-applied-at-boundary`, `flow.steer-preserves-adjacency`, `flow.steer-after-final-reply-runs-turn`, `flow.steer-no-active-run`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestConversationSteer'`
  - note: `core/drive_mailbox.go` is the driver package's safe-point drain and calls row 22's loop seam; the mailbox depth (ten signals, then `ErrMailboxFull` on the eleventh, `stores/spec.md:452`) is already in `core/stores`. `SteerApplied` exists in the floor. The drain-turn interaction with `MaxTurns` belongs to 23.7's enforcement, not here.

- [x] 23.5 `core`: Enforce root-only `Steer` ownership and the takeover scope check; expose `SessionControl` filtering through the session index.
  - files: `core/session_control.go`, `core/session_control_test.go`, `core/types/identity.go`
  - scenarios: `identity.steer-root-only`, `permission.takeover-requires-scope`, `stores.control-in-session-index`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionControl'`
  - note: the `session:control` scope has no literal anywhere today - only `scopeSessionWrite = "session:write"` (`core/stores/session_memory.go:24`) and its grant in `std/permission/policy.go:40`. Declare the control scope once in the floor and use it in both places. `SessionQuery.Control` filtering and the `Control` field are already implemented in the memory index; the scenario asserts them through `Stack.Sessions` (22.7).

- [x] 23.6 `core`: Implement `TakeOver`/`HandBack`/`OperatorSend`, safe-point takeover, pending-token expiry, audited control transitions, and `OriginOperator` messages without model calls.
  - files: `core/session_takeover.go`, `core/session_takeover_test.go`
  - scenarios: `flow.takeover-pauses-agent`, `flow.operator-send-origin`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestSessionTakeover'`
  - note: `flow.md:176` requires takeover to cancel a streaming run at its next safe point and to expire a pending human-approval token - both are timing assertions and need `synctest`. `ControlHandoffRequested`/`ControlHuman` are in `core/stores`. The hand-back restore half is M1-deferred (`flow.handback-fences-operator-turns`), so assert the transitions this chunk makes observable.

- [x] 23.7 `core/chains`: Enforce `RunLimits` - turns, tool calls, cost, wall clock and the soft ratio - in the chain, so the limits apply under any backend; charge a fallback once per chunk.
  - files: `core/chains/limits.go`, `core/chains/limits_test.go`
  - scenarios: `limits.hard-cost-abort`, `limits.wall-clock`, `limits.cost-accumulates`
  - verify: `go test -short -timeout 2m ./core/... -run 'TestChainLimits'`
  - note: `limits/spec.md:48` says the chains enforce the limits, which is why the runtime must not also count `MaxToolCalls`. This chunk carries the charging half of `chains.fallback-charged`, deferred by row 18: the routing decision and the fallback-once ordering already landed, the spend does not. `limits.cost-accumulates` needs 0.04 + 0.04 + 0.04 against a `MaxCost` of 0.10 to abort on the third spend with `Done.Cost` on the root equal to the sum; the root/tree machinery is row 25, so assert the accumulation this chunk owns and record the root projection. `limits.wall-clock` is monotonic-time and needs `synctest`. `std/presets.go` currently installs a pass-through telemetry step, so pricing arrives here.

- [x] 23.8 `std/flow`: Implement `Extract` and `Classify` as governed single-call recipes with typed validation and no tools.
  - files: `std/flow/extract.go`, `std/flow/extract_test.go`, `std/flow/classify.go`, `std/flow/classify_test.go`
  - scenarios: `flow.extract-recipe`, `flow.classify-recipe`
  - verify: `go test -short -timeout 2m ./std/flow/ -run 'TestFlowRecipes'`
  - note: both recipes are declared in the flow spec's implementations table (`flow/spec.md:86-87`, tagged M0) but appear nowhere else - not in `docs/design/types.md`, not in the package table, which this rung adds. They build on `Flow` and `FlowFunc` (23.1) and on row 20's strict schema. A new package: `std/flow` must state its dependency direction like every other leaf.


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

- [ ] 26.6 `core`: implement typed `SharedState`/`SetSharedState`, session metadata persistence and resume restoration; emit the row-3 `StateChanged`/`PatchOp` shapes without core diffing.
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

- Row 1: the CI set carries a race-detected test leg (`task test:race`, `-race -short`) and a pinned `govulncheck` job, because the lease-and-reclaim rows are the first ones the race detector is worth running on; the adapter matrix, `conformance` and `examples` stay deferred to their suites.
- Rows 11 and 12: the audit log's chain, the event log's ring buffer, the retention sweep, legal hold, the feedback store, the content-addressed output store, the notes store and the schema registry all land in `core/stores`, with `std/outputs` and `std/guard` carrying the read-output tool and the blob guard. The `Stack`/`Stores` façades, `Ephemeral()` and the `Build`-side caps wiring stay row 21's; the retention sweep, the cascade and the fork copies reach other stores through injected dependents. The storetest suites mirror each port with their own types so `testkit/storetest` never imports `core/stores`, and each is bound to the memory implementation from a `core/` test — the shape `adapter/postgres` reuses in M3. Two conformance notes worth keeping: the runs suite drops assertions that encode one implementation (notice reason mapping, `ErrSignalsPending` on finish, the store-local not-found sentinel), and the schemas suite asserts upcaster determinism because purity is not provable in process. `Feedback` is declared in `core/stores` because the flow row has not landed it, and the store-local not-found sentinels stay the contract gap recorded under row 7.
- Row 1: branch is `master`; `git init`, the `origin` remote, `.gitignore`, `LICENSE` and `README.md` already exist; `go.work` is committed as development wiring and is never the version authority, while `go.work.sum` stays ignored because it is reproducible per checkout. Row 1 also lands the first two package declarations (`core/doc.go`, `core/types/doc.go`), so `task lint`, `go vet` and `task test` have a compilable unit: an empty module fails them (measured: `go test` exit 1, `golangci-lint run` exit 5). CI carries `spec`, `lint`, `test` and `bench` on `master` only — `conformance` and `examples` arrive with their suites, `api:check` with `adapter/httpapi` in M4, and the adapter matrix is empty for all of M0, so it is path-filtered. `task test:full` and `task examples:test` are named by `AGENTS.md` and land with rows 29 and 31.
- Row 3: `gohan.limit_exceeded`'s HTTP cell reads "429 for quota pools, else 422"; `ProblemOf` has no profile context, so it returns 422 and the quota-pool 429 is the transport's to set when the run's pool is known. `golangci-lint` now checks gofmt and goimports, because rows 1 and 2 left two files unformatted and the `default: standard` set does not look at formatting.
- Rows 9 and 10: their pre-split file labels resolve into `core/stores` (`journal.go`, `journal_memory.go`, `runs.go`), and each row's chunk that names a mechanism a later row owns asserts the store-level part only: `stores.replay-returns-recorded-result` cannot establish `ToolFinished.Replayed`, because the journal decorator is row 15.1, and `stores.journal-ttl` cannot assert that audit records survive the purge, because `AuditLog` is row 11.1; `recovery.no-double-run` names `Invoke`, which is row 23.1, so 10.1 asserts `Runs.Start` returning `ErrRunActive`. The journal TTL is prose only ("default 24 h"), so row 9 names it `DefaultJournalTTL` in `core/stores`. `RunNotice`/`NoticeKind`/`Notifier` stay in `core/types` where row 3.1 landed them, even though the design's package plan lists them under `core/streams` — row 10 uses that copy rather than redeclaring.
- Row 8: the `Checkpoints` port, the `Checkpoint` shape, the memory implementation and its two test files land in `core/stores`, and `ResumeInput`, `ApprovalVerdict` and `WorkspaceRef` land there too, because `Checkpoints.Consume` takes the first of them. Row 24 declares the suspension-side constructors and uses these types instead of redeclaring them; the drift audit carries the package question, since the suspension spec names them for a package that does not exist yet.
- Row 7: the memory store needs four errors the contract does not name — a missing session, a missing message, a linked session and a missing hold scope — and exports them (`ErrSessionNotFound`, `ErrMessageNotFound`, `ErrSessionLinked`, `ErrHoldScopeMissing`) because `adapter/postgres` must return the same ones in M3. The stores spec declares no such sentinel and the catalogue has no row for them, so they are a contract gap: whichever row first serves a session id to a client (`adapter/httpapi`, M4) declares `ErrSessionNotFound` with its catalogue code, and rows 8-12 declare theirs when they need them. Recorded here so the drift audit does not re-find them as unexplained.
- Row 7: the port, `History`/`ForkPoint`, `SessionMeta` and the index types land in `core/stores/session.go` (package `stores`), the memory implementation with the versioned append, `Message.ID` assignment, the index queries, fork and the cascade in `core/stores/session_memory.go`, and the four test files beside them. The `Stack.Sessions`/`UpdateSession` and `Stores.ForkSession`/`DeleteSession` façades are row 21's, because `Stack` and `Stores` are built there; the cascade and the fork's copies reach the other stores through injected dependents, so rows 8-12 wire them when they land. The spec's `memory.New(memory.WithNow…)` prose predates the package split: the memory implementation is `core/stores`, and its clock injection is an option on that package's constructor. `SchemaVersion` stays row 12.6's, where the stored-shape registry lands.
- Row 6: it lands the tool vocabulary in `core/types/tool.go`, the egress mechanism in `core/types/tool_egress.go`, the schema walker in `core/types/tool_schema.go` (with a consumer-owned `IdentityFieldExcluder`, because `types` never imports the driver), the decider in `core/types/decider.go`, and the driver's construction and call paths in `core/tool.go`, `core/tool_names.go`, `core/tool_new.go` and `core/tool_call.go`. `Build` wiring — registration, collisions, reserved names, egress enforcement, exfil derivation — is row 21's, and each mechanism is exposed by name for it. `gohan.Retryable` is declared in the tool contract, `ToolArgsError` is a named catalogue waiver, and the index's collision check now routes driver-declared names to package `gohan` (ADR-0142, second pass).
- Rows 6 and 18: the `Model` port moves to 18.6, because `Profile() ModelProfile` and `Generate(ctx, ModelRequest)` need both row-18 types, and `EgressPolicy` moves into 6.2 with the rest of the tool vocabulary, because `ToolSpec` carries one; 6.4 keeps the missing-policy refusal and the `Capabilities.Exfil` derivation.
- Row 4: the identity vocabulary lands in `core/types/identity.go` and `core/types/credential.go` (`Principal`, `RunInfo`, `CostTags`, `SessionOwner`, `LatencyClass`, `RunMode`, `Credential`, the `CredentialSource` port), the unexported keys and the `(T, bool)` accessors in `core/identity.go`, the seam in `core/identity_seam.go` and the exclusion mechanism in `core/identity_args.go`; rows 6, 14 and 18 use them instead of declaring their own. `ApprovalFrom` waits for `Approval` (permission, row 14), and the default identity field list (`user_id`, `tenant`, `customer_id`) waits for `std` in row 17: core carries the mechanism, std the policy.
- Row 3: it lands the whole shared vocabulary rather than the event payloads alone — the 32 sentinels, the 12 typed errors, the model error classes, the event set, and the seven types the payloads reference — because the payloads, the catalog and every later row's errors are one contract; rows 9, 12, 16, 24 and 26 use those declarations instead of making their own. `CallKey` moves here from 9.1 (`Done.Uncertain` needs it) and `streams.monotonic-seq` moves to 11.3: a type package cannot establish "`Seq` values are exactly 1..N in delivery order", the `EventLog` that assigns `Seq` can. `errors.retry-after-on-retryable` keeps its M0 half here (the row is `Retryable`, and `gohan.mailbox_full` carries a 1 s `RetryAfter`); the HTTP leg — a 409 `application/problem+json` with `Retry-After` set to the lease's remaining seconds — arrives with `adapter/httpapi` in M4, and the remaining lease is the transport's to supply.
- Row 2: the block model and its `BlockKind` tags are one unit; `ModelChunk`/`Usage`/`DeltaKind`/`FinishReason` are the second, and the request shape moved to 18.7 — `ModelRequest` needs `ToolSpec`, which row 12 lands, so declaring it in row 2 would pull the whole tool vocabulary forward. `CompactionKind` is declared with its block in `core/types` (the `context` spec shows its members and names `messages` as the owner).
- Rows 2–F: every `files:` line carries the resolved path — a `core/` label resolves through the package plan in `design.md` before dispatch, so `core/message.go` is `core/types/message.go` and the driver file is `core/aliases.go` — and each vocabulary row adds the driver aliases for its own declarations (ADR-0139 rule 3).
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

- Row 13: paths resolved through the design package table (`core/chains`, package `chains`); the model half of the chain (`ModelFunc`, `ModelMiddleware`, `ModelChain`) moved to 18.7 because it needs `ModelRequest`; `std/presets.go` added to 13.2 because no chunk declared the presets its scenario needs. Names invented here and frozen, since the spec is prose: `ValidateToolChain` (ordering), `RunToolChain` (composition and panic naming), `Preset` (the presets' return type). Deferred at assertion time: usage charging for a replaced cache result and the assembled-request equality of an empty chain (22), `Explain`/`Stack` and the manifest prompt hash (21), span step recording (26/28).

- Row 14: paths resolved to `core/permission` (package `permission`, per the spec's `permission.Gate`); the gate returns a tool middleware, so `ToolFunc`/`ToolMiddleware` moved to the type floor (`core/types/middleware.go`) with aliases left in `core/chains` — depguard refuses a leaf importing a peer leaf, and moving the declaration is the correct fix, not widening the rule. `core/limits.go` dropped (`MaxPending` is an `ApprovalPolicy` field). The taint vocabulary (`ArgTaint`, `TaintAction`, `TaintDenied`) is declared in `core/types/taint.go` for the empty `TaintHook` slot while the behaviour stays M1. Grants go through a `GrantStore` interface declared in `std/permission` with a memory implementation; `Waker` (24.2) arrives as a narrow injected interface; `ToolSpec.FingerprintFields` and `WithFingerprintFields` (tools spec:112, prose only) land here. Deferred at assertion time: the `Suspended` emission and `Failed(Permanent)` results (22), `gohan.approval.expired` (28), the `Resume` entry point (24.1).
- Row 15: `std/` root is package `std` and gains journal, shield, verify and uncertainty; the canonical fingerprint helper is frozen in `std/journal.go` (the spec gives a formula, not a function); metrics, the `Drive` loop's append count, `Flow.Invoke`/`Done` emission and `Recover`/`Resume` wiring are deferred to rows 22/23/24/27/28.
- Row 16: `core/guards` declares `GuardInput`, `GuardAction`, `GuardVerdict`, `Guard`, `OutputMode` and `Fallback` — the design table's placement, except `GuardStage` and `GuardBlockedError`, which already live in `core/types` from row 3 and are referenced rather than moved. Frozen in `std/guard`: the rules grammar (`Rule{Name, Contains, Action}`, case-insensitive substring, first match decides), `Fence`/`OriginGuard`/`ContextGuard`, `Buffered`/`Windowed` with `DefaultWindow = 64`. Deferred: the failing `Build` (21.1), spans (28.1), `Flow.Invoke` (23.1), `Done(guard_blocked)` (23.2), the assembler's fencing call site (19.1), the chain-side origin overwrite (22).
- Row 17: `ToolPolicy` stays in the driver package because its `DescribeGuard` is a `guards.Guard` and the type floor may not import `core/guards`; the effect cap is applied before gate evaluation. Frozen in `std`: the manifest hash (SHA-256 over name, description, schema, effect and scopes) and the pinned-file shape `{version:1, tools:[{name,hash}]}` sorted by name, since the spec declares neither; `NarrowTools` rejects widening with `ErrToolFilterWidened` and re-emits in base order. The active tool set persists in `Checkpoint.Data` — no store shape change. `depends_on` 19.1 dropped from 17.2: the assembler is the filter's consumer, not its dependency. Deferred: `Build`'s manifest option and share warning (21), `Replay`'s drift error (22), the effect-capped metric (28).

- Chunk 3.4 and row 9: 3.4 was skipped when the rest of row 3 landed, and row 9's chunks landed without their ticks; both are closed now with their verifies green. 3.4's file resolves to `core/types/stream_error.go`, next to the payload vocabulary row 3 landed in `core/types/event.go`, rather than to a new `core/streams` package — the landed payload split wins over the design table's prose.

- Rows 18 and 19: the plan's duplicate chunk id 18.7 is renumbered 18.8, and the old 18.6 is split into 18.6 (profile, caps, port and budget vocabulary) and 18.9 (stream release, cancellation and timeouts) so each dispatch stayed reviewable. Ports and value types moved to the floor because the driver declares no shared type and `core` may not import `std`: `ProviderKeySource`/`ProviderCredential`/`ProviderKeyValidator` in `core/types/model_keys.go`, `ContextSlot`/`ContextProvider` in `core/types/assembly.go`, the estimator port and budget types in `core/types/tokens.go` with the arithmetic in `core/types/context_budget.go`. `BlobCaps` moved out of `core/stores/blob_checks.go` into the floor with an alias left behind, since `Caps.Blobs` needs it; `Fidelity` is declared here because `Caps.Fidelity` needs it and no chunk owned it. 18.7 landed the chain's model half that row 13 deferred: `ModelFunc`/`ModelMiddleware` join the tool pair in the floor and `core/chains/model_chain.go` carries `ModelChain` plus aliases. `std/keys`, `std/route` and `std/tokens` are added to the design package table. Frozen here, with no spec shape to follow: `ClassifyProviderError` and its code sets; the retry policy (two retries, 100ms base, 2s cap, no jitter, transient class only); the limiter's constructor and per-endpoint bulkheads; the router, the breaker (threshold 5, window 30s, half-open 10s) and the fallback middleware; `Truncate`'s shape; the heuristic's token rules with its frozen image constant; the budget formulas (`margin = MaxTokens/2` truncated, `Reserved = MaxTokens + margin`, `Limit = window - Reserved`). The terminal-partial state has no `Message` field, so the projection keys on `Meta["finish"]` and row 23 must set it. `gohantest.ScriptedModel` does not exist yet, so every model test in these rows uses a local fake and the shared testkit stays deferred. Deferred at assertion time: the `Build` seam wiring (21.1), the fallback charge (`RunLimits`, 23.7), the failover and effect-capped metrics (28), the flow's `Done` emission (23) and the adapter-produced `Usage.KeyID`.

- Commit mapping for rows 18 and 19: both landed in one commit, `4c8727b`, whose message describes row 18; the row-19 ticks and every correction above are in the same commit. The split was lost to a `git add -A` before the path-scoped commits, and a pushed `master` is never force-pushed here - the record lives in this list instead.

- Rows 20 and 21: `PartialView` follows the spec (non-generic, `(acc string) PartialValue`), not the plan's generic form; `ErrStructuredOutput` already lived in the floor and is referenced, never redeclared; `ToolUse` carries no truncation flag, so truncation is derived from the finish reason; the partial view's tolerant parse is hand-written because `jsontext` has no incomplete mode; the strict schema reuses the floor's `SchemaBuilder` rather than a second walker. A strict audit of the corpus confirmed every M0-owned scenario is named by some chunk's `scenarios:` line, and recovered three the plan had left unassigned: `model.undeclared-fidelity` and `build.opaque-compaction-fallback` now belong to 21.2, `release.id-deterministic` to 21.3. `Build`'s options take floor types only, because depguard forbids `core` importing `std` while `Build` is the composition root: the key source, the model middleware chain (router, retry, limiter), the estimator and the logger arrive as floor types or interfaces and the caller passes the policy value. The pinned manifest's value type moved into the floor with an alias in `std/manifest.go`. `WithLogger(*slog.Logger)` is declared although the spec's option list omits it. Options whose value types no spec declares (`Budget`, `Redactor`, `Sandbox`, `Flags`, `Detach`, `Skill`/`SkillSource`, `PromptRef`, `flowdef.Definition`) are deferred with their spec line, names reserved; `MaxParallelTools` carries its own integer; `AllowDrop` stays a `std/flow` option; the `Stack` store façades move to row 22. New chunk 21.4 carries the preset-to-option adapter no chunk owned (`func (p Preset) Options() []gohan.Option`, legal because `std` may import the driver). Frozen with no spec shape: `ToolSchema` and its strict marker, `ReasonFirst` and its `Explain` string, the truncation allowance (4096 output tokens, doubled once), `ValidateRepair` and its bound. Deferred: the repair and truncation turns to the Drive loop (22.4), the driver's `Explain` entry and the flow resolutions in the matrix record to the runtime row, the dropped-block and refusal metrics (28), and the manifest's skill, chain and definition sections, which have no M0 source.

- Commit mapping for row 21: the code lands in its own commit, but the plan file carrying rows 20 and 21's ticks and the corrections entry rode with the row-20 commit `fad0fd1` (staged and committed before the row-21 message was fixed for length). The entry itself covers both rows, so the record is complete - it simply sits one commit earlier than the code it describes.

- Rows 22 and 23, from the rung's scouts. **Row 22 is seven chunks now, not five**: `RunLimits` moved up from 23.7 to 22.1, because 22.3/22.4/22.5 all spend it (`MaxToolCalls`, `MaxParallelTools`, `MaxTurns`) and it had no Go shape in any spec or file; the option that carries it and the build-time refusal of an unbounded run land with it, and its enforcement stays at 23.7 (now `core/chains/limits.go`, which is where `limits/spec.md:48` says limits are enforced). The runtime chunks renumbered 22.2-22.6. **Paths resolved through the package table**: the runtime is a leaf - `core/runtime/runtime.go`, `runtime_batch.go`, `runtime_schedule.go` - not the driver, while `core/drive.go`, `drive_turn.go` and `drive_lifecycle.go` are the driver package, which declares no shared type, so their vocabulary needs aliases. **New chunk 22.7** carries two things rows 7 and 21 both claimed and neither landed: the `Stores` value type (`stores/spec.md:54`) with the `Stack.Stores`/`Stack.Sessions` façades, and `SessionLog.Fork` as an optional interface - the memory store has `Fork`, the interface does not, and `flow.regenerate-is-fork-and-continue` needs it. **Four declarations the row could not have written without inventing them**: `AssembleInput` moves into the floor with an alias (it is in `std/assembly.go` and `AgentRun.Assemble` names it); the identity ctx seams (`RunInfoFrom`, `WithPrincipal`/`PrincipalFrom`, `WithIdempotencyKey`) move down for the same reason - a leaf may not import the driver; the component-event sink (`runtime/spec.md:86`) had no shape anywhere and is now declared in the floor with a normative shape in the spec (ADR-0143); and `permission.Gate` exposes only a middleware, which cannot deny a batch before its first call runs, so 22.3 exports the decision seam on that package. **`runtime.message-round-trip` is no longer M0's**: it asserts conversion to eino's `AgenticMessage` and adk-go's `genai.Content` and M0 ships no adapter, so `scenarios.json` defers it to M2 (its sibling `runtime.governed-components-in-eino-graph` was already M2). Row 22 therefore holds 17 scenarios, not 18, and covers all 17. **Scheduling**: `MaxParallelTools`/`SequentialTools()` reach the runtime through `StrategyPlan` from `Stack.ResolveStrategies`, never read from the config; `runtime.max-turns` counts model calls including a steer-drain turn; limits live in the chains, so the runtime must not count tool calls again. **synctest**: mandatory for `runtime.readonly-parallel` (a wall-clock claim) and for the cancel/hand-off/takeover/wall-clock scenarios; the parallel cap is an invariant to count with an atomic, never to measure with time. **`std/flow` added to the design's package table** - chunk 23.8 writes it and the table never listed it. **The shared testkit stays in row 29**, and the design's test strategy now records the interim: rows 22-27 use a local scripted model, three such fakes already landed, and row 29 replaces them. A local fake must yield on a channel, never on a timer, or it breaks the synctest bubble.

## Deferred

Ownership is per scenario in `openspec/scenarios.json` (`deferred_to`), set where a milestone deliberately leaves a scenario red (ADR-0135). Tally at M0: 280 of 512 scenarios are in the chunks above; the other 232 name a later milestone (M0.5 9, M1 86, M2 36, M3 56, M4 45). No scenario of an M0 capability is left unassigned.
