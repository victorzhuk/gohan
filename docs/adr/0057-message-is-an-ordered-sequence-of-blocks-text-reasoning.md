# ADR-0057: `Message` is an ordered sequence of `Block`s (`Text`, `Reasoning`, `Image`, `Audio`, `File`, `Document`, `ToolUse`, `ToolResult`, `CacheBreak`, `Raw`); no tool-call side field, no tool role; adapters declare a per-block fidelity matrix that `Explain` prints and tests assert

Status: accepted · Origin: gohan-spec v0.13 decision D57

## Decision

`Message` is an ordered sequence of `Block`s (`Text`, `Reasoning`, `Image`, `Audio`, `File`, `Document`, `ToolUse`, `ToolResult`, `CacheBreak`, `Raw`); no tool-call side field, no tool role; adapters declare a per-block fidelity matrix that `Explain` prints and tests assert.

## Context and evidence

Anthropic ordered thinking blocks, Gemini parts and eino `AgenticMessage` are block-ordered; a side field loses order and reasoning signatures.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
