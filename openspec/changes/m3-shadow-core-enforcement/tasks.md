# Tasks

Risk depth: existing-service-strict. The six scenario ids already exist in `openspec/scenarios.json` and leave the `M3` deferred list here.

## 1. Contracts

- [x] 1.1 Add the frozen `types.ShadowSuppressed` sentinel beside `types.ToolError`. Acceptance: the declaration matches the release spec verbatim.
- [x] 1.2 Add `WithShadowMode` and `WithReleaseIdentity`, carrying mode, release id and variant from `Build` through `Stack` into both conversation constructors. Acceptance: a shadow stack starts shadow runs; a champion stack is unchanged.

## 2. Behavior

- [x] 2.1 Derive the shadow session (`<session>/shadow/<runID>`) for the run row and every append while `RunInfo.SessionID` stays the primary session; skip the run-start audit. Acceptance: `release.shadow-isolated`.
- [x] 2.2 Enforce the tool rules at the single batch execution seam that already covers local and provider-executed calls: read-only executes under the ordinary permission decision, journal replay returns `Replayed: true`, a miss returns `ShadowSuppressed`, side-effect provider tools are never declared to the provider, and no model or provider journal write happens under shadow. Acceptance: `release.shadow-suppressed`, `release.shadow-replayed`.
- [x] 2.3 Terminate a shadow suspension in `Lifecycle.drive` with `Done{Reason: ShadowSuspended}` and no checkpoint. Acceptance: `release.shadow-suspension`.
- [x] 2.4 Charge shadow cost to its own ledger under `CostTags.Feature + "/shadow"`. Acceptance: `release.shadow-budget`.
- [x] 2.5 Carry release and variant on spans, audit records, `Done` event meta and `gohan.*` metrics; add `gohan.shadow.runs` and `gohan.shadow.suppressed{tool}`. Acceptance: `release.run-attributes`.

## 3. Integration

- [x] 3.1 Prove every id with an exact-ID subtest in `core/shadow_enforcement_test.go` against memory stores and a fake model. Acceptance: six subtests pass by scenario id.
- [x] 3.2 Un-defer the six ids in `openspec/scenarios.json`, then verify `task spec:types`, `task spec`, `task lint` and `task test`; update `[Unreleased]`. Acceptance: `missing_in_scope=0 unregistered=0 failed_legs=0`, lint clean, short suite green.
