# ADR-0044: Approval enters only through `Resume` with a transport-set approver principal; no tool, model or sub-flow can approve

Status: accepted · Origin: gohan-spec v0.13 decision D44

## Decision

Approval enters only through `Resume` with a transport-set approver principal; no tool, model or sub-flow can approve.

## Context and evidence

Rogue "approval agent" spoofing (ASI10).

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
