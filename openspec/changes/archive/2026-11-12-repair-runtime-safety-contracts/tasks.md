# Tasks

All work uses the existing-service-strict acceptance contract and resource bounds in `design.md`. Scenario IDs below are proposed until task 1.2 registers them. A task is complete only after its behavior assertions and relevant regression tests pass. Existing tests remain, except the ordinary-receipt reuse expectation explicitly superseded by the normative call-bound contract.

## 1. Contract and compatibility baseline

- [x] 1.1 Record approval of the design's optional recovery surface, lifetime policy, fingerprint transition, Docker privilege split and proxy contract; verify the approved decisions match `design.md` before source edits.
  - Observed evidence: `docs/adr/0170-governed-call-authority-and-runtime-repair-baseline.md` records owner approval of the five decisions; content matches parent decisions 2, 5, 6, 9 and 10 (verified during integration apply; `scratchpad/apply-repair-runtime-safety-contracts/docs-evidence.md` row 1.1).
- [x] 1.2 Add the next verified ADR, synchronize the eight capability deltas and register their scenario IDs without changing unrelated research-report artifacts; verify `openspec validate repair-runtime-safety-contracts --strict` and `task spec:types`.
  - Observed evidence: integration re-verification on merged tree c470a34 — `openspec validate repair-runtime-safety-contracts --strict` exit 0; `task spec:types` exit 0 with `{type: 282, func: 82, const: 200, error: 32}`, no dups/undefined, `docs/design/types.md` unchanged; eight deltas under `openspec/changes/repair-runtime-safety-contracts/specs/`; all 45 umbrella IDs registered exactly once in `openspec/scenarios.json`; archived research-report change untouched.
- [x] 1.3 Preserve the existing baseline checks and record focused failing reproductions for the approved public boundaries; verify each failure is a behavior assertion rather than a compilation or infrastructure error.
  - Observed evidence: all six boundary families carry source revision, bounded command, scenario, expected behavior and failing assertion; compilation/infra failures kept separate (integration walk of `openspec/changes/repair-*/tasks.md` evidence and `scratchpad/repair-*/`; docs-evidence.md row 1.3).

## 2. Approval and exact argument authority

- [x] 2.1 Add public native and foreign approval regressions covering later run/call IDs and changed non-fingerprinted arguments; verify `permission.ordinary-approval-call-bound` and `permission.approval-full-arguments-bound` fail before the fix.
  - Observed evidence: both scenario IDs pass as exact subtests on the merged tree (coverage log + scenario matrix); red-first establishment recorded in child evidence `openspec/changes/repair-call-authority/tasks.md` 2.1 (commits 74f608d..47a54f3).
- [x] 2.2 Bind ordinary receipt and context authority to the complete persisted call identity while preserving receipt reads, deduplication and `DiffFromLast`; verify the two scenarios from 2.1 pass and existing scoped-grant cases remain green.
  - Observed evidence: both call-binding scenarios pass with scoped-grant cases green (coverage log; child evidence 2.2).
- [x] 2.3 Revalidate current scopes, exclusions, separation and every quorum vote before execution; verify `permission.tightened-scope-refused`, `permission.tightened-votes-revalidated` and existing duplicate-vote/token-preservation cases pass.
  - Observed evidence: both scenario IDs pass with duplicate-vote/token-preservation cases (coverage log; child evidence 2.3).
- [x] 2.4 Add exact numeric canonicalization and a versioned authority fingerprint; refuse legacy rounded grants and retain exact-value history rendering; verify `tools.exact-numeric-fingerprint` and `permission.legacy-rounded-grant-refused`, including equivalent decimal spellings and nested values.
  - Observed evidence: both scenario IDs pass (coverage log; child evidence 2.4).
- [x] 2.5 Implement legacy unresolved journal candidate comparison using exact stored arguments and original entry identity; verify `tools.legacy-intent-exact-match`, successful-new-intent behavior and the documented legacy missing-evidence refusal.
  - Observed evidence: `tools.legacy-intent-exact-match` passes with new-intent and missing-evidence refusals (coverage log; child evidence 2.5).
- [x] 2.6 Update existing compatibility and changelog documentation for ordinary receipt authority and fingerprint rollout; verify the documented mixed-version drain and rollback constraints match the implementation.
  - Observed evidence: `docs/design/compatibility.md:40-46` (receipt authority, exact-v1, drain/rollback), `CHANGELOG.md:45-46` ([Unreleased] fingerprints/legacy renewal) match implementation (docs-evidence.md row 2.6).

## 3. Tool execution boundary

