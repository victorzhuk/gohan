# 0178. Repair strict validation in the main specs

Status: accepted

## Context

Strict validation reports requirement bodies without `MUST` or `SHALL`, an empty `tool/exec` requirement, and requirements without scenarios. ADR-0161 requires each requirement body to state the normative contract bound by its scenarios.

## Decision

Repair `openspec/specs/*/spec.md` directly, following the consistency passes in ADR-0099, ADR-0100, ADR-0138 and ADR-0141. The change carries no spec deltas. Archive it with `--skip-specs`.

Insert `MUST` or `SHALL` into flagged requirement bodies without changing their semantics. Do not change scenarios, IDs, identifiers or contracts beyond the inserted verb.

Promote the existing `#### tool/exec` contract into its empty requirement. The three exec scenarios (`tools.timeout-kills-group`, `tools.output-cap`, `tools.env-allowlist`) are already registered and already have subtests; they are filed under `Verification` and move into the `tool/exec` requirement. No scenario ID, registry row or test changes.

Strict mode also fails a spec whose requirement body exceeds 500 characters. The measured baseline has 30 over-long bodies across 15 capabilities, recorded in the change's worklist. The original count of 37 included all warnings, not only long bodies. Split long bodies into separate requirements that each state one behavior. Preserve every existing requirement heading and keep each scenario with the behavior it binds.

Each resulting requirement must own an existing scenario. Split only along scenario boundaries. Move scenario-less prose verbatim into the capability's `Contract` section, whose prose remains normative. Do not invent scenarios to satisfy validation.

## Considered options

Spec deltas applied at archive time defer the repair and are rejected for this direct baseline pass.

## Consequences

The repair introduces no new spec content, invented scenario semantics, code changes, test changes or scenario-ID renames. Strict validation must pass all 36 specs. `task spec:types` and `task test` must pass.
