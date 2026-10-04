# ADR-0013: Exactly-once tool effects = result journal + idempotency key + effect metadata

Status: accepted · Origin: gohan-spec v0.13 decision D13

## Decision

Exactly-once tool effects = result journal + idempotency key + effect metadata.

## Context and evidence

Journal covers replays; key covers the crash window.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
