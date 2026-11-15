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

## Implementation contract

Binding: `github.com/modelcontextprotocol/go-sdk` v1.8.0, protocol revision `2026-07-28` (the SDK default). The SDK carries stateless streamable HTTP, MRTR (`NeedsInput`, `InputResponses`, `RequestState`), cacheable catalogs (`ttlMs`, `cacheScope`), annotations and native `subscriptions/listen`; it has no Tasks extension, so `tasks/get` is registered as a custom JSON-RPC method.

| File | Responsibility |
|---|---|
| `client.go` | `Client(url, opts...) (*Set, error)`, options, `Tools`, `Assemble`, imported-tool registration and call mapping |
| `catalog.go` | `tools/list` fetch, `ttlMs` refresh loop, pin compare, drift disable, `gohan.tool.manifest_drift` |
| `guard.go` | `AnnotationCap`: annotations lower the wiring effect cap and never raise it |
| `mrtr.go` | `input_required` → suspension, child-token continuation, schema guard, `tools/call` replay |
| `tasksclient.go` | task meta → `AwaitingTool`, `Waker` poll fallback |
| `server.go` | `Expose`, `NewHandler`, suspension → `input_required` reply, stateless retry → `Flow.Resume` |
| `tasksserver.go` | `tasks/get`, `TaskStatus`, `Detached` run row, `EventLog`-backed ordered updates |
| `wire.go` | `_meta` keys, `TaskRef`, opaque `RequestState` codec, method names |
| `internal/schema` | JSON-Schema instance validation over `google/jsonschema-go` (2020-12) |

Client decisions:

- Namespace pins the import: `<ns>__<tool>`, `^[a-z][a-z0-9]{0,15}$`. `mcp.Assemble` refuses more than one set when any set has no namespace and names that server's URL, before `Build` runs; core keeps `ErrToolCollision` as the backstop.
- Imported tools are `Deferred` and `Untrusted`. Effective effect is the lower of the wiring `MaxEffect` (default `ReadOnly`) and the annotation cap (`readOnlyHint` → `ReadOnly`; `destructiveHint: false` with `idempotentHint` → `Idempotent`), then `ApplyToolPolicy` runs, so no annotation can raise it. Trust has no wire field.
- Refresh runs on first `tools/list` and every `ttlMs` (minimum 1s) while the set lives. Each refresh recomputes `std.ToolManifestHash`; a pinned tool whose hash changed or that disappeared is disabled and `gohan.tool.manifest_drift` increments once per changed tool at disable time. An unpinned first catalog self-pins. A refresh never moves a tool between namespaces or raises trust or effect.
- MRTR uses the chain-resume seam: the tool mints a single-use token in a required `stores.Checkpoints` and returns `&types.SuspendError{Token, Reason: HumanApproval|AwaitingInput, Payload}`. Core stores it as `Checkpoint.Child` and, on resume, calls the tool's `ResumeChild`, which validates delivered data against the server schema **before** consuming its token, then replays `tools/call` with `inputResponses` and the echoed `RequestState`. A returned `SuspendError` re-suspends, so multi-round input requests chain. A confirmation carries an adapter-built `permission.ApprovalRequest`; chained confirmations deliberately bypass core quorum, eligibility and approval receipts because the confirmation is the MCP server's, not a gohan gate's. The added dependency is `mcp.WithCheckpoints`, required at construction; a dedicated store instance keeps adapter records out of the recovery sweep.
- A task handle in `_meta.gohan.task` suspends with `AwaitingTool`; completion arrives from the subscription stream when available and otherwise from a bounded poll.

Server decisions:

- One exposed flow is one MCP tool with the declared `In`/`Out` schema. `HumanApproval` and `AwaitingInput` produce an `input_required` result carrying `_meta.gohan.token` and an opaque `RequestState`; the delta's "error" wording is satisfied by the `input_required` outcome, because the protocol and the SDK put `_meta` only on results and a JSON-RPC error has no `_meta`.
- The retry decodes `inputResponses` into `stores.ResumeInput` (accept → `Approve`, decline → `Reject`, content → `Deliver` after schema validation) and calls `Flow.Resume` with the principal from the OAuth layer placed in context as transport code; core replaces any caller-set approver with that principal.
- `AwaitingExternal`, `AwaitingBatch`, `Scheduled` and `AwaitingTool` become a task reference with `TaskStatus` `working` and `wakeAt` where known; they are never JSON-RPC errors. Tasks rows live in `Runs`, status and ordered updates come from `Runs` and `EventLog`, and the handler holds no run state between calls.
- The Tasks extension has no SDK support, and the SDK's `subscriptions/listen` stream carries only catalog and resource-change notifications, so run progress is served by `tasks/get` plus an `EventLog.Read`-backed ordered pull surface. The notification-stream attachment stays a single named seam; no notification is faked.

Contract consequences recorded for review:

- `interop.mrtr-replay`'s "journal records one entry" holds only for a journaled call (effect above `ReadOnly`): the original round completes one entry and the replayed round adds none. At the default `ReadOnly` cap the call is not journaled at all. The fixture must raise `MaxEffect` to assert the count, never the entry content.
- `interop.awaiting-input-invalid` ("the token remains usable") describes a non-chained core `AwaitingInput` suspension. Through the chain seam the parent token is spent before `ResumeChild` runs, so the adapter proves error-before-replay, not token reuse. Core-local `AwaitingInput` schema validation stays unimplemented because the root module carries no dependency; that gap is a named follow-up, not absorbed here.
