# Tasks

Risk depth: existing-service-strict. Nineteen registry ids in M3 scope have no passing
subtest; each task below turns one group of them into executable evidence, and implements
the requirement where the assertion exposes a gap. No spec text changes.

## 1. Baseline and registry

- [ ] 1.1 Record the gate baseline in this change. Acceptance: the evidence quotes
  `task spec:gate GATE=M3` as `summary registered=820 … missing_in_scope=22 unregistered=0
  failed_legs=0` on the tree with every M3 child change archived, and names the three
  blocked `performance.live-*` legs. Verify `timeout 15m task spec:gate GATE=M3`, as a
  record of the starting count only.
- [ ] 1.2 Drop `deferred_to` for each id as its subtest starts passing, and never re-defer an
  id to a later milestone. Acceptance: the gate count falls only together with a passing
  subtest of that exact id, and `jaq -e . openspec/scenarios.json` exits zero after every
  registry edit. Verify `timeout 15m task spec:gate GATE=M3`.

## 2. Sub-flows

- [ ] 2.1 Assess the seven deferred sub-flow rules against the shipped nesting, fan-out and
  effect machinery: `subflows.fresh-history`, `subflows.child-session`,
  `subflows.output-guarded`, `subflows.effect-derived`, `subflows.effect-tighten-only`,
  `subflows.cancel-propagates`, `subflows.child-events`. Acceptance: the assessment names the
  code path that satisfies each rule or states the gap, without editing any spec.
- [ ] 2.2 Assert the seven ids under the package's existing sub-flow test function, as
  subtests named exactly their ids, extending the current fixtures additively.
  Acceptance: the existing nested, depth and fan-out subtests still pass unchanged. Verify
  `timeout 300s go test -timeout 4m ./std/flow/`.
- [ ] 2.3 Implement every gap found in 2.1 in the owning package. Acceptance: each subtest
  from 2.2 passes against the behavior, not around it.

## 3. Release

- [ ] 3.1 Assess `release.dataset-pinned`, `release.delta-gate`, `release.import-redacted`,
  `release.judge-median`, `release.online-sampling` and `release.pairwise-report` against
  `std/evals`, the release identity surfaces in `core` and the release spec requirements each
  binds to. Acceptance: the assessment names the state the assertion binds to, or the gap.
- [ ] 3.2 Assert the six ids beside the release suites, as subtests named exactly their ids.
  Acceptance: the six subtests pass. Verify `timeout 600s go test -timeout 8m ./std/evals/ ./core/`.
- [ ] 3.3 Implement every gap found in 3.1 in `std/evals` or `core`. Acceptance: no release
  requirement is asserted through a helper that reimplements the rule under test.

## 4. Single-id capabilities

- [ ] 4.1 `structured-output.constrained`: assert the parent scenario of the constrained
  negotiation path end to end, beside the seven narrower ids `m3-constrained-output`
  registered. Acceptance: the subtest is named exactly `structured-output.constrained`.
- [ ] 4.2 `interop.awaiting-input-invalid`: assert the interop rule for a malformed awaiting-
  input reply through the MCP server surface. Acceptance: the subtest is named exactly the id.
- [ ] 4.3 `model.version-drift`: read the model spec requirement, then assert it on the
  surface that owns the model catalog. Acceptance: the subtest is named exactly the id.
- [ ] 4.4 `stores.pg-auto-migrate-refused-in-prod`: assert the production refusal in the
  postgres full lane. Acceptance: the subtest is named exactly the id and runs under the
  gate's postgres leg. Verify `timeout 900s go -C adapter/postgres test -timeout 10m -short
  -tags full ./...`.
- [ ] 4.5 `context.research-report-claim-boundary`: assert the claim boundary rule the context
  spec states. Acceptance: the subtest is named exactly the id.
- [ ] 4.6 `taint.subflow-carries-origin`: assert the origin-carrying behavior the taint spec
  states, or record a superseding decision naming the sentence it replaces. Acceptance: the
  milestone conflict is closed either way, and no further deferral is added.

## 5. Floor

- [ ] 5.1 Run the M3 gate. Acceptance: `missing_in_scope=3`, and those three are the blocked
  `performance.live-*` legs with their admission recorded in
  `openspec/changes/m3-s3-loadtest/lane-report.md`. Verify `timeout 15m task spec:gate GATE=M3`.
- [ ] 5.2 Run the floor. Acceptance: `task spec`, `task lint` and `task test` exit zero on the
  tree with the change applied.