- [x] 3.1 Resolve one effective advertised host effect and preserve explicit declaration/option precedence; verify `tools.host-effect-default-consistent`, `tools.host-effect-option-consistent` and existing timeout outcome tests through governed execution.
  - Observed evidence: both scenario IDs pass with existing timeout outcomes (coverage log; child evidence 3.1).
- [x] 3.2 Install the reserved downstream key on both initial execution and replay; verify `tools.initial-and-replay-key-stable` with completion-loss injection and the existing intent-level cases.
  - Observed evidence: scenario passes with completion-loss injection (coverage log; child evidence 3.2).
- [x] 3.3 Preserve exclusion metadata and enforce it recursively during typed argument decoding; verify `identity.excluded-arguments-not-decoded` and `identity.nested-excluded-arguments-not-decoded`, plus malformed/duplicate-key regression cases.
  - Observed evidence: both scenario IDs pass with malformed/duplicate-key cases (coverage log; child evidence 3.3).
- [x] 3.4 Update existing tool documentation and `[Unreleased]` entries for effective effects, ignored identity arguments and stable replay keys; verify documented behavior through the public tool scenarios.
  - Observed evidence: `CHANGELOG.md:47-49` ([Unreleased] effective effect precedence, reserved replay key, nested excluded args) match public scenarios (docs-evidence.md row 3.4).

## 4. Lease and checkpoint stores

- [x] 4.1 Implement one session-exclusion predicate for memory Start, resume and reclaim, consistent `onSess` updates and stored-state reclaim checks; verify `stores.resume-session-exclusive`, `stores.reclaim-session-exclusive` and `stores.reclaim-state-refused`.
  - Observed evidence: all three scenario IDs pass (coverage log; child evidence `openspec/changes/repair-token-recovery/tasks.md` 4.1).
- [x] 4.2 Implement the same acquisition semantics under the existing PostgreSQL session-before-run lock order; verify real-store exclusivity and stale-generation conformance with competing acquisitions.
  - Observed evidence: real-store exclusivity passes — `task test:full` adapter/postgres leg exit 0 and `task adapters:verify` postgres conformance ok 13.728s with real testcontainer (child evidence 4.2).
- [x] 4.3 Implement the optional `CheckpointRuns` surface in both concrete stores without changing frozen methods; verify `stores.resume-current-token-fenced`, exactly one winner under concurrent acquisition and unchanged newer suspensions on refusal.
  - Observed evidence: `stores.resume-current-token-fenced` and `stores.optional-checkpoint-resume-port` pass; one-winner and newer-suspension preservation in full conformance (coverage log; child evidence 4.3).
- [x] 4.4 Derive production PostgreSQL ownership/checkpoint times from database time while retaining explicit `WithClock` test override behavior; verify `stores.postgres-ownership-database-time` through independent adapter instances and existing clock fixtures.
  - Observed evidence: `TestPostgres/stores.postgres-ownership-database-time` passes full-tag with testcontainer (exact-subtest rerun ok 5.926s, exit 0) and in the M0.5/M2 gate legs (scenario matrix).
- [x] 4.5 Remove lease-derived checkpoint expiry and add explicit memory/PostgreSQL TTL options with negative-policy refusal before writes; verify `stores.checkpoint-lifetime-independent` and `stores.checkpoint-lifetime-policy` at the store and conversation boundaries.
  - Observed evidence: both scenario IDs pass at store and conversation boundaries (coverage log; child evidence 4.5).
- [x] 4.6 Bind memory checkpoint recovery discovery to the run surface without nested checkpoint/run locks; filter current token, consumption, expiry and suspended state before the limit in both stores; verify `stores.resume-ready-filter-before-limit` with limit one and older ineligible records.
  - Observed evidence: scenario passes with limit one and older ineligible records (coverage log; child evidence 4.6).
- [x] 4.7 Filter actual preemption reason before discovery limits using existing fields and legacy checkpoint evidence; verify `stores.preempted-filter-before-limit` with human approval preceding the eligible run.
  - Observed evidence: scenario passes with human approval ahead of eligible preemption (coverage log; child evidence 4.7).
- [x] 4.8 Update storetest factories, shipped memory wiring, existing architecture/compatibility docs and changelog; verify optional-surface present/absent paths and full PostgreSQL conformance without adding a DDL migration.
  - Observed evidence: `docs/design/architecture.md:96-104`, `docs/design/compatibility.md:22-24` (frozen Runs set, additive growth, no DDL), `CHANGELOG.md:20`; optional-surface present/absent paths pass (docs-evidence.md row 4.8; wiring: examples commit 8003ca6).

