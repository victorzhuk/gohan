# ADR-0056: Layout: `core/` (package `gohan`), `std/`, `testkit/` in one module; every third-party integration under `adapter/<name>/` as its own module

Status: accepted · Origin: gohan-spec v0.13 decision D56

## Decision

Layout: `core/` (package `gohan`), `std/`, `testkit/` in one module; every third-party integration under `adapter/<name>/` as its own module.

## Context and evidence

Version core and std together; isolate every external dependency; explicit folders with idiomatic call sites.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
