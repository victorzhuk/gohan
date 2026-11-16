# Tasks

Risk depth: existing-service-strict. Nineteen registry ids in M3 scope have no passing
subtest; each task below turns one group of them into executable evidence, and implements
the requirement where the assertion exposes a gap. No spec text changes.

## 1. Baseline and registry

- [x] 1.1 Record the gate baseline in this change. Acceptance: the evidence quotes
  `task spec:gate GATE=M3` as `summary registered=820 … missing_in_scope=22 unregistered=0
  failed_legs=0` on the tree with every M3 child change archived, and names the three
  blocked `performance.live-*` legs. Verify `timeout 15m task spec:gate GATE=M3`, as a
  record of the starting count only.
  - observed: `summary registered=820 subtests=2913 covered=753 missing=67
    missing_in_scope=22 unregistered=0 failed_legs=0`. The twenty-two in-scope ids are the
    nineteen owned here plus `context.research-report-claim-boundary` (deferred_to M2, still
    missing) and the three blocked legs `performance.live-kafka-refunds-under-load`,
    `performance.live-temporal-travel-under-load`, `performance.live-camunda-invoice-under-load`,
    owned by `m3-s3-loadtest`. All three legs report `deferred_to M3` and no failing leg.
- [x] 1.2 Drop `deferred_to` for each id as its subtest starts passing, and never re-defer an
  id to a later milestone. Acceptance: the gate count falls only together with a passing
  subtest of that exact id, and `jaq -e . openspec/scenarios.json` exits zero after every
  registry edit. Verify `timeout 15m task spec:gate GATE=M3`.
  - observed: eighteen of the nineteen ids are undeferred after their subtest passed —
    the seven release ids, `structured-output.constrained`,
    `interop.awaiting-input-invalid`, `model.version-drift`,
    `stores.pg-auto-migrate-refused-in-prod`, `context.research-report-claim-boundary`,
    `taint.subflow-carries-origin` and six of the seven `subflows.*` ids.
    `jaq -e . openspec/scenarios.json` exited zero after every edit (last: valid, 84
    M3-deferred entries remain). `subflows.child-events` keeps its `deferred_to: "M3"` under
    the contradiction recorded in 2.1; no id was re-deferred and no deferral was added.

## 2. Sub-flows

- [x] 2.1 Assess the seven deferred sub-flow rules against the shipped nesting, fan-out and
  effect machinery: `subflows.fresh-history`, `subflows.child-session`,
  `subflows.output-guarded`, `subflows.effect-derived`, `subflows.effect-tighten-only`,
  `subflows.cancel-propagates`, `subflows.child-events`. Acceptance: the assessment names the
  code path that satisfies each rule or states the gap, without editing any spec.
  - observed: six rules hold or were implemented. `fresh-history` and `child-session` are the
    call boundary itself (`core/flow_tool.go` `runSubflow` builds `types.WithoutRunIdentity`
    plus `ChildRunInfo` and `SessionID = <parent>/<childID>`). `cancel-propagates` inherits the
    parent ctx. `output-guarded` had a real gap: `isControlError`
    (`core/drive_turn.go:1476`) omitted `*types.GuardBlockedError`, so a `StageToolResult`
    block was demoted to an unexecuted tool error; the guard error now steers the run and the
    conversation emits `GuardBlocked` then `Done(guard_blocked)` (`guards` spec :71).
    `effect-derived` and `effect-tighten-only` were a declaration, not a derivation: the tool
    effect is now folded from the child's registered `NativeSpec.Tools`
    (`maxSpecEffect`/`derivedChildEffect`), and an explicit `WithChildEffect` below that floor
    is a construction error.
  - contradiction: `subflows.child-events` cannot be satisfied on this tree. The spec sentence
    is "child events appear with `ParentRunID` set and `Depth == 1`, and only the parent emits
    the final `Done`". Child identity does reach the child ctx (`ChildRunInfo` in
    `runSubflow`), but it is discarded before any consumer: `core/flow.go:74-76` (`FlowFunc`
    runs the child Drive and drops its events), `core/conversation.go:330` and `:381-387`
    (`Send` mints a fresh root run and keeps tree fields only when `a.RunID == runID`), and
    `core/conversation.go:664-671` stamps that re-rooted info; a child's own event log is
    private to its conversation and `runSubflow` relays nothing to the parent's log.
    Satisfying it needs run-adoption at the call boundary plus an event relay — an ADR-scale
    driver change outside this change's assertion scope. The id keeps `deferred_to: "M3"`; no
    deferral is added, and the gate target becomes four (three `performance.live-*` plus this
    one). Driver decision recorded here instead of a spec edit.
