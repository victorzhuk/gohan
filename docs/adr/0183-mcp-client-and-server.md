# ADR-0183: `adapter/mcp` client and server in M3

Status: proposed · Extends ADR-0074 and ADR-0092 · Origin: m3-interop-mcp

## Decision

M3 ships `adapter/mcp` as one standalone module with two surfaces on the 2026-07-28 binding. The client registers each imported server tool as `<ns>__<tool>` with `Untrusted` + `Deferred` trust and an effect cap that annotations may only lower; discovery, scope checks and guarding from the `tools` capability apply to imported tools unchanged, and descriptions are pinned into the manifest with the existing fail-closed drift check. The server exposes one `Flow` as one stateless MCP tool and surfaces `HumanApproval` and `AwaitingInput` as an `input_required` error carrying the resume token in `_meta.gohan.token`; the retry is `Resume(token, in)` with the OAuth principal as `Approver`, and every other suspension reason maps to Tasks status, never a protocol error.

## Context and evidence

ADR-0074 fixed interop as adapters and ADR-0092 fixed the 2026-07-28 binding, but the module itself was unscheduled. M3 delivers it. The deferred-tool contracts in `openspec/specs/tools/spec.md` already govern registered-but-undiscovered tools, so imported tools need no new `tools` surface; the only new contract is the boundary rule that the import cannot raise trust or effect and cannot rewrite pinned descriptions.

See `docs/design/evidence.md` for the published sources behind the binding decisions.

## Consequences

`interop` gains requirements for imported deferred registration, boundary guarding, description drift and the server suspension token surface. `tools` stays unchanged. A2A remains a later adapter per ADR-0074. Changes to the mapping require a superseding ADR.
