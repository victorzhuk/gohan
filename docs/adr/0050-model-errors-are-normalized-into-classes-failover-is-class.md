# ADR-0050: Model errors are normalized into classes; failover is class-based with per-endpoint circuit breakers; fallback targets are validated for capability compatibility at build; context is re-fit per target

Status: accepted · Origin: gohan-spec v0.13 decision D50

## Decision

Model errors are normalized into classes; failover is class-based with per-endpoint circuit breakers; fallback targets are validated for capability compatibility at build; context is re-fit per target.

## Context and evidence

429 needs failover not retry; "model non-equivalence" breaks blind fallback.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
