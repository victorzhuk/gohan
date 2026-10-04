# ADR-0052: `ModelProfile.Version` pins the exact model; reported version drift is measured and can fail closed. Core types and ports reach v1 at M2 and change only additively

Status: accepted · Origin: gohan-spec v0.13 decision D52

## Decision

`ModelProfile.Version` pins the exact model; reported version drift is measured and can fail closed. Core types and ports reach v1 at M2 and change only additively.

## Context and evidence

Silent degradation after provider-side upgrades; "a building block should be unlikely to change".

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
