# ADR-0026: Prefix-stable request assembly with `ContextProviders` in fixed slots and a `ContextPolicy` strategy

Status: accepted · Amended by ADR-0090: `Summarize`/`Compact` are the `context` capability (persisted compaction, projections). · Origin: gohan-spec v0.13 decision D26

## Decision

Prefix-stable request assembly with `ContextProviders` in fixed slots and a `ContextPolicy` strategy.

## Context and evidence

Prompt-cache hit rate; memory/RAG injection without hacking instructions; compaction later without breaking changes.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