## 5. Resume, recovery and durable terminal delivery

- [x] 5.1 Require token-aware external resume support before consumption and use the expected token for lease acquisition; verify `recovery.optional-token-surface-absent`, token preservation on pre-consumption refusals, and recoverability of a recorded decision after lease-acquisition failure.
  - Observed evidence: scenario passes with token preservation and post-failure recoverability (coverage log; child evidence 5.1).
- [x] 5.2 Consume current preemption tokens conditionally and remove consumption inference from input values; verify `recovery.preemption-token-consumed`, `recovery.human-approval-not-preempted` and `recovery.empty-continue-consumed` through public recovery.
  - Observed evidence: all three scenario IDs pass through public recovery (coverage log; child evidence 5.2).
- [x] 5.3 Share redacted record-before-delivery behavior between live, resumed and recovered execution; verify `recovery.eventlog-tail-durable` with a second conversation attaching to a recovered run and a bounded completion context.
  - Observed evidence: scenario passes with second-conversation Attach (coverage log; child evidence 5.3).
- [x] 5.4 Check terminal store errors before yielding carried completion events; verify `runtime.finish-error-no-done`, exactly one terminal boundary and existing steer/limit/handoff behavior.
  - Observed evidence: scenario passes with exactly one terminal boundary and existing steer/limit/handoff cases (coverage log; child evidence 5.4).
- [x] 5.5 Persist and restore cumulative tool admissions once, preserving prepaid pending calls; verify `runtime.resume-tool-budget-cumulative` through Send, approval Resume and crash recovery.
  - Observed evidence: scenario passes through Send, approval Resume and crash recovery (coverage log; child evidence 5.5).
- [x] 5.6 Update existing lifecycle/testing documentation and changelog; verify documented expiry, optional-store fallback and reconnect behavior match the public regressions.
  - Observed evidence: `docs/design/architecture.md:104-108` (ErrCheckpointIncompatible fallback, conditional consume, redacted tail + fresh Attach), `docs/design/testing.md:11-12`, `CHANGELOG.md:20,38` (docs-evidence.md row 5.6).

## 6. Provider and runtime composition

- [x] 6.1 Add controlled provider HTTP scenarios through Build and native conversations for completed, parallel, forced-schema and unfinished tool blocks; verify the new model scenarios fail against the prior decoders.
  - Observed evidence: controlled HTTP scenarios reach Build and native conversations; red-first establishment in child evidence `openspec/changes/repair-provider-composition/tasks.md` 6.1; all model scenarios pass on merged tree (coverage log).
- [x] 6.2 Complete Anthropic argument blocks by index and emit identity-bearing previews; preserve forced-schema text mapping and truncation; verify `model.anthropic-complete-tool-blocks`, `model.provider-argument-preview-identity` and `model.forced-output-and-tool-truncation`.
  - Observed evidence: all three scenario IDs pass (coverage log; adapters:verify conformance stream shows them passing in the isolated anthropic module).
- [x] 6.3 Repair OpenAI finalization, optional cache details and argument-preview identity; verify `model.openai-consumer-stop-final`, `model.openai-stream-error-final`, `model.openai-cache-details-absent` and shared preview assertions, including body release.
  - Observed evidence: all three scenario IDs pass with body-release assertions (coverage log; child evidence 6.3).
- [x] 6.4 Install the existing Eino call-ID handler in its internal agent and test the public foreign constructor; verify `runtime.eino-governed-call-identity` with gate, journal, approval and exactly-once tool execution evidence.
  - Observed evidence: scenario passes through the public foreign constructor (coverage log; child evidence 6.4).
- [x] 6.5 Update provider fixtures and existing adapter/testing docs and changelog; verify all three adapter conformance suites without upgrading their dependencies.
  - Observed evidence: `docs/design/adapters.md:62-69`, `docs/design/testing.md:13-14`, `CHANGELOG.md:33-37`; anthropic/openai/eino conformance suites pass inside `task adapters:verify` without dependency upgrades (docs-evidence.md row 6.5).

## 7. Sandbox and outbound network enforcement

- [x] 7.1 Add controlled Docker attack and setup-failure scenarios through Sandbox and Workspace APIs; verify capability regain or firewall mutation cannot be counted as successful policy enforcement.
  - Observed evidence: attack and setup-failure scenarios pass through public Sandbox/Workspace APIs (coverage log; child evidence `openspec/changes/repair-sandbox-isolation/tasks.md` 7.1).
