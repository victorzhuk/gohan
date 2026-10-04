# ADR-0065: Deferred tools: `ToolSpec.Deferred` tools are governed but not assembled until discovered via the built-in `search_tools`; activation is run state recorded in checkpoint and audit and asserted on `Replay`; `Build` warns above a definitions-to-context share (default 5 %)

Status: accepted · Origin: gohan-spec v0.13 decision D65

## Decision

Deferred tools: `ToolSpec.Deferred` tools are governed but not assembled until discovered via the built-in `search_tools`; activation is run state recorded in checkpoint and audit and asserted on `Replay`; `Build` warns above a definitions-to-context share (default 5 %).

## Context and evidence

200–400 tokens per definition; selection accuracy 95 % with 4 tools vs 71 % with 46; MCP progressive discovery is the host's job.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
