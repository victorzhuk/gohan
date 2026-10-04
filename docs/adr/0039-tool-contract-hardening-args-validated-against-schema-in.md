# ADR-0039: Tool contract hardening: args validated against schema in the chain for every `Tool`; unknown tool names rejected as error results; `ToolResult.Error.Kind` classifies failures

Status: accepted · Origin: gohan-spec v0.13 decision D39

## Decision

Tool contract hardening: args validated against schema in the chain for every `Tool`; unknown tool names rejected as error results; `ToolResult.Error.Kind` classifies failures.

## Context and evidence

Silent tool failures and hallucinated tools/args are top-ranked production incidents.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
