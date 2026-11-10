# Tasks

All work uses the existing-service-strict acceptance contract and resource bounds in `design.md`. Scenario IDs below are proposed until task 1.2 registers them. A task is complete only after its behavior assertions and relevant regression tests pass. Existing tests remain, except the ordinary-receipt reuse expectation explicitly superseded by the normative call-bound contract.

## 1. Contract and compatibility baseline

- [ ] 1.1 Record approval of the design's optional recovery surface, lifetime policy, fingerprint transition, Docker privilege split and proxy contract; verify the approved decisions match `design.md` before source edits.
- [ ] 1.2 Add the next verified ADR, synchronize the eight capability deltas and register their scenario IDs without changing unrelated research-report artifacts; verify `openspec validate repair-runtime-safety-contracts --strict` and `task spec:types`.
- [ ] 1.3 Preserve the existing baseline checks and record focused failing reproductions for the approved public boundaries; verify each failure is a behavior assertion rather than a compilation or infrastructure error.

## 2. Approval and exact argument authority

- [ ] 2.1 Add public native and foreign approval regressions covering later run/call IDs and changed non-fingerprinted arguments; verify `permission.ordinary-approval-call-bound` and `permission.approval-full-arguments-bound` fail before the fix.
- [ ] 2.2 Bind ordinary receipt and context authority to the complete persisted call identity while preserving receipt reads, deduplication and `DiffFromLast`; verify the two scenarios from 2.1 pass and existing scoped-grant cases remain green.
- [ ] 2.3 Revalidate current scopes, exclusions, separation and every quorum vote before execution; verify `permission.tightened-scope-refused`, `permission.tightened-votes-revalidated` and existing duplicate-vote/token-preservation cases pass.
- [ ] 2.4 Add exact numeric canonicalization and a versioned authority fingerprint; refuse legacy rounded grants and retain exact-value history rendering; verify `tools.exact-numeric-fingerprint` and `permission.legacy-rounded-grant-refused`, including equivalent decimal spellings and nested values.
- [ ] 2.5 Implement legacy unresolved journal candidate comparison using exact stored arguments and original entry identity; verify `tools.legacy-intent-exact-match`, successful-new-intent behavior and the documented legacy missing-evidence refusal.
- [ ] 2.6 Update existing compatibility and changelog documentation for ordinary receipt authority and fingerprint rollout; verify the documented mixed-version drain and rollback constraints match the implementation.

## 3. Tool execution boundary

- [ ] 3.1 Resolve one effective advertised host effect and preserve explicit declaration/option precedence; verify `tools.host-effect-default-consistent`, `tools.host-effect-option-consistent` and existing timeout outcome tests through governed execution.
- [ ] 3.2 Install the reserved downstream key on both initial execution and replay; verify `tools.initial-and-replay-key-stable` with completion-loss injection and the existing intent-level cases.
- [ ] 3.3 Preserve exclusion metadata and enforce it recursively during typed argument decoding; verify `identity.excluded-arguments-not-decoded` and `identity.nested-excluded-arguments-not-decoded`, plus malformed/duplicate-key regression cases.
- [ ] 3.4 Update existing tool documentation and `[Unreleased]` entries for effective effects, ignored identity arguments and stable replay keys; verify documented behavior through the public tool scenarios.

## 4. Lease and checkpoint stores

- [ ] 4.1 Implement one session-exclusion predicate for memory Start, resume and reclaim, consistent `onSess` updates and stored-state reclaim checks; verify `stores.resume-session-exclusive`, `stores.reclaim-session-exclusive` and `stores.reclaim-state-refused`.
- [ ] 4.2 Implement the same acquisition semantics under the existing PostgreSQL session-before-run lock order; verify real-store exclusivity and stale-generation conformance with competing acquisitions.
- [ ] 4.3 Implement the optional `CheckpointRuns` surface in both concrete stores without changing frozen methods; verify `stores.resume-current-token-fenced`, exactly one winner under concurrent acquisition and unchanged newer suspensions on refusal.
- [ ] 4.4 Derive production PostgreSQL ownership/checkpoint times from database time while retaining explicit `WithClock` test override behavior; verify `stores.postgres-ownership-database-time` through independent adapter instances and existing clock fixtures.
- [ ] 4.5 Remove lease-derived checkpoint expiry and add explicit memory/PostgreSQL TTL options with negative-policy refusal before writes; verify `stores.checkpoint-lifetime-independent` and `stores.checkpoint-lifetime-policy` at the store and conversation boundaries.
- [ ] 4.6 Bind memory checkpoint recovery discovery to the run surface without nested checkpoint/run locks; filter current token, consumption, expiry and suspended state before the limit in both stores; verify `stores.resume-ready-filter-before-limit` with limit one and older ineligible records.
- [ ] 4.7 Filter actual preemption reason before discovery limits using existing fields and legacy checkpoint evidence; verify `stores.preempted-filter-before-limit` with human approval preceding the eligible run.
- [ ] 4.8 Update storetest factories, shipped memory wiring, existing architecture/compatibility docs and changelog; verify optional-surface present/absent paths and full PostgreSQL conformance without adding a DDL migration.

## 5. Resume, recovery and durable terminal delivery

