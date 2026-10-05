# Tasks: m0-public-path-closure

Scope: the repairs and evidence bindings named in `proposal.md`. Decisions are sealed in `design.md`; ADR-0153 amends the delivery and worker decisions, ADR-0154 seals the continuation record and per-ask approvals. All rows are complete; the verification record at the end is what was actually observed.

## How to run this plan

- A **chunk** is the executable unit: one file cluster, its own tests, one literal verify command.
- A chunk is done when its `verify:` command is green and `task lint` and `task test` are green for the packages it touched.
- A regression test that pinned a defect is replaced by one that asserts the contract; the replacement is named below.
- Scenario IDs are claimed only by a test that asserts the scenario's whole contract (`design.md` D9).
- `task spec:gate` stays green throughout: this change registers no scenario and leaves no scenario of an M0 capability unassigned.

## Wave 1 — untrusted ingress, identity, ownership, stores

1. [x] `core`: refuse the reserved approval metadata at every message ingress. Decision D1.
  - files: `core/approval_receipt.go`, `core/conversation.go`, `core/conversation_steer.go`, `core/session_takeover.go`, `core/approval_receipt_seam_test.go`
  - tests: `TestSendRefusesReservedReceiptMeta`, `TestSteerRefusesReservedReceiptMeta`, `TestOperatorSendRefusesReservedReceiptMeta`, `TestForgedReceiptDoesNotGrantSideEffect`, `TestRejectReservedMeta` kept green

2. [x] `core`: mint the acquired run's identity on `Send` and `Continue`. Decision D2.
  - files: `core/conversation.go`, `core/conversation_continue.go`, `core/native_identity_regression_test.go`
  - tests: `TestNativeSendInstallsRunIdentity`, `TestNativeContinueInstallsRunIdentity`, `TestNativeJournalIsolatesSessions`, `TestNativeAmbientRunInfoDoesNotOverrideAcquiredRun`

3. [x] `core`: own the resolved definition slices. Decision D7.
  - files: `core/native_resolve.go`, `core/native_snapshot_regression_test.go`
  - tests: `TestResolveNativeOwnsDefinitionSlices`, `TestNativeConversationDefinitionMutationDoesNotChangeExecution`

4. [x] `core/stores`: compact a wrapped event ring in ring order. Decision D6.
  - files: `core/stores/events.go`, `core/stores/events_test.go`
  - tests: `TestEventLogExpireWrappedRing`

5. [x] `core`: arbitrate a terminal provider error against a closed chunk buffer. Decision D6.
  - files: `core/model_stream.go`, `core/model_stream_test.go`
  - tests: `TestModelStreamClosedBufferPreservesTerminalError`

6. [x] `core`: observe live writes from another conversation instance in `Attach`. Decision D6, ADR-0153 decision 6.
  - files: `core/attach.go`, `core/attach_cross_instance_test.go`
  - tests: `TestAttachObservesAnotherConversationWriter`, existing `TestAttach` cases kept green

## Wave 2 — resolved execution preparation

7. [x] `core`: honour the tool step predicate and assemble from the resolution. Decisions D7, D8.
  - files: `core/native_effects.go`, `core/drive_turn.go`, `core/native_preparation_test.go`
  - tests: `TestNativeToolChainApplies`, `TestNativeConversationToolChainAppliesMatchesExplain`, `TestNativeAssemblyReceivesResolvedConfiguration`

## Wave 3 — authoritative turns, append shape and per-ask approvals

8. [x] `core`, `core/runtime`: carry the driver record and preserve it across phase advance. ADR-0154 decisions 2, 5, 6.
  - files: `core/checkpoint_envelope.go`, `core/runtime/native.go`, `core/drive_turn.go`, `core/approval_receipt.go`, `core/native_continuation_test.go`
  - tests: `TestNativePhasePreservesDriverRecord`, `TestNativeBatchPersistsContinuation`, `TestCheckpointEnvelopeVersionTwoRoundTrip`, `TestCheckpointEnvelopeV1NativeApprovalRefused`, `TestCheckpointEnvelopeV1MultiApprovalRefused`, `TestApprovalReceiptVersionIndependent`

