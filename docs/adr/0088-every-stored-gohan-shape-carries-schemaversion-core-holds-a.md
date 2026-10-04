# ADR-0088: Every stored gohan shape carries `SchemaVersion`; core holds a registry of pure upcasters N→N+1 applied on read; stores never rewrite records in place; `storetest` replays recorded fixtures from every released schema version

Status: accepted · Origin: gohan-spec v0.13 decision D88

## Decision

Every stored gohan shape carries `SchemaVersion`; core holds a registry of pure upcasters N→N+1 applied on read; stores never rewrite records in place; `storetest` replays recorded fixtures from every released schema version.

## Context and evidence

The message model already changed twice; replay across versions must be a tested path, not an accident.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