- [x] 2.2 Assert the seven ids under the package's existing sub-flow test function, as
  subtests named exactly their ids, extending the current fixtures additively.
  Acceptance: the existing nested, depth and fan-out subtests still pass unchanged. Verify
  `timeout 300s go test -timeout 4m ./std/flow/`.
  - observed: the sub-flow suites live in `core`, not `std/flow`; `timeout 300s go test
    -timeout 4m ./core/ -run 'TestSubflows' -v` exits 0 with
    `subflows.fresh-history`, `subflows.child-session`, `subflows.output-guarded`,
    `subflows.effect-derived`, `subflows.effect-tighten-only` and
    `subflows.cancel-propagates` PASS. All pre-existing `TestSubflowsChain`,
    `TestSubflowsLimits` and fan-out/cost/depth subtests still pass unchanged. The seventh
    id is recorded as the contradiction in 2.1 and is not asserted.
- [x] 2.3 Implement every gap found in 2.1 in the owning package. Acceptance: each subtest
  from 2.2 passes against the behavior, not around it.
  - observed: two real gaps closed in `core`. `FlowAsTool` now derives the tool effect from
    the child's registered `NativeSpec.Tools` (`maxSpecEffect`/`derivedChildEffect` in
    `core/flow_tool.go`), with `WithChildEffect` refused below that floor; the temporary
    `WithDerivedEffect` declaration was deleted, so no public symbol outlives the work.
    `isControlError` (`core/drive_turn.go`) now reports `*types.GuardBlockedError`, so a
    `StageToolResult` block reaches `GuardBlocked`/`Done(guard_blocked)` instead of being
    demoted. Full package green: `timeout 900s go test -timeout 12m ./core/` → `ok`.

## 3. Release

- [x] 3.1 Assess `release.dataset-pinned`, `release.delta-gate`, `release.import-redacted`,
  `release.judge-median`, `release.online-sampling` and `release.pairwise-report` against
  `std/evals`, the release identity surfaces in `core` and the release spec requirements each
  binds to. Acceptance: the assessment names the state the assertion binds to, or the gap.
  - observed: `pairwise-report` and `judge-median` were satisfied by existing state
    (`shadow.go` rows, `judge.go:71 UnstableSpread`, `medianSpread`). Gaps found:
    no pinned-hash verification (`shadow.go:187` computed only), no delta-band decision,
    no `Import` (`record.go` 11 lines), and no deterministic sampler seam.
- [x] 3.2 Assert the six ids beside the release suites, as subtests named exactly their ids.
  Acceptance: the six subtests pass. Verify `timeout 600s go test -timeout 8m ./std/evals/ ./core/`.
  - observed: `timeout 600s go test -timeout 8m ./std/evals/ -run 'TestRelease' -v` exits 0;
    `release.dataset-pinned`, `release.delta-gate`, `release.judge-median` under `TestReleaseGate`,
    `release.import-redacted` under `TestReleaseImport`, `release.online-sampling` under
    `TestReleaseOnline`, `release.pairwise-report` under `TestReleaseShadow` all PASS; every
    pre-existing release subtest unchanged.
- [x] 3.3 Implement every gap found in 3.1 in `std/evals` or `core`. Acceptance: no release
  requirement is asserted through a helper that reimplements the rule under test.
  - observed: `ShadowRun.DatasetHash` + `ErrDatasetPinned` refuse before any case (`shadow.go`);
    `Gate`/`DefaultGate` bands in `release_checks.go`; `Import`/`WithImportRedactor` in
    `record.go`; `Online.sample` unexported seam in `online.go`. `release.delta-gate` and
    `release.judge-median` assert through the exported/extant functions, not local copies.

## 4. Single-id capabilities

- [x] 4.1 `structured-output.constrained`: assert the parent scenario of the constrained
  negotiation path end to end, beside the seven narrower ids `m3-constrained-output`
  registered. Acceptance: the subtest is named exactly `structured-output.constrained`.
  - observed: `TestStructuredOutputHardening/structured-output.constrained` PASS; the request
    carries `Options.ResponseSchema` and one model call yields the value, zero repair turns.
    Assert-only: the constrained path already satisfied the spec sentence.
- [x] 4.2 `interop.awaiting-input-invalid`: assert the interop rule for a malformed awaiting-
  input reply through the MCP server surface. Acceptance: the subtest is named exactly the id.
  - observed: `TestLoopback/interop.awaiting-input-invalid` PASS in `adapter/mcp`: a
    non-conforming delivery returns `ErrInputInvalid` with zero flow resumes, then the same
    token accepts conforming data and the flow resumes once.