9. [x] `core`: choose the turn's append shape and make the turn counter authoritative. Decisions D4, D5.
  - files: `core/drive_turn.go`, `core/drive_lifecycle.go` (the double-terminal fix), `core/native_turn_contract_test.go`
  - tests: `TestNativeReadOnlyTurnSingleAppend`, `TestNativeSideEffectTurnPersistsCallsBeforeExecution`, `TestNativeMaxTurnsStopsPublicRun`, `TestNativeTurnRestoredFromState`, `TestBatchOverrunTranslatesToLimitExceeded`

10. [x] `core`: suspend a native ask as a human approval with one persisted approval for the active ask. ADR-0154 decision 1.
  - files: `core/drive_lifecycle.go`, `core/drive_turn.go`, `core/native_approval_regression_test.go`
  - tests: `TestNativeAskSuspendsAsHumanApproval`, `TestNativeApprovedAskExecutesOnce`, `TestNativeReadOnlyAskKeepsSettledResults`
  - replaced: `TestNativeResumeSuspendsAgain` (delivery-as-approval) by the approval contract; `TestRuntimeBatchTurn`'s AwaitingBatch pin for a permission ask, with genuine external batch delivery coverage retained

11. [x] `core`: settle and continue a batch one ask at a time; apply a Resume decision to the active ask only. ADR-0154 decisions 3, 7.
  - files: `core/drive_turn.go`, `core/resume.go`, `core/native_approval_sequence_test.go`
  - tests: `TestNativeSequentialAsksOneRequestEach`, `TestNativeQueuedAsksNeedDistinctApprovals`, `TestNativeAskDecisionsDoNotRerunSettledAsks`, `TestNativeRejectSettlesOnlyTheActiveAsk`

12. [x] `core`: restore the run's admitted tool-call total on a drive re-entered from a persisted state, and copy the resolved `ConsumerStall` into the native conversation. ADR-0154 decision 4.
  - files: `core/drive_turn.go`, `core/native_conversation.go`, `core/native_limits_regression_test.go`
  - tests: `TestNativeMaxToolCallsPermitsPrepaidAsks`, `TestNativeConsumerStallConfigured`, `TestNativeRestoredToolTotalIsNotReservedTwice`

13. [x] `core`: reconstruct a running native state from durable history and refuse unresolved stored authority. Decisions D3, D4.
  - files: `core/recover.go`, `core/native_recovery_contract_test.go`
  - tests: `TestNativeRecoverRunningRun`, `TestNativeRecoverSettledBatchStartsModelPhase`, `TestNativeRecoverReadOnlyUncommittedTurn`, `TestNativeMaxTurnsSurvivesRecovery`, `TestRecoveryAuthorityMissingOwnerFailsClosed`, `TestNativeRecoveryPreservesCheckpointOriginator`
  - adapted: existing `recover_test.go` subtests that relied on ambient reaper authority now seed a stored owner; the no-owner case is covered by `TestRecoveryAuthorityMissingOwnerFailsClosed`

## Wave 4 — one worker for initial and resumed runs

14. [x] `core`: one bounded handoff, consumer-armed stall detection, and resumed runs on the same worker. Decision D6, ADR-0153.
  - files: `core/stream_lifetime.go`, `core/stream_stall.go`, `core/resume.go`, `core/stream_stall_test.go`, `core/native_worker_test.go`, `core/native_stall_test.go`
  - tests: `TestConversationStreamBufferBound`, `TestConversationDetachedDeliveryDoesNotRetainBacklog`, `TestStreamStallDoesNotPreemptWaitingConsumer`, `TestStreamStallDoesNotPreemptSilentTool`, `TestConversationEarlyBreakStopsDeliveryHelpers`, `TestNativeConsumerStallPreempts`, `TestNativeConsumerStallDetaches`, `TestResumeConsumerStallPreempts`, `TestResumeConsumerStallDetaches`, `TestResumeFailureRecordedForAttach`
  - replaced: the `stalled-consumer-gets-no-model-retry` fixture, which cancelled the run context and so tested a client disconnect rather than the stall contract; the replacement proves preemption on the production path with two model calls, one preemption, a persisted token and a released lease

