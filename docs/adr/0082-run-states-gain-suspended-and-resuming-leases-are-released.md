# ADR-0082: Run states gain `Suspended` and `Resuming`; leases are released on suspension; `Checkpoints.Consume(ctx, t, in)` stores the resume input atomically; `Recover` re-drives `Resuming` runs from the stored input; only `Running`/`Resuming` are recoverable; retention while suspended is the checkpoint's `ExpiresAt`

Status: accepted · Origin: gohan-spec v0.13 decision D82

## Decision

Run states gain `Suspended` and `Resuming`; leases are released on suspension; `Checkpoints.Consume(ctx, t, in)` stores the resume input atomically; `Recover` re-drives `Resuming` runs from the stored input; only `Running`/`Resuming` are recoverable; retention while suspended is the checkpoint's `ExpiresAt`.

## Context and evidence

A crash between consuming the token and running the backend lost the resume; multi-day approvals had no lease semantics.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
