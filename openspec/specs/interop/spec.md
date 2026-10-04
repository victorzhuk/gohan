# Interop: MCP client and server

Capability: `interop` · Spec v1.3 (ADR-0134) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

Binds gohan's seams to MCP as of the 2026-07-28 specification (stateless core, multi round-trip requests, Tasks extension, cacheable catalogs) without adding protocol surface to core. A2A stays a later adapter (`docs/design/adapters.md`). Out of scope: MCP prompts, resources, apps.


## Contract

### Core addition

`suspension` already declares `SuspendReason` value `AwaitingInput` with payload `InputRequest{Prompt, Schema}`; `interop` only binds the MCP client and server to it.

Any tool or flow may return `gohan.SuspendTool(AwaitingInput, InputRequest{...})`. `Resume(token, Deliver(data))` validates `data` against `Schema` before the run continues; invalid data returns `ErrInputInvalid` and keeps the token unconsumed. `AwaitingInput` payloads pass the `StageToolResult` guard on delivery like any external input.

### `adapter/mcp` client

1. Registration: `mcp.Client(url, mcp.Namespace(ns))` registers each server tool as `<ns>__<tool>` with `ns` matching `^[a-z][a-z0-9]{0,15}$`; the namespace is required when a flow has more than one imported set and is part of the pinned manifest, so a catalog refresh cannot move a tool between namespaces; the server side maps `<ns>__` back when exposing a flow. Server tools register as `Untrusted` + `Deferred`; `MaxEffect` caps at `ReadOnly` unless raised in wiring. Tool annotations (`readOnlyHint`, `destructiveHint`, `idempotentHint`) are recorded in the manifest and may only **lower** the effective effect, never raise it.
2. Catalog: `tools/list` `ttlMs`/`cacheScope` set the manifest refresh interval; a refresh that changes a pinned tool's hash fails closed (`tools.rug-pull`); no `initialize` handshake or session header is assumed.
3. **MRTR.** A `resultType: "input_required"` reply is not an error. A confirmation request (boolean / accept-decline shape) becomes `HumanApproval` with an `ApprovalRequest` built from the pending call; any other request becomes `AwaitingInput` with the server's schema. On `Resume` the adapter replays the original `tools/call` with `inputResponses` filled from `ResumeInput`; the replay uses the same `CallKey`, so the journal sees one call.
4. **Tasks.** A tool returning a task handle becomes `AwaitingTool{handle}`; the adapter wakes the run from `subscriptions/listen` when available and otherwise registers a `Waker` poll on `tasks/get`; task cancellation follows run cancellation (`SideEffect` shield unchanged).
5. Identity: the outbound credential comes from `CredentialSource` (token exchange / on-behalf-of); `RunInfo` ids and `CostTags` travel in `_meta`; deprecated roots/sampling/logging RPCs are not implemented.

### `adapter/mcp` server

1. One `Flow` is one MCP tool with the `In`/`Out` schemas; the exposure rule stays (single-shot recipes via MCP, agent flows via A2A, both only by explicit option).
2. `HumanApproval` and `AwaitingInput` from the flow become `input_required` replies; the resume token travels in `_meta.gohan.token`. The client's retry with `inputResponses` is `Resume(token, in)`; `Approver` is the principal authenticated by the MCP OAuth layer, set by the adapter as transport code (ADR-0028/ADR-0044).
3. A flow whose expected duration exceeds the request budget is exposed through the Tasks extension: `tools/call` returns a task, `Detached` run + `EventLog` back `tasks/get`, `subscriptions/listen` streams `Seq`-ordered status.
4. `AwaitingExternal`/`AwaitingBatch`/`Scheduled` map to a Tasks status (`working`) with `WakeAt` where known; they are never surfaced as protocol errors.
5. Requests are stateless: nothing about a run is held in process between `tools/call` and its retry; `Checkpoints` and `Runs` are the only state.


### `adapter/httpapi`

```go
// (illustrative) adapter/httpapi; http.Request and http.Handler from net/http
type Authenticator func(r *http.Request) (Principal, Credential, error)

func NewHandler(stack *Stack, auth Authenticator) http.Handler
```

