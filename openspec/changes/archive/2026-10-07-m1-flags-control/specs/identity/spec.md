# Spec Delta

## MODIFIED Requirements

### Requirement: Identity

`RunInfo` MUST carry the run's frozen flag values in a `Flags` field of type `runtime.FlagSnapshot` so an in-run reader sees them from `ctx` (the `flags` *Frozen and live flags* requirement); `RunInfoFrom` MUST return them unchanged.

#### Scenario: no principal
ID: `identity.no-principal`
- WHEN a flow without `AllowAnonymous` is invoked without a principal
- THEN `ErrNoPrincipal`

#### Scenario: tenant for provider key from ctx
ID: `identity.tenant-for-key-from-ctx`
- WHEN the model passes `tenant` in tool arguments or a flow input while the principal's tenant is `t1`
- THEN the provider key is selected for `t1` and no other tenant's key is reachable

#### Scenario: steer is owner-checked and root-only
ID: `identity.steer-root-only`
- WHEN a principal of another tenant calls `Steer`, or the owner steers a `FlowAsTool` child's session id
- THEN the first returns `ErrSessionForbidden` and the second `ErrRunNotActive`; only the root run's history receives steers

#### Scenario: HTTP authenticator required
ID: `identity.http-authenticator-required`
- WHEN `httpapi.NewHandler` is built without an `Authenticator`, or the authenticator returns an error for a request
- THEN construction panics in the first case and the request answers `401 gohan.no_principal` in the second, and no `Stack` method runs

#### Scenario: notice endpoint per tenant
ID: `identity.notice-endpoint-per-tenant`
- WHEN tenants `t1` and `t2` finish runs and `Endpoint` returns a different URL and secret for each
- THEN each notice goes only to its tenant's URL, signed with that tenant's secret, and a tenant without an endpoint receives nothing

#### Scenario: hold requires scope
ID: `identity.hold-requires-scope`
- WHEN the session owner without `session:hold` calls `UpdateSession(Hold: "case-42")`
- THEN `ErrSessionForbidden` is returned and no `hold_set` audit record is written

#### Scenario: session listing owner-checked
ID: `identity.sessions-owner-checked`
- WHEN a principal without `session:read` lists sessions with a query naming another subject
- THEN the listing is scoped to the principal's own subject and nothing of the other subject is returned

#### Scenario: no principal fails at the seam
ID: `identity.no-principal-at-seam`
- WHEN `Send` is called without a principal on a flow without `AllowAnonymous`
- THEN `ErrNoPrincipal` is returned before `RunStarted` is emitted and before any `Runs` or `SessionLog` access

#### Scenario: model cannot set identity
ID: `identity.model-cannot-set-identity`
- WHEN the model passes `user_id` in tool args for a tool built with `NewTool`
- THEN `user_id` is absent from the schema (`ExcludeFields`), the arg is ignored, and the tool acts on `PrincipalFrom(ctx)`

#### Scenario: credentials on resume
ID: `identity.credentials-on-resume`
- WHEN a run resumes after the original token expired
- THEN `CredentialSource.Credentials` is called for the originator before the tool executes

#### Scenario: token never exported
ID: `identity.token-never-exported`
- WHEN content capture is enabled
- THEN no span, log or metric contains the `Credential` token
