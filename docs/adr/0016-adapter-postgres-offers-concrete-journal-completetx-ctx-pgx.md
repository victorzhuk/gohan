# ADR-0016: `adapter/postgres` offers concrete `Journal.CompleteTx(ctx, pgx.Tx, …)`

Status: accepted · Origin: gohan-spec v0.13 decision D16

## Decision

`adapter/postgres` offers concrete `Journal.CompleteTx(ctx, pgx.Tx, …)`.

## Context and evidence

Same-transaction journaling for own-DB side effects; core stays driver-free.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
