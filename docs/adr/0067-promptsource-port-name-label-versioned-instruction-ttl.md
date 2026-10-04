# ADR-0067: `PromptSource` port (name + label → versioned instruction, TTL cache, embedded fallback); version stamped on spans and manifest. `adapter/langfuse` implements it plus an evals `Sink` and dataset provider

Status: accepted · Origin: gohan-spec v0.13 decision D67

## Decision

`PromptSource` port (name + label → versioned instruction, TTL cache, embedded fallback); version stamped on spans and manifest. `adapter/langfuse` implements it plus an evals `Sink` and dataset provider.

## Context and evidence

Prompt management and A/B are not tracing; they are a seam gohan already needed.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
