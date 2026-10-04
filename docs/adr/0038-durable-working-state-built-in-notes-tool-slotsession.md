# ADR-0038: Durable working state: built-in `Notes` tool + `SlotSession` provider; large tool outputs stored by reference (`ToolResult.Ref`) with per-tool truncation

Status: accepted · Origin: gohan-spec v0.13 decision D38

## Decision

Durable working state: built-in `Notes` tool + `SlotSession` provider; large tool outputs stored by reference (`ToolResult.Ref`) with per-tool truncation.

## Context and evidence

Compaction and resets must not erase progress, constraints or attempts; large outputs must not bloat context.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
