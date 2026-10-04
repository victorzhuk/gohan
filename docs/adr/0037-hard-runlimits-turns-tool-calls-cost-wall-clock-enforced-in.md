# ADR-0037: Hard `RunLimits` (turns, tool calls, cost, wall clock) enforced in code with typed abort and a soft-threshold event

Status: accepted · Origin: gohan-spec v0.13 decision D37

## Decision

Hard `RunLimits` (turns, tool calls, cost, wall clock) enforced in code with typed abort and a soft-threshold event.

## Context and evidence

Soft alerts do not stop runaway loops.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
