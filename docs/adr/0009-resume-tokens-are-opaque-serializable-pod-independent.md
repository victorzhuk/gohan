# ADR-0009: Resume tokens are opaque, serializable, pod-independent, single-use (`ErrTokenConsumed`)

Status: accepted · Origin: gohan-spec v0.13 decision D9

## Decision

Resume tokens are opaque, serializable, pod-independent, single-use (`ErrTokenConsumed`).

## Context and evidence

Multi-pod resume; idempotent approvals under at-least-once delivery.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
