# ADR-0033: Multi-module repo; core depends on stdlib + OTel API only

Status: accepted · Origin: gohan-spec v0.13 decision D33

## Decision

Multi-module repo; core depends on stdlib + OTel API only.

## Context and evidence

Adapter users don't pull other frameworks.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
