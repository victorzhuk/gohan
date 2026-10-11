# Tasks

## 1. Runtime composition and constructors

- [x] 1.1 Confirm `restore-runtime-governance` has landed its `core/foreign_conversation.go` edit before task 1.2 runs. The governed tool wrapper and the caller-derived run context MUST both survive afterwards. Verification: the source check in task 1.5 finds the governed wrapper and the caller-derived context in the file, and the driver supplies the integration evidence for the landing order when it delivers this change without commits.
- [x] 1.2 Add one binding helper and call it from `NewNativeConversation` (`core/native_conversation.go:19`) and `NewForeignConversation` (`core/foreign_conversation.go:133`) so both bind `stack`, `tenant`, `titleProfile`, `flags` and `frozen` before the option loop. Verification: a unit test builds both constructors over one stack and asserts shutdown admission, tenant, title profile and frozen snapshot match.
- [x] 1.3 Add a public test that builds every constructor after `Shutdown` and asserts `Send`, `Continue` and `Resume` fail with `types.ErrShuttingDown`. Covers `streams.handle-constructor-bindings`, `streams.handle-native-foreign-share-stack`.
- [x] 1.4 Update the conversation handle documentation for the new binding contract. Verification: `go doc` shows the changed constructor comment.
- [x] 1.5 Internal-only. Source check on `core/foreign_conversation.go` after task 1.2: the file still routes `governedTool.Call` through the governed foreign path and still derives the run context from the caller parameter. Observable: the file names both, and the lifecycle binding helper does not replace either. No public behavior change; this task is verified by the source check plus the executed tests in sections 1-5.

## 2. Shutdown admission

- [x] 2.1 Make the admission decision and the in-flight registration one atomic operation in `core/shutdown.go` (`beginRun`). Verification: a unit test interleaves `Send` with `Shutdown` and asserts no run starts unregistered. Covers `runtime.shutdown-admission-atomic`.
- [x] 2.2 Register before `Runs.Start` on the `Send`, `Continue` and `Resume` paths. Verification: a test asserts registration ordering for each path. Covers `runtime.shutdown-admission-every-constructor`, `runtime.shutdown-admission-resume`.
- [x] 2.3 Document the atomic admission rule in the shutdown code comment group.

## 3. Lifecycle terminal reason and handoff

- [x] 3.1 Pass the `terminal` argument of `finishTurn` (`core/drive_lifecycle.go:958`) to `finishRun` and `done` instead of `types.StopCompleted`. Verification: a table test covers every terminal reason and asserts `Done.Reason` and the durable reason. Covers `runtime.done-carries-limit-reason`, `runtime.finish-carries-terminal-reason`, `runtime.cancelled-run-keeps-reason`, `runtime.handed-off-run-keeps-reason`.
- [x] 3.2 Report the recorded `StopReason` from `noticeFor` (`core/stores/runs_notices.go:56`). Verification: a store test asserts the terminal notice reason for `StopLimit`, `StopHandedOff` and `StopCancelled`. Covers `stores.finish-notice-recorded-reason`, `stores.finish-notice-cancelled`.
- [x] 3.3 Collapse the two `*types.HandoffError` branches in `Lifecycle.drive` (`core/drive_lifecycle.go:485`, `:530`) into one helper that persists the `HandoffContext`, sets `ControlHandoffRequested`, audits `handoff_requested`, fires the optional callback once and closes the run with `StopHandedOff`. Verification: a public test asserts persistence, control state, one audit record, one callback and `Done{StopHandedOff}` on a full store and on an optional-store configuration. Covers `flow.handoff-control-transition-once`, `flow.handoff-optional-store`, `flow.handoff-state-preserved`, `flow.handoff-no-resume-token`.
- [x] 3.4 Extend the lifecycle and handoff docs for the single transition and the preserved reason.

## 4. Detached discovery and `SessionRunFinder`

- [x] 4.1 Implement `RunForSession` on `MemoryRuns` (`core/stores`), returning the session's run that is `Running` or `Resuming` with a live lease, or `stores.ErrRunNotFound`. A `Suspended`, terminal or lease-expired run MUST NOT be returned. Verification: a store test asserts cross-instance discovery, and refusal for absent, suspended, terminal and expired runs. Covers `stores.run-for-session-cross-instance`, `stores.run-for-session-absent`, `stores.run-for-session-terminal`.
- [x] 4.2 Add `ActiveRun(ctx context.Context, sessionID string) (string, error)` to `Conversation` (`core/conversation.go`), owner-authorized, using the in-process live map then `SessionRunFinder`, returning `types.ErrRunNotActive` and no ID unless the run is `Running` or `Resuming` with a live lease. `Send` keeps its signature and detached behavior. Verification: public tests cover found, absent, suspended, terminal, expired, unauthorized and cross-instance. Covers `streams.active-run-found`, `streams.active-run-absent`, `streams.active-run-terminal-or-expired`, `streams.active-run-owner-authorized`, `streams.active-run-cross-instance`.
- [x] 4.3 Verify a detached run is discoverable while it is `Running` or `Resuming` with a live lease. Covers `streams.detached-run-discoverable`; `identity.detached-run-id-captured-before-gate`.
- [x] 4.4 Make `Cancel` and `Steer` reach a cross-pod run through `SessionRunFinder`. Verification: a cross-instance control test asserts `Cancel` reaches the run. It runs after `repair-postgres-store-contracts` ships `adapter/postgres.Runs.RunForSession`, and does not define that query.

