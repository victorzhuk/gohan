# Design: MCP client and server

## Context

ADR-0074 fixes interop as adapters; ADR-0092 fixes the 2026-07-28 binding and `AwaitingInput`. `openspec/specs/interop/spec.md` already states the client and server contracts (namespaces, MRTR, Tasks, ttl drift, stateless server). This change builds the module to those contracts and pins the two sides the spec leaves implicit for M3: imported tools remain governed deferred tools and the server surfaces suspension as an `input_required` error carrying the resume token.

## Goals / Non-Goals

Goal: a standalone `adapter/mcp` module usable as client and server with no core changes. Non-goals: MCP prompts, resources, apps, A2A (later adapter), any MCP host surface in core, non-2026-07-28 protocol versions.

## Decisions

One module, two surfaces. `mcp.Client(url, mcp.Namespace(ns))` registers each server tool as `<ns>__<tool>` with the namespace in the pinned manifest; tools register `Untrusted` + `Deferred` with `MaxEffect` capped at `ReadOnly` unless wiring raises it, and tool annotations may only lower the effective effect. The deferred registration reuses the existing `tools` contracts unchanged: discovery, activation persistence and scope checks apply to imported tools as to local ones, so no `tools` delta is required.

Descriptions are guarded at the boundary: the server-provided description is stored in the manifest, hashed with the spec, and a refresh that changes it fails closed as drift (`tools.rug-pull` semantics). The client never lets a catalog refresh move a tool between namespaces or rewrite its effect, trust or schema.

Client suspension mapping stays as specified: confirmations to `HumanApproval`, other `input_required` to `AwaitingInput` with the server schema, task handles to `AwaitingTool` woken by `subscriptions/listen` or a `Waker` poll, replays under the original `CallKey`. Catalog `ttlMs` drives refresh; changed hashes disable the tool and increment `gohan.tool.manifest_drift`.

The server exposes one `Flow` as one stateless MCP tool with the `In`/`Out` schemas. `HumanApproval` and `AwaitingInput` become `input_required` replies with the resume token in `_meta.gohan.token`; the stateless retry is `Resume(token, in)` with `Approver` set from the MCP OAuth principal as transport code. `AwaitingExternal`, `AwaitingBatch` and `Scheduled` map to Tasks `working` status, never protocol errors. Long flows use the Tasks extension with `Detached` runs and `EventLog`-backed `tasks/get` and `subscriptions/listen`. No run state lives in process between `tools/call` and its retry.

## Risks / Trade-offs

Risk depth: existing-service-strict, no core changes. The guarding boundary is the main new risk: a hostile server rewriting descriptions or annotations between refreshes; the fail-closed hash check and lower-only annotation rule bound it. MRTR replay depends on the journal treating the replay as one call; the existing `CallKey` contract covers it and is asserted, not re-implemented.

## Migration Plan

New module, no migration. Adoption is explicit wiring of `mcp.Client` or the MCP server handler. Rollback removes the wiring; manifests that pinned imported tools keep disabling them as drift after removal, matching existing manifest behavior.

## ADR

`docs/adr/0183-mcp-client-and-server.md` records the M3 module decision: one `adapter/mcp` module on the 2026-07-28 binding, guarding intact across the import boundary, suspension surfaced as an error carrying the resume token.
