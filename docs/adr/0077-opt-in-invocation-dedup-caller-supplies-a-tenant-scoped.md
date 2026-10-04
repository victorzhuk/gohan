# ADR-0077: Opt-in invocation dedup: caller supplies a tenant-scoped `OperationID`; `Runs.Start` refuses a duplicate with `ErrOperationExists{RunID}` and the caller reads the stored result; retention is caller-defined

Status: accepted · Origin: gohan-spec v0.13 decision D77

## Decision

Opt-in invocation dedup: caller supplies a tenant-scoped `OperationID`; `Runs.Start` refuses a duplicate with `ErrOperationExists{RunID}` and the caller reads the stored result; retention is caller-defined.

## Context and evidence

Fingerprint keys are per intent and deliberately fresh after a success (D35); redelivery of a whole message needs a separate durable identity.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
