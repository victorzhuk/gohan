# ADR-0070: `std/flow` ships recipes whose shape is defined by types and composition (`Extract`, `Classify`, `Route`, `MapReduce`, `Pipeline`, `RAG`, `Judge`/`Refine`); nothing that encodes a model-capability assumption or a prompt (no supervisor/transfer, no plan-and-execute, no "ReAct"). Each recipe: one constructor with options, returns `Flow[In, Out]`, ≤ ~100 lines, no prompt text outside `PromptSet`, `Explain`-able, with an acceptance scenario and cassette in `examples/`

Status: accepted · Origin: gohan-spec v0.13 decision D70

## Decision

`std/flow` ships recipes whose shape is defined by types and composition (`Extract`, `Classify`, `Route`, `MapReduce`, `Pipeline`, `RAG`, `Judge`/`Refine`); nothing that encodes a model-capability assumption or a prompt (no supervisor/transfer, no plan-and-execute, no "ReAct"). Each recipe: one constructor with options, returns `Flow[In, Out]`, ≤ ~100 lines, no prompt text outside `PromptSet`, `Explain`-able, with an acceptance scenario and cassette in `examples/`.

## Context and evidence

Teams rebuild extract/classify/map-reduce in the wrong order; eino removed transfer agents; Anthropic removed sprint decomposition as models improved.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
