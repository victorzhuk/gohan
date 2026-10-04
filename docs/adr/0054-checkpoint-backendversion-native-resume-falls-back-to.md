# ADR-0054: `Checkpoint.BackendVersion`; `Native` resume falls back to `Replay` on mismatch or decode failure for agent flows; graph flows surface `ErrCheckpointIncompatible`

Status: accepted · Origin: gohan-spec v0.13 decision D54

## Decision

`Checkpoint.BackendVersion`; `Native` resume falls back to `Replay` on mismatch or decode failure for agent flows; graph flows surface `ErrCheckpointIncompatible`.

## Context and evidence

Upstream checkpoint encodings change across minor versions (eino v0.9 `ToolInfo`).

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
