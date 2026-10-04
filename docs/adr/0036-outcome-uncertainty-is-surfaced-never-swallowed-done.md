# ADR-0036: Outcome uncertainty is surfaced, never swallowed: `Done.Uncertain`, `*UncertainOutcomeError` from `Flow.Invoke`, and a model-visible "outcome unknown" result with optional `ReadBack`

Status: accepted · Origin: gohan-spec v0.13 decision D36

## Decision

Outcome uncertainty is surfaced, never swallowed: `Done.Uncertain`, `*UncertainOutcomeError` from `Flow.Invoke`, and a model-visible "outcome unknown" result with optional `ReadBack`.

## Context and evidence

Agents report success over unknown writes; the business layer must see it.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
