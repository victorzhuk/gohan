# ADR-0030: Resume strategy per backend: `Replay` (history + journal, universal for agent loops) or `Native` (backend checkpoint, e.g. eino)

Status: accepted · Origin: gohan-spec v0.13 decision D30

## Decision

Resume strategy per backend: `Replay` (history + journal, universal for agent loops) or `Native` (backend checkpoint, e.g. eino).

## Context and evidence

Works on every agent runtime; graphs use native interrupts.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
