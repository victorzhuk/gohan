# ADR-0034: Fourth store port `Runs` with leases; a stateless harness recovers crashed runs via `Replay` from `SessionLog` + `Journal`; concurrent `Invoke` on a leased session is rejected

Status: accepted · Origin: gohan-spec v0.13 decision D34

## Decision

Fourth store port `Runs` with leases; a stateless harness recovers crashed runs via `Replay` from `SessionLog` + `Journal`; concurrent `Invoke` on a leased session is rejected.

## Context and evidence

Pod evictions and deploys are the first failure under real load; a run must never be silently lost or double-run.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
