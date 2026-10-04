# ADR-0001: gohan owns structure; runtimes/backends own execution

Status: accepted · Origin: gohan-spec v0.13 decision D1

## Decision

gohan owns structure; runtimes/backends own execution.

## Context and evidence

Reuse mature loops and graphs; gohan is a framework, not a facade.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
