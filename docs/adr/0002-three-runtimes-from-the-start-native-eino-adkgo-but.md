# ADR-0002: Three runtimes from the start: `native`, `eino`, `adkgo` — but adapters are sacrificial: no feature depends on a capability only one adapter has, S1–S3 pass without framework adapters, each adapter is its own module and can be archived without a core change

Status: accepted · Origin: gohan-spec v0.13 decision D2

## Decision

Three runtimes from the start: `native`, `eino`, `adkgo` — but adapters are sacrificial: no feature depends on a capability only one adapter has, S1–S3 pass without framework adapters, each adapter is its own module and can be archived without a core change.

## Context and evidence

A port with one implementation takes its shape; upstream frameworks have shipped breaking releases and shown uneven maintenance.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
