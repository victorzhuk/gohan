# ADR-0098: Core budget rule, `Verify`, milestone re-cut, capability freeze

Status: accepted · Origin: grill round 24 (2026-09-29)

## Decision

Core contains ports, types, events, errors and the driver; defaults, matchers, heuristics, presets and policy values are `std`; protocol- and vendor-specific code is `adapter`. Applied: `SkillSource` → `std/skills`; `TaintPolicy` defaults and matcher → `std/taint` behind a core `TaintHook`; JSON-Patch diffing → `std/state`. `ToolSpec.Verify` is added: an optional read-only post-condition the harness runs for `Unknown` outcomes after side effects, on resume/recover and before `Done`, reconciling the journal and reserving `UncertainOutcomeError` for what cannot be verified. Milestones re-cut in the proof → identity → verification → scaffold order: M1 gains `taint` and `Verify`; M2 `sandbox`, `context`; M3 `subflows`, `interop`, `release`; M4 `skills`, `agui`, evals. New capability specs are frozen until M0 ships; candidates go to `docs/backlog.md`.

## Context and evidence

Eight capabilities were added in one day; the field's warning is that harness value comes from governance before capability and that the model "can report an outcome that never happened" — proof must come from authoritative state, which the spec had no primitive for. Placement drift into core is the failure mode of every framework the comparison pieces criticise.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

`docs/design/architecture.md` §4.2a; `tools` gains `Verify` + 4 scenarios; `limits` cross-references; `skills`, `taint`, `agui` placement notes; `docs/overview.md` milestones; `docs/backlog.md` created.
