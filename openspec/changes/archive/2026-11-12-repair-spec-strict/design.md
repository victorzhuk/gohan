# Design: Repair strict spec validation

## Context

Strict validation identifies requirement bodies without `MUST` or `SHALL`, an empty `tool/exec` requirement, and requirements without scenarios. The existing `tool/exec` contract is in the `#### tool/exec` block of the tools spec.

## Goals / Non-Goals

Make strict validation pass without changing existing contract semantics. Do not add new spec content, invent scenario semantics, change code or tests, or rename scenario IDs.

## Decisions

Edit `openspec/specs/*/spec.md` directly, following the consistency passes in ADR-0099, ADR-0100, ADR-0138 and ADR-0141. Do not create spec deltas in this change. Archive it with `--skip-specs` because the change edits the main specs directly and carries no deltas.

ADR-0161 requires each requirement body to state the normative contract bound by its scenarios. Insert `MUST` or `SHALL` in flagged bodies without changing their meaning. This insertion changes no scenario, ID, identifier or contract beyond the verb itself.

Duplicate requirement headings are consolidated: `performance` carried three identically named `Performance budgets` blocks, each empty-bodied and each holding one distinct scenario, so the name resolved to nothing unambiguous. Requirement names must be unique, so the three become one requirement carrying all three scenarios unchanged.

Promote the existing `tool/exec` contract into a normative requirement body. The existing `#### tool/exec` Contract block remains authoritative for the full option and execution contract, including host execution without isolation and the `AllowHostExec()` build warning. The three exec scenarios move unchanged from `Verification` into `tool/exec`. No scenario ID, registry row or test changes.

Strict validation reports every requirement body above 500 characters as a warning, and strict mode fails the spec on it. Each such body is split into separate requirements that each state one behavior. This is the validator's primary remedy, it keeps requirement bodies authoritative under ADR-0161, and it preserves every existing heading, so citations in other specs, `docs/design/` and the authority contract test keep resolving. Scenarios stay with the behavior they bind, and no ID, registry row or test changes.

A requirement must own at least one scenario, and a split cannot invent one. A behavior carried by a long body that no existing scenario binds moves verbatim into the file's `## Contract` section under its own `###` heading, and no requirement heading is created for it. `streams` `Run notices` established this pattern; the repair applies it wherever a split would otherwise leave a scenario-less requirement.

The `messages` `Error catalog` requirement owns the closed catalogue mapping every code to its kind, HTTP status and source identifier. The validator counts table rows as requirement text but skips fenced blocks. `tools/gen_types_index.py` reads catalogue rows between `### Requirement: Error catalog` and `### Requirement: Blobs`. The table therefore stays in place inside one fenced block. Its rows remain unchanged, and the generator keeps its existing source boundaries.

`streams` `Run notices` takes the same shape for the same reason: its body carried about two thousand characters and owned only three scenarios, so splitting it could never reach 500 characters. It became two requirements with the retry and signing scenarios, and the scenario-less delivery and refused-webhook rules moved verbatim into the spec's `## Contract`, where the file's own header declares prose normative.

## Risks / Trade-offs

A verb can alter an obligation if inserted without regard to the original wording. Review each insertion against the existing contract. A scenario must describe only behavior covered by its existing test.

## Migration Plan

Inventory the errors, repair the main specs, preserve the registry, and run the specified gates. Archive with `--skip-specs` because this change repairs the main specs directly.
