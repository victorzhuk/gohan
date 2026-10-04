# ADR-0068: Record/replay keyed by hash of the assembled request + profile `Version`; modes `Strict`, `ByTurn`, `Rerecord`; streams recorded as timed chunk sequences; evals use tolerance bands and `pass@k`/`pass^k`; LLM deciders default to temperature 0, enum output, pinned version

Status: accepted · Origin: gohan-spec v0.13 decision D68

## Decision

Record/replay keyed by hash of the assembled request + profile `Version`; modes `Strict`, `ByTurn`, `Rerecord`; streams recorded as timed chunk sequences; evals use tolerance bands and `pass@k`/`pass^k`; LLM deciders default to temperature 0, enum output, pinned version.

## Context and evidence

Exact-string matching and live models are the two causes of flaky agent tests.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
