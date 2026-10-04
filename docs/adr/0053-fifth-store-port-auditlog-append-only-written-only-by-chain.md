# ADR-0053: Fifth store port `AuditLog`: append-only, written only by chain steps (the data path), records decisions, identities, versions and checksums — never content. `Journal` results get a TTL after run finish. `Reconstruct(sessionID)` rebuilds a decision trail

Status: accepted · Origin: gohan-spec v0.13 decision D53

## Decision

Fifth store port `AuditLog`: append-only, written only by chain steps (the data path), records decisions, identities, versions and checksums — never content. `Journal` results get a TTL after run finish. `Reconstruct(sessionID)` rebuilds a decision trail.

## Context and evidence

Audit requirements (EU AI Act Art. 12 and enterprise security reviews): logs must not be agent-written, must be append-only and retained; full tool results in a durable journal are a secondary PII store.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
