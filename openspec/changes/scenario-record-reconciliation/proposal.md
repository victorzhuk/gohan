# Proposal

## Why

The audit's coverage run (gate M0.5, eight module legs) reported `registered=732 covered=560 missing=172 missing_in_scope=36 unregistered=0`. Per-ID attribution against the test sources then established that **every one of the 36 in-scope IDs already has a literal `t.Run("<id>", ...)` subtest in the tree** — none is missing test work. The gate failure decomposes into:

1. **Mis-ranked deferrals** (High, static). `tools/spec_coverage.py:70-75` ranks a row without `deferred_to` as M0. 29 post-M0 rows carry no deferral while their implementing modules ship later: 6 model provider rows (adapter/openai and adapter/anthropic, M1), 8 runtime eino/bridge rows (adapter/eino, M2), 7 sandbox rows (adapter/docker M2; the data-analyst example M2), 4 stores postgres rows (adapter/postgres M1; subtests behind `//go:build full`), `context.research-report-compaction` (M2 example), and 3 flag-dependent engines rows deferred M0.5 while all `flags.*` rows are M1 (dependency ordering needs verification at admission). The M0 exit contract requires every test-less scenario to carry a later-milestone deferral.
2. **Environment skips** (10 IDs, not defects). 6 sandbox subtests skip without a Docker daemon (`adapter/docker/factory_test.go:37`, `daemon_test.go:12`); 4 stores postgres subtests are excluded by the `full` build tag, which the coverage leg does not set (`Taskfile.yml:25`). Their deferrals (M2/M1) are correct regardless; the M0.5 gate then excludes them.
3. **Leg-level failures in the audit run** (10 IDs). The 9 engines subtests live in the examples module (kafka-refunds, temporal-travel, camunda-invoice — the M0.5 exit processes) and `telemetry.export-lifecycle` lives in adapter/otel; all exist with no skip conditions. The examples leg needs uncached third-party modules offline, and adapter/otel hit a transient setup failure during the audit (driver-demonstrated: `export.go` briefly carried an unresolvable import; the tree is under active edit). The wrapper's group semantics mask intermediate leg failures (see `coverage-leg-failure-visibility`), so the run reported a summary without naming the failed legs.
4. **Eleven ID-less scenario blocks** (Medium, static). Six in `cache` (identical prefix hits; repopulating miss writes the prefix; scope is never crossed; hit reports cached input; repopulating miss reports cache write; hit and miss counted) and five in `model` (shared suite coverage; cache control markers mapped in order; stop reason mapped to finish reason; response schema honoured through forced tool; reasoning signature round-trips). WHEN/THEN acceptance without `ID:` lines or registry rows — invisible to every gate.
5. **Eight registry IDs without spec blocks** (Medium, static). `stores.reclaim-oldest-first-exclusive`, `stores.start-refused-reclaimed-lease`, `stores.resume-older-owner-blocks-start`, `stores.resume-ready-reentrant-run-port`, `stores.preempted-rank-before-decode`, `recovery.consumed-token-fenced`, `recovery.eventlog-bound-outlives-drive`, `recovery.redaction-tenant-continuity` — origin `repair-runtime-safety-contracts`. All eight have subtests (five short-mode, three `full`-tag); none has spec text. The documented exception list (`docs/overview.md:103`) was never extended and says "Eight" while enumerating nine.
6. **One spec-only ID** (Medium, static). `context.research-report-claim-boundary` has a spec block but no registry row.
7. **Unsanctioned namespaces and origins** (Low, static). Four `errors.*` IDs in capability `messages`; 166 change-slug origins outside the documented vocabulary.
8. **Unattributable deferrals and unbound conformance** (Low, static). `identity.http-authenticator-required` is M4 with no scope line naming httpapi (`architecture.md:169` schedules it M4). `telemetry.same-tree-on-all-backends` has no deferral while naming adk-go (M3). The runtime conformance requirement binds zero registry IDs.

## What Changes

Registry metadata and scenario records only; no runtime behavior changes and no new test work for the 36 IDs. Set owning-milestone deferrals on the post-M0 rows; assign stable IDs and registry rows to the eleven blocks; add spec scenario blocks for the eight repair IDs (text derived from the repair change's artifacts and bound subtests); register the context sibling; document the `errors.*` alias and change-slug origins; attribute the httpapi and telemetry rows; bind the conformance requirement to registered scenario IDs. Re-run the gate on a stable tree with a warm module cache to establish the true residual.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `cache`, `model`, `stores`, `recovery`, `context`, `runtime`: scenario records (IDs, blocks, registrations). No requirement semantics change; the added blocks document behavior already shipped and already tested. This change edits main specs and the registry directly and archives with `--skip-specs`, following the `repair-spec-strict` precedent.

## Impact

`openspec/scenarios.json`, six spec files, and convention docs (alias/origin vocabulary land in `records-map-integrity`'s overview refresh; ADR-0189 records the decisions). After admission, the M0.5 gate's `missing_in_scope` is expected to reach 0 on a stable tree: every row either passes, skips under a named environment prerequisite with a post-M0.5 deferral, or sits behind the `full` tag with an M1 deferral. The engines M0.5 rows pass when the examples leg builds (module cache warm).

Dependencies: `coverage-leg-failure-visibility` should land first so the verification gate names failed legs instead of masking them. `records-map-integrity` admits after this change (exception list and counts). `repair-contract-drift` and `governance-fail-closed` add registrations additively.