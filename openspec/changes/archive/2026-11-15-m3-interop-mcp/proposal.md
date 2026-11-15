# Proposal: MCP client and server

## Why

M3 ships the `adapter/mcp` module promised by ADR-0074 and ADR-0092: the client that imports external MCP servers as governed tools and the server that exposes gohan flows to MCP hosts, both on the 2026-07-28 binding. The capability spec already fixes the contracts; this change implements them and closes the remaining spec gaps on the client-side guarding of imported tools.

## What Changes

- Add the `adapter/mcp` module with the client: namespace-pinned registration of server tools as `Untrusted` + `Deferred` tools, annotation-derived effect caps that may only lower effect, MRTR `input_required` mapping, Tasks-to-`AwaitingTool` mapping, and ttl-driven manifest refresh with the fail-closed drift check.
- Add the server side: one `Flow` as one stateless MCP tool, `HumanApproval`/`AwaitingInput` surfaced as `input_required` replies carrying the resume token in `_meta`, stateless retry as `Resume(token, in)` with the OAuth principal as `Approver`, and long flows through the Tasks extension.
- Extend the `interop` spec: imported tools stay governed deferred tools with trust, effect and description guarding intact across the import boundary, and server-side suspension surfaces as an error carrying the resume token.
- No `tools` delta is needed: the deferred-tool registration and guarding contracts in `openspec/specs/tools/spec.md` already cover imported tools without modification.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `interop`: MCP client deferred registration and guarding, MCP server suspension token surface.

## Impact

New standalone module `adapter/mcp` implementing frozen core seams only (`ToolSpec`, `SuspendError`, `Resume`, manifest hashing, `CredentialSource`). No core changes; `suspension.AwaitingInput` and the interop contracts are already frozen. Scenario ids register at implementation admission per ADR-0171.
