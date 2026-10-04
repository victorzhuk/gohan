# ADR-0025: `gohan.Build` and flow constructors validate every combination at startup and log the resolved matrix

Status: accepted · Origin: gohan-spec v0.13 decision D25

## Decision

`gohan.Build` and flow constructors validate every combination at startup and log the resolved matrix.

## Context and evidence

Silent disablement of caching/constrained decoding becomes a deploy error.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
