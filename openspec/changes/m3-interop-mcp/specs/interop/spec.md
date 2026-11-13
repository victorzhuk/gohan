## ADDED Requirements

### Requirement: Imported tools are governed deferred tools

The MCP client MUST register imported server tools as `Untrusted` + `Deferred` under the pinned namespace, and discovery, scope checks and effect gating MUST apply to them unchanged.

#### Scenario: deferred registration
ID: `interop.client-deferred-registration`
- WHEN `mcp.Client` imports a server catalog with a pinned namespace
- THEN each tool registers as `<ns>__<tool>`, `Deferred` and `Untrusted`, excluded from the first request until discovered

### Requirement: Guarding is intact across the import boundary

Imported tools MUST keep the client-side trust and effect decision at the boundary: annotations may only lower the effective effect and no refresh may raise trust, effect or permissions.

#### Scenario: guarding intact
ID: `interop.client-guarding-intact`
- WHEN a server catalog refresh declares `readOnlyHint: true` on a tool wired with `MaxEffect: SideEffect`
- THEN the effective effect lowers to `ReadOnly`, the annotation is recorded in the manifest and no later refresh can raise it back

### Requirement: Descriptions are pinned and fail closed

The client MUST hash the imported description with the tool spec and MUST disable the tool as drift when a refresh changes it or the spec hash.

#### Scenario: description drift
ID: `interop.client-description-drift`
- WHEN a refresh after `ttlMs` changes a pinned tool's description without changing its name
- THEN the tool is disabled as drift and `gohan.tool.manifest_drift` increments

### Requirement: Client input_required carries the server schema

A non-confirmation `input_required` reply MUST suspend the run with `AwaitingInput` carrying the server schema, and resumed data MUST pass the schema guard before replay.

#### Scenario: awaiting input schema
ID: `interop.client-awaiting-input-schema`
- WHEN a server replies `input_required` with a non-boolean schema and the resume delivers non-conforming data
- THEN the run suspends with `AwaitingInput{Schema}` and invalid data fails the guard before the `tools/call` replay

### Requirement: Server suspension surfaces as an error carrying the token

The MCP server MUST surface a suspending flow as an `input_required` error whose `_meta.gohan.token` carries the resume token, and MUST NOT hold run state in process between the call and its retry.

#### Scenario: suspension token error
ID: `interop.server-suspension-token-error`
- WHEN an exposed flow suspends and `tools/call` returns
- THEN the reply is an `input_required` error with the resume token in `_meta.gohan.token` and no run state remains in the serving process

### Requirement: Server AwaitingInput resumes statelessly

A retry with `inputResponses` MUST run `Resume(token, in)` with `Approver` set to the OAuth-authenticated principal and MUST validate `in` against the suspension schema.

#### Scenario: awaiting input token
ID: `interop.server-awaiting-input-token`
- WHEN a client retries the suspended call with `inputResponses` from another process
- THEN the flow resumes on the token, the schema validates `in` and `Approver` is the authenticated principal

### Requirement: Other suspensions never become protocol errors

The server MUST map `AwaitingExternal`, `AwaitingBatch` and `Scheduled` to Tasks `working` status and MUST NOT surface them as protocol errors.

#### Scenario: suspensions never protocol errors
ID: `interop.server-suspensions-never-protocol-errors`
- WHEN an exposed flow suspends with `AwaitingExternal` and a known wake time
- THEN `tools/call` returns a task in `working` status with `WakeAt` and no error is emitted
