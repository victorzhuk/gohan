# ADR-0062: `ToolFilter` is deterministic for `(RunInfo, session state)`; the filtered set is recorded in audit and checkpoint and asserted on `Replay`

Status: accepted · Origin: gohan-spec v0.13 decision D62

## Decision

`ToolFilter` is deterministic for `(RunInfo, session state)`; the filtered set is recorded in audit and checkpoint and asserted on `Replay`.

## Context and evidence

Dynamic tool lists must survive resume unchanged (eino persists them in state for the same reason).

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
