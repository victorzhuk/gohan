# ADR-0028: Ambient verified `Principal`, set by transport only; `ToolSpec.RequiredScopes` checked before any decider; identity forwarded downstream; originator and approver kept separate across resume

Status: accepted · Amended by ADR-0109 (closed set, `(T, bool)` accessors, propagation table). · Origin: gohan-spec v0.13 decision D28

## Decision

Ambient verified `Principal`, set by transport only; `ToolSpec.RequiredScopes` checked before any decider; identity forwarded downstream; originator and approver kept separate across resume.

## Context and evidence

Closes the confused-deputy hole.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
