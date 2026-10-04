# ADR-0072: Structured output hardening: app-side validation after `Constrained`; strict-compatible generated schemas (`additionalProperties: false`, explicit `required`, no recursion, depth ≤ 4, enums ≤ 50); refusal-as-JSON → `ContentPolicy`; truncated `ToolUse` never executed; `ReasonFirst` option, default on for `Agentic`; schema hash in audit

Status: accepted · Origin: gohan-spec v0.13 decision D72

## Decision

Structured output hardening: app-side validation after `Constrained`; strict-compatible generated schemas (`additionalProperties: false`, explicit `required`, no recursion, depth ≤ 4, enums ≤ 50); refusal-as-JSON → `ContentPolicy`; truncated `ToolUse` never executed; `ReasonFirst` option, default on for `Agentic`; schema hash in audit.

## Context and evidence

Grammar engines treat bounds as advisory; malformed tool args are a top-3 pipeline failure; constrained decoding costs up to 10 pp on multi-step reasoning.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
