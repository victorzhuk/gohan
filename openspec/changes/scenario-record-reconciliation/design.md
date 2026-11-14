# Design

## Context

The audit's M0.5 coverage run reported `registered=732 covered=560 missing=172 missing_in_scope=36 unregistered=0`. Per-ID attribution showed every one of the 36 in-scope IDs already has a literal subtest. The gate failure is therefore a records defect, not missing test work: 29 post-M0 rows carry no `deferred_to` and are ranked M0 by default, ten rows are invisible because their module leg skips (no Docker daemon) or sits behind `//go:build full`, ten were counted missing because their module leg failed, eleven spec blocks carry no `ID:` line, eight registry rows carry no spec block, one spec block carries no row, and three vocabulary patterns (the `errors.*` prefix inside capability `messages`, change-slug `origin` labels, and the exception set) are used without being sanctioned.

The coverage tool ranks a row without `deferred_to` as M0 (`tools/spec_coverage.py:70-75`, `RANKS`). That default is correct and stays: a row is M0 until it says otherwise. The defect is the missing annotation, not the ranking.

## Goals and non-goals

Goals:

- Every registry row is attributable: it passes a subtest, or it names the milestone whose module ships it.
- Every spec `#### Scenario:` carries an `ID:`, and every `ID:` has a row or membership in the documented exception set.
- The used vocabularies are sanctioned in writing, so the next audit does not re-litigate them.
- The M0.5 gate reaches `missing_in_scope=0` on a stable tree, with failed legs named.

Non-goals:

- No port, type, adapter or runtime behavior change.
- No new test work for the 36 in-scope IDs. The eleven ID-less blocks already have covering subtests; only their names bind to IDs.
- No requirement semantics change. Added blocks document shipped, already-asserted behavior.
- No rename of any existing ID, and no change to the `errors.*` identifiers.

## Decisions

### 1. A post-M0 row carries its owning milestone

`scenario_rank` returns M0 for a row without `deferred_to`. Every row whose covering module ships after M0 therefore gains `deferred_to` with the milestone that ships it, taken from the scope lines in `docs/overview.md` §12 and `docs/design/architecture.md`. Values are restricted to the `RANKS` keys (`M0`, `M0.5`, `M1`, `M2`, `M3`, `M4`); the tool already rejects anything else.

`M0` and `M0.5` are not written as `deferred_to` on a row that has no passing subtest, because both ranks sit at or before the default gate. A row that ships at M0.5 keeps the absence of the field.

### 2. Environment prerequisites do not move a milestone

Six `adapter/docker` subtests skip without a Docker daemon and four `adapter/postgres` subtests sit behind `//go:build full`, which the coverage legs do not set (`Taskfile.yml`). Both are environment facts about the leg, not deferrals: the rows keep their owning milestone (`M2` and `M1`) and the M0.5 gate excludes them by rank rather than by reaching zero coverage. The gate's zero is about scope, not about executing every prereq-gated test.

### 3. Eleven ID-less blocks get IDs; their tests get names

Each block already has a covering subtest. Binding is a rename, or a `t.Run` wrapper where the covering body serves two blocks. No assertion moves.

| Block | ID | Deferral |
|---|---|---|
| cache: identical prefix hits | `cache.prefix-hit` | M1 (`std/cache` ships M1) |
| cache: repopulating miss writes the prefix | `cache.prefix-miss-repopulates` | M1 |
| cache: scope is never crossed | `cache.scope-isolation` | M1 |
| cache: hit reports cached input | `cache.hit-usage` | M1 |
| cache: repopulating miss reports cache write | `cache.miss-write-usage` | M1 |
| cache: hit and miss counted | `cache.hit-miss-metrics` | M1 |
| model: shared suite coverage | `model.shared-suite-coverage` | none (M0: the shared suite is the M0 conformance surface) |
| model: cache control markers mapped in order | `model.anthropic-cache-markers-order` | M1 (adapter/anthropic) |
| model: stop reason mapped to finish reason | `model.anthropic-stop-reason-mapped` | M1 |
| model: response schema honoured through forced tool | `model.anthropic-forced-tool-schema` | M1 |
| model: reasoning signature round-trips | `model.anthropic-reasoning-signature` | M1 |

Two bindings are weaker than their block titles and are recorded rather than repaired: the Anthropic cache-marker subtest asserts the marker count, not the block order, and the shared-suite binding runs the suite rather than enumerating it. Strengthening either is test work this change does not authorize.

