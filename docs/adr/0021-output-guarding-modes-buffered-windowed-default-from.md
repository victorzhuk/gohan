# ADR-0021: Output guarding modes `Buffered` / `Windowed`; default from latency class

Status: accepted · Origin: gohan-spec v0.13 decision D21

## Decision

Output guarding modes `Buffered` / `Windowed`; default from latency class.

## Context and evidence

Unguarded deltas are a leak; the tradeoff is a business latency decision.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
