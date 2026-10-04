# ADR-0048: Journal, fingerprinting and cancel shield apply only to `Idempotent`/`SideEffect` tools; `SessionLog` receives one write per turn; heartbeats piggyback on turn writes

Status: accepted · Origin: gohan-spec v0.13 decision D48

## Decision

Journal, fingerprinting and cancel shield apply only to `Idempotent`/`SideEffect` tools; `SessionLog` receives one write per turn; heartbeats piggyback on turn writes.

## Context and evidence

p95/p99 latency accumulates per layer; DB round trips must not gate read-only tools.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
