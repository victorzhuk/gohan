# ADR-0008: Flow suspension is a typed error `*SuspendError`; `Resume(ctx, token, input)`

Status: accepted · Origin: gohan-spec v0.13 decision D8

## Decision

Flow suspension is a typed error `*SuspendError`; `Resume(ctx, token, input)`.

## Context and evidence

Non-suspending call sites stay `out, err := f.Invoke(ctx, in)`.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
