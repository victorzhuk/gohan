# ADR-0011: Run context (`RunInfo`, principal, event sink, turn counter) travels in `ctx`

Status: accepted · Origin: gohan-spec v0.13 decision D11

## Decision

Run context (`RunInfo`, principal, event sink, turn counter) travels in `ctx`.

## Context and evidence

Decorators see the run without loop access.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
