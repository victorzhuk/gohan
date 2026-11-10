# Tasks

Use the existing-service-strict contract and limits in `design.md`.

## 0. Local pre-fix evidence

- [ ] 0.1 Preserve existing memory/PostgreSQL and public recovery baseline checks before local repairs. Record pre-fix behavior failures for assigned store, Send, Resume, recovery and Attach boundaries. Include source revision, bounded command, scenario, expected behavior and failing assertion. Use real isolated PostgreSQL for its assigned store boundaries. Reject compilation, missing-command and infrastructure errors as reproductions. Supply this evidence to integration-owned parent 1.3.

## 1. Lease and checkpoint stores

- [ ] 1.1 Implement one memory session-exclusion predicate for Start, resume and reclaim. Maintain `onSess` and verify stored reclaim state. Verify `stores.resume-session-exclusive`, `stores.reclaim-session-exclusive` and `stores.reclaim-state-refused`. Parent 4.1.
- [ ] 1.2 Apply acquisition semantics under PostgreSQL session-before-run locks. Verify real-store exclusivity and stale-generation conformance with competing acquisitions. Parent 4.2.
- [ ] 1.3 Implement `CheckpointRuns` in both concrete stores without changing frozen methods. Verify `stores.optional-checkpoint-resume-port`, `stores.resume-current-token-fenced`, one concurrent winner and unchanged newer suspensions on refusal. Parent 4.3.
- [ ] 1.4 Use database time for production ownership/checkpoint operations. Preserve explicit `WithClock` fixtures. Verify `stores.postgres-ownership-database-time` through independent adapters without the override. Parent 4.4.
- [ ] 1.5 Remove lease-derived checkpoint expiry. Add explicit memory/PostgreSQL TTL options and negative-policy refusal before writes. Verify `stores.checkpoint-lifetime-independent` and `stores.checkpoint-lifetime-policy` at store and conversation boundaries. Parent 4.5.
- [ ] 1.6 Bind memory discovery to Runs without nested checkpoint/run locks. Filter current token, consumption, expiry and suspension before limits. Verify `stores.resume-ready-filter-before-limit` with limit one and older ineligible records. Parent 4.6.
- [ ] 1.7 Filter actual preemption reason before limits using existing fields and legacy checkpoint evidence. Verify `stores.preempted-filter-before-limit` with an older human approval. Parent 4.7.
- [ ] 1.8 Update storetest factories, shipped memory wiring, architecture/compatibility docs and changelog. Verify optional support present/absent and full PostgreSQL conformance without a DDL migration. Parent 4.8.

## 2. Public recovery and terminal delivery

- [ ] 2.1 Require token-aware external resume before consumption. Acquire with the expected token. Verify `recovery.optional-token-surface-absent`, pre-consumption token preservation and recovery after post-consumption lease failure. Parent 5.1.
- [ ] 2.2 Consume current preemption tokens conditionally. Remove input-based consumption inference. Verify `recovery.preemption-token-consumed`, `recovery.human-approval-not-preempted` and `recovery.empty-continue-consumed` through public recovery. Parent 5.2.
- [ ] 2.3 Share redacted record-before-delivery behavior across live, resumed and recovered execution. Verify `recovery.eventlog-tail-durable` with another conversation attaching and a bounded completion context. Parent 5.3.
- [ ] 2.4 Check terminal store errors before carried completion events. Verify `runtime.finish-error-no-done`, one terminal boundary and existing steer/limit/handoff behavior. Parent 5.4.
- [ ] 2.5 Persist and restore cumulative admissions once. Preserve prepaid pending calls. Verify `runtime.resume-tool-budget-cumulative` through Send, approval Resume and crash recovery. Parent 5.5.
- [ ] 2.6 Update lifecycle/testing docs and changelog. Verify expiry, optional-store fallback and reconnect instructions against public regressions. Parent 5.6.

## 3. Child acceptance

- [ ] 3.1 Run assigned scenarios and real PostgreSQL/store conformance within the design bounds. Record eligible discovery, token fencing, failure-window and lock-order assertions.
- [ ] 3.2 Supply native/store race evidence for parent integration. Preserve frozen ports, baseline scenario ownership and the independent research-report edits.