- [x] 4.3 `model.version-drift`: read the model spec requirement, then assert it on the
  surface that owns the model catalog. Acceptance: the subtest is named exactly the id.
  - observed: `TestModelVersionDrift/model.version-drift` PASS. Implemented in
    `core/drive_turn.go` `modelEffect`: a served `Usage.ModelVersion` that differs from
    `Profile.Version` counts `gohan.model.version_drift` once with the profile and served
    version, and a `Caps.StrictVersion` profile flushes deltas and fails
    `*types.ModelError{Class: ClassVersionDrift}` before calibration or commit. The failover
    half of the sentence is owned below the driver (`std/route/fallback.go:124`,
    `std/retry/retry.go:109-116` already refuse an in-place retry), so the subtest asserts
    the driver-observable half plus the non-strict and matching-version cases.
- [x] 4.4 `stores.pg-auto-migrate-refused-in-prod`: assert the production refusal in the
  postgres full lane. Acceptance: the subtest is named exactly the id and runs under the
  gate's postgres leg. Verify `timeout 900s go -C adapter/postgres test -timeout 10m -short
  -tags full ./...`.
  - observed: `timeout 300s go -C adapter/postgres test -short -timeout 4m -tags full ./...
    -run 'TestStores' -v` exits 0; `TestStores/stores.pg-auto-migrate-refused-in-prod` PASS.
    Implemented `postgres.Build` (`stack.go`): the refusal precedes any dial and names
    `WithAutoMigrate`.
- [x] 4.5 `context.research-report-claim-boundary`: assert the claim boundary rule the context
  spec states. Acceptance: the subtest is named exactly the id.
  - observed: `TestResearchReportOffline/context.research-report-claim-boundary` PASS in
    `examples/research-report`: the checkpoint carries no active-tool activation payload and no
    replayable call ids, the durable history holds exactly one `search_tools` call, and the
    first resumed request's toolset equals the suspension boundary's.
- [x] 4.6 `taint.subflow-carries-origin`: assert the origin-carrying behavior the taint spec
  states, or record a superseding decision naming the sentence it replaces. Acceptance: the
  milestone conflict is closed either way, and no further deferral is added.
  - observed: asserted, not superseded. `TestSubflowTaint/taint.subflow-carries-origin` PASS:
    a child flow returns `types.Quarantined[invoice]` with a provider origin, the parent's
    sink call receives the extracted `iban`, the run suspends on `HumanApproval`, and
    `ArgOrigins["iban"]` is the provider origin — explicitly not `{OriginTool, "child"}`.
    Closed by two source changes: `core/tool_new.go` `outBlocks` renders one nested
    `ToolResult` per distinct origin (mirroring `RecordQuarantined`), and
    `std/taint/taint.go` anchors each nested block's subtree to its own origin
    (`collectSegments`), which replaces the flat `blockTexts`/`nestedTexts` walk. No public
    API grew. The M1 milestone conflict is closed by behavior.

## 5. Floor

- [x] 5.1 Run the M3 gate. Acceptance: `missing_in_scope=3`, and those three are the blocked
  `performance.live-*` legs with their admission recorded in
  `openspec/changes/m3-s3-loadtest/lane-report.md`. Verify `timeout 15m task spec:gate GATE=M3`.
  - observed: `summary registered=820 subtests=2938 covered=771 missing=49 missing_in_scope=4
    unregistered=0 failed_legs=0`. The fourth in-scope id is `subflows.child-events`, not
    `missing_in_scope=3`: the plan's contingency for an id that cannot be implemented as
    written applies, and the exact spec sentence, the four code sites that drop child
    identity, and the ADR-scale remedy are recorded under task 2.1. The three
    `performance.live-*` legs remain blocked and owned by `m3-s3-loadtest`. Nothing was
    re-deferred; `covered` rose from 753 to 771 (+18 asserted ids) and `subtests` from 2913
    to 2938.
- [x] 5.2 Run the floor. Acceptance: `task spec`, `task lint` and `task test` exit zero on the
  tree with the change applied.
  - observed: `task spec` → `summary … missing_in_scope=0 unregistered=0 failed_legs=0`
    (M0.5 gate). `task lint` → `0 issues` for the root module, the examples config, the
    postgres `pgtxlint` leg and the redis module (run through the CI-pinned
    `golangci-lint@v2.14.0`, since the binary is absent from this environment). `task test`
    → exit 0 across the workspace. One pre-existing load-sensitive hang in
    `std/evals.TestBudget` surfaced under parallel packages; the engine's spend-accrued
    admission rule could skip a racing offer, so the test waited forever for a settle that
    could not happen. The test now gates its wave-1 judge calls on an arrival barrier; every
    assertion is unchanged. Repro `go test -short -p 8 -timeout 2m ./std/...` is green.
