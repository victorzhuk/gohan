# Proposal: m2-api-freeze

## Why

The M2 exit freezes every port that exists at the end of M2 at v1 and tags the modules `v1.0.0` (`docs/overview.md:108`, `:110`; `openspec/changes/m2/design.md:9`; `docs/design/compatibility.md:7-9`), and `m2-api-freeze` owns the port-method-set compatibility check and that tag (`openspec/changes/m2/design.md:149-150`; `openspec/changes/m2/tasks.md:45`). The shapes to freeze are the ones the three first-wave designs settled and the milestone definition of done lists (`openspec/changes/m2/tasks.md:53`).

The gate that is supposed to witness the freeze does not run. `task api:check` iterates `.` and `adapter/*` (`Taskfile.yml:53-69`) and skips any module with no tag of its own version line; only `v0.1.0` (root) and `adapter/otel/v0.1.0` exist, so `adapter/anthropic`, `adapter/docker`, `adapter/eino`, `adapter/openai` and `adapter/postgres` print `no tag yet, skipped (no baseline to diff against)`. Five of the seven modules the loop visits have no baseline, and `tools/apicheck` fails from v1 only when it has a tag to diff against (`tools/apicheck/main.go:84-114`), so a frozen port could grow a method with no gate reporting it.

## What Changes

- Record the frozen port shapes in `docs/design/compatibility.md`: `runtime.Runtime` unchanged, `runtime.AgentRun` grown only when two shipped backends need it, `State.Backend` the only backend state carrier, `StepGranularity` two values, the sandbox port and `types.SecretResolver` as declared (`openspec/changes/m2/tasks.md:53`), each with the file:line that ships it.
- Record the per-module `v1.0.0` tag plan in the same section — root first, then each adapter once its conformance suite passes against core v1 (`docs/design/compatibility.md:7-9`) — as a table over every module `task api:check` visits, and note that `examples/` is outside the loop.
- Make `tools/apicheck` stop at a nested module boundary, so a root-module run freezes the root module's packages instead of the `examples/` and `adapter/*` trees it parses today (`docs/design/api-review-m0-5.md`, *Gate evidence*, "Surface covered").
- Cut `v1.0.0` for the root module and `<dir>/v1.0.0` for every adapter module, after the M2 adapters land and the M2 gate reports `missing_in_scope=0`; record the cut in `CHANGELOG.md` and the `docs/overview.md` M2 exit line.
- Verify `task api:check` is meaningful again: every module in the loop diffs HEAD against its own `v1.0.0` tag, and an incompatible change at v1 fails the task.
- **No spec delta.** The change cuts tags, records port shapes and runs gates; no requirement text and no scenario id changes, so it sets `skip_specs: true`, the pattern `m2-sandbox-adapter-conformance`, `m2-docker-sandbox` and `m2` use.

## Capabilities

### New Capabilities

- (none)

### Modified Capabilities

- (none)

The freeze adds no behaviour: every port it names already ships, its shape is stated by the capability spec that introduced it, and the change records and tags them rather than restating them. No requirement text, no scenario id and no `openspec/specs/*/spec.md` file changes, so the change sets `skip_specs: true`.

## Impact

Affected files: `docs/design/compatibility.md` (the freeze record), `tools/apicheck/main.go` and `tools/apicheck/main_test.go` (the nested-module boundary), `CHANGELOG.md` (the release entry) and `docs/overview.md` (the M2 exit line). Repository state: one tag `v1.0.0` on the root module and one tag per adapter module (`adapter/<name>/v1.0.0`). No `openspec/specs/` file, no `core` port method set and no scenario changes.

Dependencies: the M2 adapters must be landed and green before the tags are cut — `m2-docker-sandbox`, `m2-e2b-sandbox` and the Docker/E2B legs of `m2-sandbox-adapter-conformance` — and `m2-s4-acceptance` must be green, per `openspec/changes/m2/tasks.md:45`. The change itself ships no adapter code and no tag of its own until those land.

Out of scope, deliberately: implementing the `spec:types` port-method-set generator clause (`docs/design/compatibility.md:38` records it as M0.5-flagged, M4-facing work), a tag for the `examples` module (not a published API line), and any change to the port method sets the freeze names.
