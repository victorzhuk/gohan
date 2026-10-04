# ADR-0017: One middleware primitive per component; hooks and gate are sugar; a canonical order with user slots, shipped by `std` and validated by core (superseded in form by D45–D46, unchanged in substance)

Status: accepted · Origin: gohan-spec v0.13 decision D17

## Decision

One middleware primitive per component; hooks and gate are sugar; a canonical order with user slots, shipped by `std` and validated by core (superseded in form by D45–D46, unchanged in substance).

## Context and evidence

Ordering is semantics; one fixed, testable chain.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
