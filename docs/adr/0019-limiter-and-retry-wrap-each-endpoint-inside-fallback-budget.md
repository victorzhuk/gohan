# ADR-0019: Limiter and retry wrap each endpoint inside fallback; budget wraps outside fallback and hooks

Status: accepted · Origin: gohan-spec v0.13 decision D19

## Decision

Limiter and retry wrap each endpoint inside fallback; budget wraps outside fallback and hooks.

## Context and evidence

Per-endpoint limits; fallbacks charged; cache hits free. (Corrects an ordering stated during review.)

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
