# ADR-0005: gohan owns neutral `Message` / `Block` / `Event` types

Status: accepted · Origin: gohan-spec v0.13 decision D5

## Decision

gohan owns neutral `Message` / `Block` / `Event` types.

## Context and evidence

Required by D4; conversion is the main bug surface → round-trip tests.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