- [x] 7.2 Establish policy in a labelled trusted namespace anchor before starting the workload, keep network administration/counter reads outside workload Exec, drop workload capabilities, enable no-new-privileges and disable inherited healthchecks; verify `sandbox.firewall-authority-isolated` and `sandbox.policy-setup-failure-cleanup`, including a tampered restored startup executable and unchanged UID/file behavior.
  - Observed evidence: `sandbox.firewall-authority-isolated`, `sandbox.policy-setup-failure-cleanup` and `sandbox.restored-startup-policy-first` pass (coverage log; child evidence 7.2).
- [x] 7.3 Publish immutable image IDs and support labelled immutable/legacy resources in enumeration and cleanup; verify `sandbox.same-workspace-snapshots-immutable`, `sandbox.snapshot-cas-failure-keeps-content` and `sandbox.legacy-snapshot-ref-readable`.
  - Observed evidence: all three scenario IDs pass — snapshot pair via exact-subtest rerun ok 5.549s and in `-short` gate legs after removing the stale `testing.Short()` skip (adapter/docker/snapshot_test.go, apply fix); CAS case via coverage log (scenario matrix).
- [x] 7.4 Reject CGNAT resolved peers under DenyPrivate while preserving explicit literal exceptions; verify `tools.cgnat-destination-denied` at both range boundaries and existing allow/private cases.
  - Observed evidence: scenario passes at both range boundaries with allow/private cases (coverage log; child evidence `openspec/changes/repair-network-pinning/tasks.md` 7.4).
- [x] 7.5 Pin validated proxy destinations while preserving HTTP/TLS authority, proxy authentication, redirects and release behavior; verify `tools.proxy-destination-pinned` and `tools.proxy-tls-authority-preserved` with controlled divergent DNS.
  - Observed evidence: both scenario IDs pass with controlled divergent DNS (coverage log; child evidence 7.5).
- [x] 7.6 Replace public DNS in the standalone Docker allowlist test with existing isolated fixture infrastructure; verify allow/deny outcomes with no public resolver or internet listener dependency.
  - Observed evidence: allowlist outcomes verified on isolated fixtures with no public resolver (coverage log; child evidence 7.6).
- [x] 7.7 Update existing sandbox/egress architecture documentation and changelog; verify the proxy decision explicitly supersedes the archived DNS-risk acceptance and helper cleanup is documented.
  - Observed evidence: `docs/design/architecture.md:180-190` (anchor boundary, labels/cleanup, immutable snapshots, CGNAT + proxy pinning; explicit supersession citing ADR 0170 section 5), `docs/design/compatibility.md:38`, `CHANGELOG.md:28-31` (docs-evidence.md row 7.7).

## 8. Independent release and compatibility validation

- [x] 8.1 Pin all landed adapters to the existing root v1 release without third-party upgrades; verify each adapter resolves the declared published dependency with `GOWORK=off`, no replacements and clean manifests. Preserve examples as a development workspace consumer and verify it through `task examples:test`.
  - Observed evidence: `task adapters:verify` all five adapters PASS with GOWORK=off and no replacements; `task examples:test` exit 0 (7 example packages ok).
- [x] 8.2 Add a verification-only adapter release task and CI coverage for independent dependency compilation, tidy cleanliness and conformance; verify it rejects replacements and produces no tag or publication.
  - Observed evidence: `task adapters:verify` exit 0 producing no tag/publication (`git tag --points-at HEAD` empty after runs; tag list unchanged); `TestReleaseVerificationCLI` green after removing the stray `/tmp/.git` that poisoned `go build -buildvcs=auto` under /tmp (environment defect, repaired `rmdir /tmp/.git`).
- [x] 8.3 Add semantic API regression fixtures for generic method removal, inferred variable types, imported type identity and permitted additive keyed-struct growth; verify the prior checker fails those assertions before replacement.
  - Observed evidence: semantic fixtures red-first against the prior checker per child evidence `openspec/changes/repair-release-verification/tasks.md` 8.3; fixtures pass with the replaced checker (tools/apicheck tests green).
- [x] 8.4 Use standard-library type information with module-aware dependency/export loading for API comparison; implement and print the narrowly verified historical adapter baseline correction, reject unrelated load errors, verify all cases from 8.3 and existing v0/v1 reporting tests, then run `task api:check`.
  - Observed evidence: `task api:check` exit 0 — root compatible with v0.2.0; adapter/otel compatible with printed verified correction (tags peel to same release commit 4ce59f7; root req corrected v0.0.0 -> v0.1.0); `TestHistoricalBaselineCLI` green after isolating the fixture module cache (apply fix: proxyEnv sets per-test GOMODCACHE so the stub proxy stays authoritative).