- [ ] 5.1 Require token-aware external resume support before consumption and use the expected token for lease acquisition; verify `recovery.optional-token-surface-absent`, token preservation on pre-consumption refusals, and recoverability of a recorded decision after lease-acquisition failure.
- [ ] 5.2 Consume current preemption tokens conditionally and remove consumption inference from input values; verify `recovery.preemption-token-consumed`, `recovery.human-approval-not-preempted` and `recovery.empty-continue-consumed` through public recovery.
- [ ] 5.3 Share redacted record-before-delivery behavior between live, resumed and recovered execution; verify `recovery.eventlog-tail-durable` with a second conversation attaching to a recovered run and a bounded completion context.
- [ ] 5.4 Check terminal store errors before yielding carried completion events; verify `runtime.finish-error-no-done`, exactly one terminal boundary and existing steer/limit/handoff behavior.
- [ ] 5.5 Persist and restore cumulative tool admissions once, preserving prepaid pending calls; verify `runtime.resume-tool-budget-cumulative` through Send, approval Resume and crash recovery.
- [ ] 5.6 Update existing lifecycle/testing documentation and changelog; verify documented expiry, optional-store fallback and reconnect behavior match the public regressions.

## 6. Provider and runtime composition

- [ ] 6.1 Add controlled provider HTTP scenarios through Build and native conversations for completed, parallel, forced-schema and unfinished tool blocks; verify the new model scenarios fail against the prior decoders.
- [ ] 6.2 Complete Anthropic argument blocks by index and emit identity-bearing previews; preserve forced-schema text mapping and truncation; verify `model.anthropic-complete-tool-blocks`, `model.provider-argument-preview-identity` and `model.forced-output-and-tool-truncation`.
- [ ] 6.3 Repair OpenAI finalization, optional cache details and argument-preview identity; verify `model.openai-consumer-stop-final`, `model.openai-stream-error-final`, `model.openai-cache-details-absent` and shared preview assertions, including body release.
- [ ] 6.4 Install the existing Eino call-ID handler in its internal agent and test the public foreign constructor; verify `runtime.eino-governed-call-identity` with gate, journal, approval and exactly-once tool execution evidence.
- [ ] 6.5 Update provider fixtures and existing adapter/testing docs and changelog; verify all three adapter conformance suites without upgrading their dependencies.

## 7. Sandbox and outbound network enforcement

- [ ] 7.1 Add controlled Docker attack and setup-failure scenarios through Sandbox and Workspace APIs; verify capability regain or firewall mutation cannot be counted as successful policy enforcement.
- [ ] 7.2 Establish policy in a labelled trusted namespace anchor before starting the workload, keep network administration/counter reads outside workload Exec, drop workload capabilities, enable no-new-privileges and disable inherited healthchecks; verify `sandbox.firewall-authority-isolated` and `sandbox.policy-setup-failure-cleanup`, including a tampered restored startup executable and unchanged UID/file behavior.
- [ ] 7.3 Publish immutable image IDs and support labelled immutable/legacy resources in enumeration and cleanup; verify `sandbox.same-workspace-snapshots-immutable`, `sandbox.snapshot-cas-failure-keeps-content` and `sandbox.legacy-snapshot-ref-readable`.
- [ ] 7.4 Reject CGNAT resolved peers under DenyPrivate while preserving explicit literal exceptions; verify `tools.cgnat-destination-denied` at both range boundaries and existing allow/private cases.
- [ ] 7.5 Pin validated proxy destinations while preserving HTTP/TLS authority, proxy authentication, redirects and release behavior; verify `tools.proxy-destination-pinned` and `tools.proxy-tls-authority-preserved` with controlled divergent DNS.
- [ ] 7.6 Replace public DNS in the standalone Docker allowlist test with existing isolated fixture infrastructure; verify allow/deny outcomes with no public resolver or internet listener dependency.
- [ ] 7.7 Update existing sandbox/egress architecture documentation and changelog; verify the proxy decision explicitly supersedes the archived DNS-risk acceptance and helper cleanup is documented.

## 8. Independent release and compatibility validation

- [ ] 8.1 Pin all landed adapters to the existing root v1 release without third-party upgrades; verify each adapter resolves the declared published dependency with `GOWORK=off`, no replacements and clean manifests. Preserve examples as a development workspace consumer and verify it through `task examples:test`.
- [ ] 8.2 Add a verification-only adapter release task and CI coverage for independent dependency compilation, tidy cleanliness and conformance; verify it rejects replacements and produces no tag or publication.
- [ ] 8.3 Add semantic API regression fixtures for generic method removal, inferred variable types, imported type identity and permitted additive keyed-struct growth; verify the prior checker fails those assertions before replacement.
- [ ] 8.4 Use standard-library type information with module-aware dependency/export loading for API comparison; implement and print the narrowly verified historical adapter baseline correction, reject unrelated load errors, verify all cases from 8.3 and existing v0/v1 reporting tests, then run `task api:check`.
- [ ] 8.5 Handle the fourteen existing unchecked iterator errors without disabling checks or dropping assertions; verify `task lint` reaches and passes both root and examples legs.
- [ ] 8.6 Update existing release, compatibility and testing documentation and changelog; verify every documented verification command exists and no workspace replacement can satisfy a release verdict.

## 9. Integrated acceptance

- [ ] 9.1 Run bounded `task tools:test`, `task lint`, `task spec`, `task test`, `task examples:test` and `task api:check`; verify no required scenario is skipped, unevidenced or flaky and the generated index is clean.
- [ ] 9.2 Run full PostgreSQL/store conformance and relevant native/store/adapter race checks within the design's worker/time bounds; verify lease exclusion, token races, event ordering and helper cleanup under failure.
- [ ] 9.3 Run the M2 milestone gate and independent adapter release verification; report any existing unrelated milestone blockers separately without lowering the accepted floor.
- [ ] 9.4 Reconcile all twenty-four findings against the implementation, regression assertions and remaining risks; verify the five original research-report edits remain intact and no release, tag rewrite or production migration was performed.
