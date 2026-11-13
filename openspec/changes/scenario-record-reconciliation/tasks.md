# Tasks

Risk depth: MVP-light (records and metadata; every affected behavior already has a subtest). Scenario IDs are registered at admission (ADR-0171). A task is done only when its acceptance holds and `task lint`, `task spec` and `task test` are green. Cite scenario HEADINGS and IDs, never line numbers: the in-flight structural batch shifts lines mid-edit.

## 1. Decision record

- [ ] 1.1 Write `docs/adr/0189-scenario-record-reconciliation.md` recording the deferral policy (a post-M0 row carries its owning milestone), the eleven ID assignments, the `errors.*` alias sanction, the change-slug origin sanction, and the exception-set policy.
  Acceptance: The ADR exists at the next free number after 0188 and lists every assigned ID with its capability and deferral.

## 2. Records

- [ ] 2.1 Assign IDs to the six cache blocks (`cache.prefix-hit`, `cache.prefix-miss-repopulates`, `cache.scope-isolation`, `cache.hit-usage`, `cache.miss-write-usage`, `cache.hit-miss-metrics`) and the five model blocks (`model.shared-suite-coverage`, `model.anthropic-cache-markers-order`, `model.anthropic-stop-reason-mapped`, `model.anthropic-forced-tool-schema`, `model.anthropic-reasoning-signature`), register the rows (cache M1; model shared-suite M0, anthropic wire-mapping M1), and bind each to a passing subtest named by its ID, reusing existing covering tests where present.
  Acceptance: All eleven IDs pass as subtests; the cache and model specs carry an `ID:` line under every `#### Scenario:` heading.
- [ ] 2.2 Set `deferred_to` on every post-M0 registry row that lacks a deferral: 6 model provider rows M1, 8 runtime eino/bridge rows M2, 6 sandbox docker rows M2, `sandbox.data-analyst-resume-and-page` M2, 4 stores postgres rows M1, `context.research-report-compaction` M2, and the remaining undephased sandbox rows. For the 3 flag-dependent engines rows (`frozen-flag-on-replay`, `live-deny-beats-approval`, `stale-control-state`), verify whether the M0.5 example processes exercise the `flags` capability: if they do, retarget M0.5→M1; if self-contained, keep M0.5 (the M0.5 exit names the three processes offline green) and record the reason.
  Acceptance: No post-M0 row without a passing subtest lacks a deferral; no deferral value is outside the RANKS map; the engines decision is recorded with its evidence.
- [ ] 2.3 Add `#### Scenario:` blocks with the eight repair IDs to the stores and recovery specs, deriving text from `openspec/changes/repair-runtime-safety-contracts/` artifacts and the bound subtests (five short-mode, three behind `//go:build full` in `adapter/postgres/recovery_scenarios_test.go`). Write no new tests for these IDs.
  Acceptance: All eight IDs have spec blocks matching their bound subtests' assertions; the overview exception set no longer needs these rows.
- [ ] 2.4 Register `context.research-report-claim-boundary` matching its sibling row (capability context, owner m2-research-report-example).
  Acceptance: The registry-minus-spec and spec-minus-registry set diffs are empty except the documented exception set.
- [ ] 2.5 Record the `errors.*` alias (four IDs, capability messages) and the change-slug origin vocabulary in `openspec/project.md` and the ADR; rename nothing.
  Acceptance: The convention docs sanction both patterns with their resolution path; the four IDs and their subtests are unchanged.
- [ ] 2.6 Verify `identity.http-authenticator-required`'s owner against `docs/design/architecture.md:169` (httpapi, M4) and keep deferred_to M4; hand the missing M4 scope line to records-map-integrity.
  Acceptance: The deferral is attributable to a named scope line after records-map-integrity lands.
- [ ] 2.7 Resolve the telemetry and conformance records: keep `telemetry.export-lifecycle` at M0 (OTel is M0 scope per overview; its subtest exists at `adapter/otel/export_lifecycle_test.go:177` and the audit miss was a transient leg setup failure), set `telemetry.same-tree-on-all-backends` deferred_to M3 (adk-go leg), and bind the runtime conformance requirement to registered IDs (`runtime.conformance-native`, `runtime.conformance-eino` at M0/M2 ownership, `runtime.conformance-adkgo` deferred M3) derived from the conformance suite.
  Acceptance: Every named row carries an attributable deferral or a passing subtest; the conformance requirement owns registered scenarios; the native/eino conformance subtests pass.
- [ ] 2.8 Update the documented exception list in `docs/overview.md:103` to the settled set with the correct count, and correct "Eight" to the enumerated number.
  Acceptance: The list equals the registry-minus-spec set diff exactly.

## 3. Gate

- [ ] 3.1 Re-run the gate on a stable tree with a warm module cache: `openspec validate scenario-record-reconciliation --strict`, `task spec:types`, `task spec:coverage --gate M0.5` and `task test`.
  Acceptance: Strict validation passes; missing_in_scope is 0 at M0.5 (environment-skipped and full-tag rows carry post-M0.5 deferrals); unregistered stays 0; the short suite passes; any failed leg is named, not masked (requires coverage-leg-failure-visibility).