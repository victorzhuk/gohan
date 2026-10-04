# ADR-0029: Credentials are never persisted; checkpoints store principal identity without tokens; a `CredentialSource` resolves credentials at execution time

Status: accepted · Origin: gohan-spec v0.13 decision D29

## Decision

Credentials are never persisted; checkpoints store principal identity without tokens; a `CredentialSource` resolves credentials at execution time.

## Context and evidence

Tokens expire during long suspensions; secrets stay out of stores.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