15. [x] `core`: resolve `Resume` ownership before the token state decides the error. Decision D2.
  - files: `core/resume.go`, `core/resume_validation_test.go`
  - tests: `TestResumeAuthorizesBeforeTokenState` (ownership check preceded by the checkpoint's own session binding where the checkpoint is identifiable; invalid tokens keep their existing refusal)
  - also: the approval-coverage requirement applies only to a checkpoint that actually carries pending calls, so a foreign suspension with no asks still resumes

## Wave 5 — evidence, gates and documentation

16. [x] `testkit/conformance`, `testkit/storetest`: certify the shipped native runtime.
  - files: `testkit/conformance/native_runtime_test.go`, `testkit/conformance/runtime_test.go`
  - proof: the Runtime and Chain suites run over `core/runtime.Native` and the shipped governed effects, with doubles only at the model and tool ports

17. [x] `std`, `core`: bind the narrowly-covered `chains` scenarios to their full contract.
  - files: `std/explain_contract_test.go`, `core/explain_test.go`, `std/shield_test.go`, `std/presets_test.go`
  - scenarios: `chains.prompt-strings-accounted-for` (real Build/Explain accounting plus prompt-sensitive release identity), `chains.empty-chains` (zero steps and the exact assembled request), `chains.read-only-tools-pay-nothing` (one turn append plus no journal/shield step), `chains.preset-is-copyable`
  - also: `identity.sessions-owner-checked` now asserts the real owner-scoped session listing; the Cancel subtest that previously claimed the ID was renamed

18. [x] `tools`, `examples`, `.github/workflows/ci.yml`: run the gates that exist. Decision D9.
  - files: `Taskfile.yml`, `.github/workflows/ci.yml`, `README.md`
  - targets: `tools:test` and `examples:run`, both wired into CI; the benchmark job runs `task bench:gate` with the pull request's own revisions under a bounded step, and the differential's shared-runner limitation is recorded rather than hidden by invented thresholds

19. [x] `docs`: state what the tree does. Decision D9.
  - files: `README.md`, `docs/overview.md`, `docs/design/scenarios.md`, `CHANGELOG.md`, `docs/design/api-review-m0-5.md`, `openspec/changes/m0-hardening/tasks.md`
  - content: the governed native entry and `Explain` as shipped; the `std/flow` boundary as it is; preset composition as it is; verified scenario, ADR and difference counts; every observed break with its replacement under *Breaking*; the pre-format-change approval checkpoint incompatibility and the `Attach` ownership gap as their own entries

## Verification record

Observed on the final tree, in this order:

```
task tools:test                    27 tests, OK
task lint                          0 issues
task test                          green (root, adapter/otel, examples)
task spec                          registered=515 subtests=1164 covered=294
                                   missing=221 missing_in_scope=0 unregistered=0
task api:check                     exit 0; 13 pre-v1 differences reported; adapter/otel compatible
task examples:run                  exit 0; all five mains ran their documented output
```

Uncached race floor across every module:

```
go test -timeout 2m -race -short -count=1 ./core/... ./std/... ./testkit/...   green (27 packages)
go -C adapter/otel test -timeout 10m -race -short -count=1 ./...                green
go -C examples test -timeout 10m -race -short -count=1 ./...                    green
```

Honest notes on the evidence, so a later reader does not over-read it:

- `missing_in_scope=0` and `covered=294` are the audit baseline restored with the new tests added; the number is a count of scenario IDs with a passing subtest, not of assertions, so the Wave 5 rebindings are what make the three `chains` claims and the session-listing claim mean their contract.
- `TaskModelStreamClosedBufferPreservesTerminalError` was written to be a guard rather than a reproduced red: the closed-buffer/live-error selection is genuinely random per iteration, and the in-test repetition makes the assertion deterministic while the unmodified code misreports at roughly one iteration in twenty thousand. The production change is justified by inspection — the closed-buffer branch previously returned success without consulting the failure channels — but the red proof is weak and is recorded as such.
- The two resumed-run failures first observed (`TestResumeConsumerStallDetaches`, `TestResumeFailureRecordedForAttach`) were test defects, not production ones: a resumed run reuses the run identifier, so its log already holds the pre-suspension prefix and an attach from sequence zero correctly stops at the `Suspended` boundary. The tests now attach from the recorded suspension sequence. The production recording on the resumed path was already correct.
- Recovery reconstructs a running state and writes a driver record carrying only the admitted tool-call total, without the batch subrecord. With a batch subrecord the recovered drive would re-enter the per-ask continuation path and re-suspend the head as an approval instead of completing plain unresolved calls; the bare record keeps recovery on the gated replay path that completes and settles them. Reservation restoration under recovery is not separately asserted beyond the shared seam.
- The benchmark differential runs on the shared CI host, where the chain-overhead budgets are advisory; allocation and byte regressions still fail the step. The environment limitation is recorded in the review document rather than papered over with a runner label.
