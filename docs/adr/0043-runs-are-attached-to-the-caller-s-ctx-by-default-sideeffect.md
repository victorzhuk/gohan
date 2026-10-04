# ADR-0043: Runs are attached to the caller's ctx by default; `SideEffect` tool execution is shielded from cancellation and persisted before cancellation is honoured; every event carries a monotonic `Seq`; `Detached` flows use an `EventLog` port with `Attach(runID, afterSeq)`

Status: accepted · Origin: gohan-spec v0.13 decision D43

## Decision

Runs are attached to the caller's ctx by default; `SideEffect` tool execution is shielded from cancellation and persisted before cancellation is honoured; every event carries a monotonic `Seq`; `Detached` flows use an `EventLog` port with `Attach(runID, afterSeq)`.

## Context and evidence

Client disconnects must not manufacture unknown outcomes; reconnect must be additive, not a redesign.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
