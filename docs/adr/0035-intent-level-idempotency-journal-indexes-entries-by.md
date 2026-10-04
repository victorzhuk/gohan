# ADR-0035: Intent-level idempotency: `Journal` indexes entries by `Fingerprint(tool, canonical args)`; a `SideEffect` timeout yields `Outcome: Unknown` (never a retryable error); a re-call with the same fingerprint in the same run reuses the pinned key

Status: accepted · Origin: gohan-spec v0.13 decision D35

## Decision

Intent-level idempotency: `Journal` indexes entries by `Fingerprint(tool, canonical args)`; a `SideEffect` timeout yields `Outcome: Unknown` (never a retryable error); a re-call with the same fingerprint in the same run reuses the pinned key.

## Context and evidence

Late commits and model re-calls with fresh call IDs are the dominant duplicate-write path.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
