# ADR-0078: Flags: a core `Flags` port with two classes — frozen rollout flags evaluated once per run, snapshotted into `RunInfo`, checkpoint and audit, asserted on `Replay`; live emergency flags re-evaluated before every new effect and able only to deny. Approval cannot override an emergency deny; flags never grant permissions. `std/flags` (static/env, snapshot), `adapter/openfeature`

Status: accepted · Origin: gohan-spec v0.13 decision D78

## Decision

Flags: a core `Flags` port with two classes — frozen rollout flags evaluated once per run, snapshotted into `RunInfo`, checkpoint and audit, asserted on `Replay`; live emergency flags re-evaluated before every new effect and able only to deny. Approval cannot override an emergency deny; flags never grant permissions. `std/flags` (static/env, snapshot), `adapter/openfeature`.

## Context and evidence

Rollout consistency inside a run and on replay; emergency control must reach suspended runs.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
