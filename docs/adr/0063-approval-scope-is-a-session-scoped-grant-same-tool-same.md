# ADR-0063: Approval scope is a **session-scoped grant**: same tool, same fingerprint (or declared `FingerprintFields`), same principal, bounded by TTL (default 2 h) and the session; any fingerprint change invalidates it; grants are audit records; tenant-wide standing approvals are rules/scopes in reviewed config, never a runtime store

Status: accepted · Origin: gohan-spec v0.13 decision D63

## Decision

Approval scope is a **session-scoped grant**: same tool, same fingerprint (or declared `FingerprintFields`), same principal, bounded by TTL (default 2 h) and the session; any fingerprint change invalidates it; grants are audit records; tenant-wide standing approvals are rules/scopes in reviewed config, never a runtime store.

## Context and evidence

Approval fatigue: identical repeats drive rubber-stamping; approval scope is a security boundary.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
