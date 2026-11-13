# Tasks

Risk depth: existing-service-strict. Shipped behavior is ground truth; every edit transcribes it. Scenario IDs are proposed at authoring and registered at admission (ADR-0171). A task is done only when its acceptance holds and `task lint`, `task spec` and `task test` are green. Cite requirement headings and scenario IDs, not line numbers: the in-flight structural batch shifts lines.

## 1. Decision record

- [x] 1.1 Write `docs/adr/0187-contract-drift-alignment.md` recording design.md decisions 1-4 with the audit evidence citations.
  Acceptance: The ADR names distinct ledger and `runtime.Batch.Run` refusals, discovery errors and supported skips, the construction vocabulary and the secret-injection boundary. Required gates pass.

## 2. Spec alignment

- [x] 2.1 Apply the limits delta: replace the Run limits requirement body and refine `limits.provider-calls-count` THEN per the delta; verify the Contract defaults paragraph already states StopLimit correctly.
  Acceptance: The requirement matches the delta byte-for-byte. The scenario verifies the ledger refusal and unchanged budget. Native driver propagation is verified separately. `task spec:types` passes.
- [x] 2.2 Apply the flow delta: add the Native definition construction requirement, register `flow.native-definition-resolved-once` and `flow.native-tools-single-source` in `openspec/scenarios.json` (capability flow, origin ADR-0187), bind each to a passing subtest named by its ID (reuse an existing covering test where one exists, else write it), and replace the two `agent.New*` constructor-table rows with `gohan.WithNativeAgent`/`gohan.NewNativeConversation`.
  Acceptance: Both scenario IDs pass as subtests; the table carries no runtime-injection constructor; the single-source invariant matches `core/native_options.go` resolution behavior verified at admission.
- [x] 2.3 Align the runtime Contract: distinguish direct ledger refusal from the refunded and translated `runtime.Batch.Run` refusal. Separate new-send `Runs.Start` from resume acquisition through `CheckpointRuns.ResumingCheckpoint`.
  Acceptance: The scenario identifies the `runtime.Batch.Run` refusal path. Evidence covers the driver translation and the direct ledger error. The existing batch subtest alone does not prove driver translation.
- [x] 2.4 Align recovery Contract and requirements: describe preempted token fencing, consumed-checkpoint discovery errors, and candidate skips after successful discovery.
  Acceptance: Recovery propagates discovery errors before per-candidate support checks. Unbound memory discovery and successful discovery with unsupported run-store candidates have evidence. All recovery scenario subtests pass.
- [x] 2.5 Align the stores Contract recovery-discovery and suspend bullets: name token-fenced paths, discovery failure and conditional skip behavior. Preserve the frozen `Consume` and `Resuming(runID)` methods.
  Acceptance: The bullets agree with the `CheckpointRuns` contract in the same spec; stores scenario subtests pass.
- [x] 2.6 Align the chains Contract: the model-chain limits label distinguishes counter stops from cost/wall-clock aborts; the constructor sentence names the definition's `ModelChain`/`ToolChain` fields instead of `agent.New`.
  Acceptance: No `MaxTurns` hard-abort label and no `agent.New` reference remains in chains; chains scenario subtests pass.
- [x] 2.7 Align sandbox rule 2 to `SecretResolver` and add `SetEnv(ctx context.Context, env map[string]string) error` to the normative `Workspace` interface block.
  Acceptance: The block matches the frozen shape in `docs/design/compatibility.md:85` and `core/types/sandbox.go:77-84`; rule 2 agrees with the secret-injection requirement; sandbox scenario subtests pass.
- [x] 2.8 Align the identity ctx-propagation table sandbox row to `SecretResolver`.
  Acceptance: The cell agrees with the identity sandbox-credential requirement; identity scenario subtests pass.

## 3. Gate

- [x] 3.1 Run the change gate: `openspec validate repair-contract-drift --strict`, `task spec:types`, `task spec:coverage` and `task test`.
  Acceptance: Strict validation passes; the types index regenerates clean; coverage reports no new missing_in_scope beyond the pre-change baseline (36 at M0.5, owned by scenario-record-reconciliation); the short suite passes.

### Verification state

- Strict validation of `repair-contract-drift` passed before the deltas were applied to the main specs.
- `task spec:types` regenerates clean: 327 types, 119 funcs, 237 consts, 45 errors, no duplicates or undefined identifiers.
- `task spec:coverage` passes: `registered=734 subtests=2569 covered=611 missing=123 missing_in_scope=0 unregistered=0`; every missing scenario is deferred above M0.5.
- `task spec:gate` (M0.5) passes: `registered=734 subtests=2630 covered=639 missing=95 missing_in_scope=0 unregistered=0`.
- `task test` passes every leg, including `adapter/docker` and `adapter/postgres` against the local Docker daemon.
- `task lint` passes with zero issues in both lint legs.
- `timeout 240s go test -short -timeout 2m -p 2 -parallel 2 ./core -run 'TestProviderToolUsage|TestRecoveryContract|TestRuntimeBatchProtocol|TestBatchOverrunTranslatesToLimitExceeded|TestNativeDefinitionScenarios' -v` passed. The provider-count, both discovery cases, batch-limit, driver-translation and native-definition tests executed.
- The translated batch refusal relies on `TestBatchOverrunTranslatesToLimitExceeded` and `TestNativeReservationOrderAndRefund` without a dedicated registered scenario; the refund's ledger effect has no end-to-end assertion.
- The `limits` and `flow` normative edits live only in their deltas; the archive step applies them to `openspec/specs`. Runtime, recovery, stores, chains, sandbox and identity alignments are applied directly to their specs, including the runtime scenario and recovery requirements.