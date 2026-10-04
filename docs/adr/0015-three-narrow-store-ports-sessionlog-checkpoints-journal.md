# ADR-0015: Three narrow store ports: `SessionLog`, `Checkpoints`, `Journal`

Status: accepted · Superseded by ADR-0034 (Runs), ADR-0053 (AuditLog) and the EventLog, OutputStore, NotesStore and Feedback ports: nine mandatory store ports, with `RetentionSource` and `SessionIndex` as optional interfaces. · Origin: gohan-spec v0.13 decision D15

## Decision

Three narrow store ports: `SessionLog`, `Checkpoints`, `Journal`.

## Context and evidence

Each concurrency contract testable; mix backends.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