## 5. Identity fixture repair

- [x] 5.1 Repair `core/identity_detach_scenarios_test.go`: capture the run ID through `ActiveRun` before closing `rt.gate` in `runDetachedReissue`, while the run is `Running` or `Resuming` with a live lease; do not widen the timeout. Verification: the test passes under `-race` and asserts one credential reissue. Covers `identity.detached-reissue-before-first-effect`, `identity.detached-no-credential-reuse`.
- [x] 5.2 Assert one credential reissue and a run ID that stays stable for the whole run lifetime.

## 6. Verification

- [x] 6.1 Run the touched core and `core/stores` package tests with the correct module boundary and a bounded timeout. Verification: the packages pass.
- [x] 6.2 Run the public-path acceptance for `Send`, `Resume`, `Recover`, `Cancel` and `ActiveRun`. Verification: each named scenario ID in sections 1-5 maps to at least one executed test; zero-matched tests do not count.
- [x] 6.3 Verify the change artifact against its task spec and the module boundary. Boundary note: the `openspec` CLI in this workspace is a stub and exits 64, so no `openspec validate` gate is run or claimed here. Verification: `timeout 300 task spec` covers the spec delta and `timeout 10m go test -timeout 10m ./core/... -run 'TestLifecycle|TestConversation'` passes.

## 7. Contract, registry and ADR at apply time

- [x] 7.1 Edit the `streams` handle Contract to add `ActiveRun(ctx context.Context, sessionID string) (string, error)` with the active-state rule. Verification: the block states that active means `Running` or `Resuming` with a live lease and names `types.ErrRunNotActive`.
- [x] 7.2 Register these 27 scenario ids in `openspec/scenarios.json`, each with `capability`, `origin` and `title` matching the delta scenario and each mapped to the executed test named in the task that owns it: `streams.handle-constructor-bindings`, `streams.handle-native-foreign-share-stack`, `streams.active-run-found`, `streams.active-run-absent`, `streams.active-run-terminal-or-expired`, `streams.active-run-owner-authorized`, `streams.active-run-cross-instance`, `streams.detached-run-discoverable`; `runtime.shutdown-admission-atomic`, `runtime.shutdown-admission-every-constructor`, `runtime.shutdown-admission-resume`, `runtime.done-carries-limit-reason`, `runtime.finish-carries-terminal-reason`, `runtime.cancelled-run-keeps-reason`, `runtime.handed-off-run-keeps-reason`; `stores.run-for-session-cross-instance`, `stores.run-for-session-absent`, `stores.run-for-session-terminal`, `stores.finish-notice-recorded-reason`, `stores.finish-notice-cancelled`; `flow.handoff-control-transition-once`, `flow.handoff-optional-store`, `flow.handoff-state-preserved`, `flow.handoff-no-resume-token`; `identity.detached-reissue-before-first-effect`, `identity.detached-run-id-captured-before-gate`, `identity.detached-no-credential-reuse`. The registration keeps all 27 authored ids and adds exactly two extras that the executed tests already cover: `smoke.send-through-each-constructor`, mapped to the constructor smoke loop at `core/lifecycle_admission_test.go:95-130`, and `stores.run-for-session-expired`, mapped to the expired-lease assertions at `core/stores/runs_session_test.go:12,87-90`. No further id is registered. Verification: `task spec:coverage` reports no registered id without a matching subtest, and each of the 27 plus 2 names an executed test by file and subtest.
- [x] 7.3 Synchronize the `streams` Contract into `openspec/specs/streams/spec.md` with the constructor bindings, the terminal-reason reporting and the active-run discovery rule.
- [x] 7.4 Synchronize the atomic shutdown admission and terminal stop-reason requirements into `openspec/specs/runtime/spec.md`.
- [x] 7.5 Synchronize the single handoff terminal transition into `openspec/specs/flow/spec.md`.
- [x] 7.6 Synchronize the shipped `SessionRunFinder` and the finish-reason reporting into `openspec/specs/stores/spec.md`.
- [x] 7.7 Synchronize the detached reissue timing and run-ID stability into `openspec/specs/identity/spec.md`.
- [x] 7.8 Regenerate the types index. This step is driver-owned and runs at apply time after 7.1 and 7.3-7.7. Verification: a re-run of the generator produces no diff.
- [x] 7.9 Update the testkit conformance runs fixture (`testkit/conformance/flow.go:184`) for the shipped `RunForSession` contract. Verification: the conformance suite compiles and passes.
- [x] 7.10 Record the design decision in an ADR chosen at apply time. Verification: the ADR is linked from the change; no number is reserved during authoring. The ADR and the generated index are driver-owned at apply time.
