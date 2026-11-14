# ADR-0189: Scenario record reconciliation

Date: 2026-11-14
Status: Proposed (`scenario-record-reconciliation`)
Supersedes: none

## Context

The M0.5 coverage baseline reported `registered=749 subtests=2591 covered=626 missing=123 missing_in_scope=4 unregistered=13 failed_legs=0`. The four in-scope rows are `stores.postgres-ownership-database-time`, `stores.reclaim-oldest-first-exclusive`, `stores.resume-older-owner-blocks-start` and `stores.start-refused-reclaimed-lease`. Their subtests in `adapter/postgres/recovery_scenarios_test.go` require `//go:build full`, which the short coverage legs in `Taskfile.yml` do not set. All four carry `deferred_to: M1`.

The 13 unregistered subtests are the eleven cache and model IDs below and `runtime.conformance-native` and `runtime.conformance-eino`. Their registry rows are part of this reconciliation.

## Decision

### Deferrals and prerequisites

A scenario whose covering module ships after M0 carries `deferred_to` equal to the owning milestone. The milestone comes from the scope lines in `docs/overview.md` section 12 and `docs/design/architecture.md`. A row without `deferred_to` ranks M0; that default stays. Values are restricted to the coverage tool's `RANKS` keys: `M0`, `M0.5`, `M1`, `M2`, `M3` and `M4`. Do not write `M0` or `M0.5` as a deferral on a row without a passing subtest: both are at or before the default gate.

Environment prerequisites do not move a milestone. The Docker daemon prerequisite for `adapter/docker` and the `//go:build full` prerequisite for `adapter/postgres` do not change the module's M2 and M1 ownership, respectively. The gate excludes these rows by rank.

### Assigned scenario IDs

| ID | Capability | Owning milestone | Requirement |
|---|---|---|---|
| `cache.prefix-hit` | cache | M1 | Exact-match prefix cache |
| `cache.prefix-miss-repopulates` | cache | M1 | Exact-match prefix cache |
| `cache.scope-isolation` | cache | M1 | Exact-match prefix cache |
| `cache.hit-usage` | cache | M1 | Cache usage reporting |
| `cache.miss-write-usage` | cache | M1 | Cache usage reporting |
| `cache.hit-miss-metrics` | cache | M1 | Cache metrics |
| `model.shared-suite-coverage` | model | M0 | Shared provider conformance contract |
| `model.anthropic-cache-markers-order` | model | M1 | Anthropic adapter wire mapping |
| `model.anthropic-stop-reason-mapped` | model | M1 | Anthropic adapter wire mapping |
| `model.anthropic-forced-tool-schema` | model | M1 | Anthropic adapter wire mapping |
| `model.anthropic-reasoning-signature` | model | M1 | Anthropic adapter wire mapping |

Two bindings are weaker than their scenario titles and are recorded, not repaired. The Anthropic cache-marker subtest asserts the marker count, not their order. The shared-suite binding runs the suite instead of enumerating it.

### Repair scenario blocks

The eight `repair-runtime-safety-contracts` IDs gain blocks under existing requirements. No test was written for them.

| ID | Requirement |
|---|---|
| `stores.reclaim-oldest-first-exclusive` | Every lease acquisition preserves session exclusivity |
| `stores.start-refused-reclaimed-lease` | Every lease acquisition preserves session exclusivity |
| `stores.resume-older-owner-blocks-start` | Every lease acquisition preserves session exclusivity |
| `stores.resume-ready-reentrant-run-port` | Recovery discovery limits eligible current checkpoints |
| `stores.preempted-rank-before-decode` | Recovery discovery limits eligible current checkpoints |
| `recovery.consumed-token-fenced` | Automatic preemption recovery consumes its exact token |
| `recovery.eventlog-bound-outlives-drive` | Recovery records the public event stream durably |
| `recovery.redaction-tenant-continuity` | Recovery records the public event stream durably |

### Registry conventions

The four IDs `errors.every-sentinel-has-code`, `errors.problem-of-unknown-is-internal`, `errors.detail-never-carries-provider-body` and `errors.retry-after-on-retryable` remain unchanged. They belong to capability `messages`, with origin `R52` and subtests in `core/types/problem_test.go` and `core/types/problem_detail_test.go`. The registry row's `capability` field decides ownership, never the ID prefix.

`origin` accepts a requirement label (`R…`), `ADR-NNNN`, or the slug of the change that added the row. It records provenance, not acceptance. Acceptance reads `deferred_to` plus `task spec:coverage`, never `origin`. A slug without a live change directory is historic provenance and stays.

`docs/overview.md` enumerates registered IDs without a `#### Scenario:` block in the main specs, with a count equal to the enumeration. A change that adds or removes a member updates that paragraph in the same commit. A row leaves the list when its block lands.

### Conformance and attribution

The runtime conformance requirement binds `runtime.conformance-native` at M0 and `runtime.conformance-eino` at M2. `runtime.conformance-adkgo` is deferred to M3; no adk-go adapter exists on disk.

`telemetry.same-tree-on-all-backends` is deferred to M3 for the adk-go leg. `telemetry.export-lifecycle` stays M0: its subtest is in `adapter/otel/export_lifecycle_test.go`, and the audit miss was a transient leg setup failure. `identity.http-authenticator-required` keeps M4, matching the `httpapi/` M4 line in `docs/design/architecture.md`.

The engines rows `frozen-flag-on-replay`, `live-deny-beats-approval` and `stale-control-state` keep M0.5. None of the three M0.5 example processes references the `flags` capability surface: `types.Flags`, `types.FlagContext`, `types.FlagsFrom`, `std/flags` or `adapter/openfeature`. `temporal-travel` has a local `LiveFlags` type, and `camunda-invoice` has a local `controlPlane` stand-in.

## Consequences

- Coverage reports distinguish milestone scope from environment prerequisites; exclusion by rank does not prove a prerequisite-gated subtest ran.
- Existing IDs and provenance remain stable. The registry field, not the ID spelling, resolves capability ownership.
- The exception list changes with the scenario blocks it tracks. The two weaker bindings remain visible without claiming stronger assertions.

## References

- `docs/overview.md` section 12 and `docs/design/architecture.md` — milestone scope.
- `openspec/scenarios.json` and `tools/spec_coverage.py` — scenario records and ranks.
- `openspec/changes/scenario-record-reconciliation/design.md` — reconciliation decisions and evidence.
