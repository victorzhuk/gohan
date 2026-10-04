# ADR-0081: Definitions are deployment-pinned: validated at `Build`, hashed into the manifest, version pinned per run, old versions retained while pending runs need them. Runtime or tenant-authored publication is out of v1

Status: accepted · Origin: gohan-spec v0.13 decision D81

## Decision

Definitions are deployment-pinned: validated at `Build`, hashed into the manifest, version pinned per run, old versions retained while pending runs need them. Runtime or tenant-authored publication is out of v1.

## Context and evidence

Same lifecycle as tools and prompts.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