1. **OpenAPI first.** `api/gohan.yaml` (OpenAPI 3.1) is the source of truth; the server is generated with `ogen` and the handlers only translate — every rule (ownership, scopes, leases, limits, catalog) stays in the `Stack`. `task api:check` fails when the document and the generated server diverge, and the document's version follows the root module. Resources: `POST /sessions/{id}/runs` (`Send`; `mode=stream|background|wait`), `POST /sessions/{id}/continue`, `POST /runs/{id}/resume`, `POST /runs/{id}/cancel`, `GET /runs/{id}/events?after=<seq>` (SSE, `id:` = `Seq`, `Last-Event-ID` reattaches), `GET /runs/{id}` (`Inspect`), `GET /sessions`, `PATCH /sessions/{id}` (`UpdateSession`), `POST /sessions/{id}/fork`, `DELETE /sessions/{id}`, `POST /feedback`, `POST /sessions/{id}/control/{takeover|handback|message}`, `PUT`/`DELETE /sessions/{id}/hold`, `GET /flows/{name}/explain`, `POST /flows/{name}/invoke`, `DELETE /subjects/{id}` (`EraseSubject`), `GET /memory/{subject}`, `GET /healthz`, `GET /readyz`.
2. **Multitask.** A `Send` on a session with a live run answers `409 gohan.run_active` unless the request carries `X-Gohan-Multitask: steer` (→ `Steer`; `ErrRunNotActive` falls back to `Send`) or `X-Gohan-Multitask: interrupt` (→ `Cancel`, then `Send`); there is no server-side queue.
3. **Idempotency and errors.** `Idempotency-Key` becomes `WithIdempotencyKey`; every error is `ProblemOf` as `application/problem+json` with `Retry-After` when set; a `background` run may pass `webhook` (URL) which overrides the tenant's notice `Endpoint` for that run only and must pass `EgressPolicy`.
4. **Identity.** `Authenticator` is required: `NewHandler` panics without one and never serves an anonymous principal; it runs before routing on every request and sets `Principal`/`Credential` in ctx as transport code (ADR-0028); scopes are checked by the `Stack`, never by the handler.
5. **Health.** `/healthz` answers 200 while the process runs; `/readyz` answers 200 when `Stack.Health(ctx).Ready` and 503 otherwise, with the report as JSON.

## Requirements

### Requirement: AwaitingInput

#### Scenario: structured input round-trip
ID: `interop.awaiting-input-roundtrip`
- WHEN a tool suspends with `AwaitingInput{Schema}` and `Resume(token, Deliver(data))` carries data matching the schema
- THEN the run continues with `data` as the tool's input response

#### Scenario: invalid input keeps token
ID: `interop.awaiting-input-invalid`
- WHEN `Deliver(data)` does not match `Schema`
- THEN `Resume` returns `ErrInputInvalid` and the token remains usable

### Requirement: MCP client

#### Scenario: namespace required for a second set
ID: `interop.namespace-required-for-second-set`
- WHEN a flow wires two MCP clients and one has no `Namespace`
- THEN `Build` fails naming the client without a namespace

#### Scenario: namespace in manifest
ID: `interop.namespace-in-manifest`
- WHEN a pinned manifest holds `crm__search` and the server's catalog later renames the tool
- THEN the tool is disabled as drift; no tool moves to another namespace

#### Scenario: annotations never raise
ID: `interop.annotations-never-raise`
- WHEN an MCP tool declares `destructiveHint: false` and `readOnlyHint: true` but wiring did not raise `MaxEffect`
- THEN the effective effect is `ReadOnly` and the annotation is recorded in the manifest

#### Scenario: input_required as suspension
ID: `interop.mrtr-confirmation`
- WHEN a server replies `input_required` with a confirmation request
- THEN the run suspends with `HumanApproval` and an `ApprovalRequest` naming the pending MCP call

#### Scenario: replay with inputResponses
ID: `interop.mrtr-replay`
- WHEN the run is resumed
- THEN the adapter issues the same `tools/call` with `inputResponses` under the original `CallKey` and the journal records one entry

#### Scenario: task handle
ID: `interop.task-handle`
- WHEN a server returns a task for `tools/call`
- THEN the run suspends with `AwaitingTool` and resumes when `subscriptions/listen` or the `Waker` poll reports completion

#### Scenario: catalog ttl
ID: `interop.catalog-ttl`
- WHEN `tools/list` reports `ttlMs` and a refresh after it yields a changed hash for a pinned tool
- THEN the tool is disabled and `gohan.tool.manifest_drift` increments (added to `telemetry`)

### Requirement: HTTP API

#### Scenario: OpenAPI matches handlers
ID: `interop.http-openapi-matches-handlers`
- WHEN `task api:check` runs with a route added to the server but not to `api/gohan.yaml`
- THEN the check fails naming the route

#### Scenario: stream and reattach
ID: `interop.http-send-stream-reattach`
- WHEN a client streams `POST /sessions/{id}/runs?mode=stream`, drops after event 17 and calls `GET /runs/{id}/events` with `Last-Event-ID: 17`
- THEN events from `Seq` 18 arrive without gap or duplicate

#### Scenario: multitask header
ID: `interop.http-multitask-header`
- WHEN a `Send` arrives during a live run with `X-Gohan-Multitask: steer`
- THEN the message is applied through `Steer` and the response is `202` with the run id; without the header the response is `409 gohan.run_active` with `Retry-After`

#### Scenario: problem json
ID: `interop.http-problem-json`
- WHEN `DELETE /sessions/{id}` hits a held session
- THEN the response is `409 application/problem+json` with `type: https://gohan.dev/problems/gohan.session_held`

### Requirement: MCP server

#### Scenario: approval as input_required
ID: `interop.server-input-required`
- WHEN an exposed flow suspends with `HumanApproval`
- THEN the reply is `input_required` carrying the resume token in `_meta`

#### Scenario: stateless retry resumes
ID: `interop.server-stateless-resume`
- WHEN the client retries with `inputResponses` on another pod
- THEN `Resume(token, in)` runs with `Approver` set to the OAuth-authenticated principal and the flow completes

#### Scenario: long flow as task
ID: `interop.server-long-task`
- WHEN a flow marked for the Tasks extension is called
- THEN `tools/call` returns a task, `tasks/get` reflects the run state and `subscriptions/listen` delivers `Seq`-ordered updates