- [x] 8.5 Handle the fourteen existing unchecked iterator errors without disabling checks or dropping assertions; verify `task lint` reaches and passes both root and examples legs.
  - Observed evidence: `task lint` exit 0, both root and examples legs report 0 issues (integration apply run).
- [x] 8.6 Update existing release, compatibility and testing documentation and changelog; verify every documented verification command exists and no workspace replacement can satisfy a release verdict.
  - Observed evidence: `docs/design/compatibility.md:12-14,56-58`, `docs/design/testing.md:17-19`, `CHANGELOG.md:9` (adapters:verify without tag/publication) (docs-evidence.md row 8.6).

## 9. Integrated acceptance

- [x] 9.1 Run bounded `task tools:test`, `task lint`, `task spec`, `task test`, `task examples:test` and `task api:check`; verify no required scenario is skipped, unevidenced or flaky and the generated index is clean.
  - Observed evidence: all six gates exit 0 on merged tree (apply run; scratchpad/apply-repair-runtime-safety-contracts/integration-evidence.md row 9.1). `task spec` first failed at `missing_in_scope=2` because `TestSandboxSnapshotIsolation` skipped under `-short`, hiding registered M0 scenarios; fixed by removing the stale skip (daemon-availability guard retained per package convention); post-fix gate summary `registered=719 subtests=2520 covered=624 missing=95 missing_in_scope=0 unregistered=0`, types index clean. 45/45 umbrella scenario IDs evidenced (scenario-matrix.md: 42 via coverage log, 3 via exact-subtest reruns).
- [x] 9.2 Run full PostgreSQL/store conformance and relevant native/store/adapter race checks within the design's worker/time bounds; verify lease exclusion, token races, event ordering and helper cleanup under failure.
  - Observed evidence: `task test:full` exit 0 (adapter/postgres full conformance with testcontainer; root full-tag green incl. fresh apicheck after fixture fix); `task test:race` exit 0 (adapter/docker race ok 141.865s, adapter/postgres race ok 6.876s, root tree race green). First runs failed from driver-side contention (9.2 and 9.3 jobs parallel on one Docker daemon/fixture space) and the stray `/tmp/.git` environment defect — root causes identified and repaired; solo reruns green. Flaky behavior: none required.
- [x] 9.3 Run the M2 milestone gate and independent adapter release verification; report any existing unrelated milestone blockers separately without lowering the accepted floor.
  - Observed evidence: `task spec:gate GATE=M2` exit 0 (no in-scope missing at M2; M3/M4 deferrals remain future-milestone work, reported by the gate itself); `task adapters:verify` exit 0 with all five adapters verified (GOWORK=off, published deps, tidy, conformance); no tag or publication (`git tag --points-at HEAD` empty).
- [x] 9.4 Reconcile all twenty-four findings against the implementation, regression assertions and remaining risks; verify the five original research-report edits remain intact and no release, tag rewrite or production migration was performed.
  - Observed evidence: findings 1-24 reconciled through the decision/coverage map — every finding's scenarios execute and pass on the merged tree (matrix 45/45); research-report: `git diff e260976..HEAD -- openspec/changes/archive/2026-11-08-m2-research-report-example` empty, `examples/research-report` delta is exactly the sanctioned checkpoint-runs wiring (+3/-1, commit 8003ca6), original edits intact; no SQL/migration diff in the repair range; root `go.mod` stdlib-only; no tag rewrite. Residual risks recorded in scratchpad/apply-repair-runtime-safety-contracts/integration-evidence.md row 9.4 (incl. pre-existing archive-time duplicate ADDED header INFO in the tools delta).

## Archive disposition

All eight capability deltas are already merged into `openspec/specs/*/spec.md`: 18 requirements and 46 scenario IDs match by name and body, so the archive needs no further sync.

Two residuals stay open outside this change and are owned elsewhere. The eight registry rows without spec blocks (`stores.reclaim-oldest-first-exclusive`, `stores.start-refused-reclaimed-lease`, `stores.resume-older-owner-blocks-start`, `stores.resume-ready-reentrant-run-port`, `stores.preempted-rank-before-decode`, `recovery.consumed-token-fenced`, `recovery.eventlog-bound-outlives-drive`, `recovery.redaction-tenant-continuity`) are tasks 2.3 of `scenario-record-reconciliation`. The three `repair-release-verification` runtime requirements remain in that child's delta without registry rows; that child is outside this sprint and keeps the finding in the mirror.

Package check: `openspec validate repair-runtime-safety-contracts --strict`. This check validates the planning artifacts only. It supplies no external acceptance, activation, or publication evidence.
