# ADR-0074: Interop is adapters: `adapter/mcp` client and server in v0.4 (server exposes one `Flow` as one tool; suspension surfaces as an error carrying the resume token); `adapter/a2a` server later, mapping `Flow` + `Detached` + `EventLog` to A2A task states. Single-shot recipes go out via MCP, agent flows via A2A; never both for one flow by default

Status: accepted · Amended by ADR-0092: the MCP binding targets the 2026-07-28 specification (`interop`). · Origin: gohan-spec v0.13 decision D74

## Decision

Interop is adapters: `adapter/mcp` client and server in v0.4 (server exposes one `Flow` as one tool; suspension surfaces as an error carrying the resume token); `adapter/a2a` server later, mapping `Flow` + `Detached` + `EventLog` to A2A task states. Single-shot recipes go out via MCP, agent flows via A2A; never both for one flow by default.

## Context and evidence

"Start with MCP, add A2A when boundaries become deployment constraints"; hiding a full agent behind one tool call is an anti-pattern.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
