# ADR-0027: Interrupt generalized to suspension with reasons: `HumanApproval`, `AwaitingExternal`, `AwaitingBatch`, `AwaitingTool`, `Scheduled`; `Waker` port for time

Status: accepted · Origin: gohan-spec v0.13 decision D27

## Decision

Interrupt generalized to suspension with reasons: `HumanApproval`, `AwaitingExternal`, `AwaitingBatch`, `AwaitingTool`, `Scheduled`; `Waker` port for time.

## Context and evidence

One mechanism for HITL, async jobs, scheduling.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