### 4. Eight repair IDs get spec blocks, not tests

`repair-runtime-safety-contracts` registered eight rows whose behavior it asserted but never wrote into the spec tree. The blocks derive from the bound subtests: three `adapter/postgres` subtests under `//go:build full`, five short-mode subtests in `core`. Requirement placement follows the behavior:

- `stores.reclaim-oldest-first-exclusive`, `stores.start-refused-reclaimed-lease`, `stores.resume-older-owner-blocks-start` → requirement *Every lease acquisition preserves session exclusivity*.
- `stores.resume-ready-reentrant-run-port`, `stores.preempted-rank-before-decode` → requirement *Recovery discovery limits eligible current checkpoints*.
- `recovery.consumed-token-fenced` → requirement *Automatic preemption recovery consumes its exact token*.
- `recovery.eventlog-bound-outlives-drive`, `recovery.redaction-tenant-continuity` → requirement *Recovery records the public event stream durably*.

No new requirement heading is introduced: each block states behavior the named requirement's prose already covers.

### 5. `errors.*` is a sanctioned alias inside capability `messages`

Four IDs use the `errors.` prefix while their `capability` is `messages` (`messages.order-preserved` style rows keep the capability prefix). The identifiers are subtest names, so renaming them would touch tests for no gate benefit. The alias is sanctioned by ADR-0189 with its resolution path: the registry row's `capability` field, never the ID prefix, decides the owning capability.

### 6. A change-slug `origin` is provenance, not a requirement label

`origin` accepts a requirement heading, `ADR-NNNN`, or the slug of the change that added the row. Acceptance never reads `origin` — `docs/overview.md` already states that acceptance is `deferred_to` plus `task spec:coverage`. The vocabulary is written down so a future audit counts rows by provenance instead of reporting 166 unexplained labels. A slug that matches no change directory is a historic label and stays as written.

### 7. The exception set is enumerated, not counted

`docs/overview.md` lists registered IDs without a spec block and says "Eight" while enumerating nine. The list becomes the exact registry-minus-spec diff with a count that matches the enumeration, and the change that adds or removes a member updates it in the same commit. Task 2.3 removes the eight repair rows from the diff, so they never enter the list.

### 8. The conformance requirement binds registered IDs

The runtime conformance requirement currently binds no registry row. Three IDs are registered from the shipped suite: `runtime.conformance-native` (M0, root module), `runtime.conformance-eino` (M2, `adapter/eino`), `runtime.conformance-adkgo` (deferred M3, no adapter on disk). Native and eino subtests wrap the existing `conformance.Runtime` calls; adk-go exists only as a row and a deferral because `adapter/adkgo` does not ship.

### 9. Deferral attribution where scope lines are thin

- `identity.http-authenticator-required` keeps `deferred_to: M4`; `docs/design/architecture.md:169` schedules `httpapi/` at M4. The M4 scope table line that would name it fully is handed to `records-map-integrity`.
- `telemetry.export-lifecycle` stays at M0: its subtest exists at `adapter/otel/export_lifecycle_test.go` and the audit miss was a transient leg setup failure, not a gap.
- `telemetry.same-tree-on-all-backends` names the adk-go leg, so it takes `deferred_to: M3`.

### 10. The three flag-dependent engines rows stay at M0.5

`frozen-flag-on-replay`, `live-deny-beats-approval` and `stale-control-state` live in the three M0.5 acceptance processes. None of the three imports the `flags` capability surface: no `types.Flags`, `types.FlagContext`, `types.FlagsFrom`, `std/flags` or flag provider is referenced; `temporal-travel` has a local `LiveFlags` type and `camunda-invoice` a local `controlPlane` stand-in for a provider. Their behavior is self-contained, so the rows keep M0.5 and are not retargeted.

## Risks

- `openspec/scenarios.json` is under concurrent edit by `governance-fail-closed`, which already appended rows in the working tree. One owner rewrites the file for this change, and the edit is additive key insertion preserving the existing key order (`capability`, `origin`, `title`, then `deferred_to`), so the other change's lines survive.
- Renaming subtests can break a `-run` selector elsewhere. Every rename is checked against the tree before it lands.
- The gate result depends on a warm module cache for the `examples` and `adapter/otel` legs; a cold-cache failure now names its leg instead of masking the remaining ones.
