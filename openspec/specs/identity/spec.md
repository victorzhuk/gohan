# Identity, approval and run trees

Capability: `identity` · Spec v1.7 (ADR-0134) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `identity` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.4 RunInfo, Principal, Approval

```go
type Principal struct {
	Subject string
	Tenant  string
	Scopes  []string
}

type Credential struct {
	Token     string
	ExpiresAt time.Time
}

type SessionOwner struct {
	Tenant  string
	Subject string
}

var (
	ErrSessionForbidden = errors.New("gohan: principal may not access this session")
	ErrResumeInsideRun  = errors.New("gohan: resume called from inside a run")
)

type Approval struct {
	Approver Principal
	Verdict  ApprovalVerdict
	At       time.Time
}

type RunInfo struct {
	Flow         string
	SessionID    string
	RunID        string
	RootRunID    string
	ParentRunID  string
	Turn         int
	Principal    Principal
	LatencyClass LatencyClass
	CostTags     CostTags
	Residency    string
	ReleaseID    string
	Variant      string
	Mode         RunMode
}

type CostTags struct {
	Feature     string
	Environment string
	CostCenter  string
}

func WithPrincipal(ctx context.Context, p Principal) context.Context
func WithCredential(ctx context.Context, c Credential) context.Context
func WithIdempotencyKey(ctx context.Context, key string) context.Context
func CredentialFrom(ctx context.Context) (Credential, bool)
func PrincipalFrom(ctx context.Context) (Principal, bool)
func ApprovalFrom(ctx context.Context) (Approval, bool)
func RunInfoFrom(ctx context.Context) (RunInfo, bool)
func IdempotencyKey(ctx context.Context) (string, bool)

type CredentialSource interface {
	Credentials(ctx context.Context, p Principal) (Credential, error)
}
```

Rules:

1. Only transport code calls `WithPrincipal`. Flows without a principal fail with `ErrNoPrincipal` (the sentinel declared in `flow`; `Send`, `Invoke` and `Resume` all return it per rule 8) unless built with `agent.AllowAnonymous()`.
2. Credentials never live on `Principal`: `Credential` travels only in `ctx` (`WithCredential`, set by transport code) and is never persisted, logged or exported to spans. `Checkpoint.Originator` is a `Principal` and therefore cannot carry a token by type.
3. On resume, the originator principal is restored from the checkpoint and passed through `CredentialSource` (token exchange / on-behalf-of / service-issued) before any tool runs. The approver is available via `ApprovalFrom(ctx)` and recorded in journal and audit spans; tools never execute with approver rights.
4. A sub-flow invoked from a tool (`FlowAsTool`) inherits `RootRunID` and sets `ParentRunID`; budget, `RunLimits`, spans and `Runs` recovery are keyed by the root. Sub-flow output enters the parent as a tool result and passes the tool-result guard. Isolation, effect derivation, nested suspension, child limits and fan-out failure policy are defined by `subflows`.
5. Approval enters the system only through `Resume`; the harness sets `ResumeInput.Approver` from the verified transport principal in `ctx` and checks it against the `ApprovalPolicy` for the request's risk tier (`permission`) — `Approve()`, `Reject()`, `EditArgs()` and `ApproveScope()` take no principal. `Resume` fails with `ErrResumeInsideRun` when `ctx` carries a `RunInfo` (a tool, sub-flow, provider or decider cannot resume anything). No tool, model output, sub-flow or context provider can produce an approval; `ResumeInput` is not constructible from `ctx` inside a run.
6. **Session ownership.** `SessionLog` records `SessionOwner{Tenant, Subject}` at the first append. `Send`, `Resume`, `Inspect`, `Sessions`/`UpdateSession`, `Feedback`, `SharedState`/`SetSharedState` and `evals.Import` require `PrincipalFrom(ctx)` to match the owner's tenant and either the subject or a `session:read` (read paths) / `session:write` (`Send`, `Resume`) scope; otherwise `ErrSessionForbidden` is returned before any store read. Child sessions (`subflows`) and shadow sessions (`release`) inherit the owner. `Recover` and the reaper are harness-internal and exempt.
7. Tool code reads identity from `PrincipalFrom(ctx)` only. `NewTool` accepts `ExcludeFields(names...)` to keep identity-like fields out of the schema, and `Build` warns when a tool schema contains fields matching a configurable identity pattern (`user_id`, `tenant`, `customer_id`, …).
8. **Context values.** The values gohan carries in `ctx` are a closed set: `Principal`, `Credential`, `Approval`, `RunInfo`, the idempotency key and shared state. Keys are unexported and defined only in `core`; no other package can set or shadow them. Setters (`WithPrincipal`, `WithCredential`, `WithIdempotencyKey`) are called by transport and harness code only, never by tools, flows or `std`; `examples/` ships a depguard rule forbidding `context.WithValue` outside `core` and `adapter/*`. Every accessor returns an `ok` and never panics; outside a run all return `ok == false`. Required values are checked once at the seam: `Send`, `Invoke` and `Resume` return `ErrNoPrincipal` before `RunStarted` and before any store access, so downstream code assumes presence. Anything else a tool or flow needs (config, clients, tenant settings) is a constructor argument captured by the tool's `fn` (`tools` *Names and identity*), never a context value.

   Propagation across ctx boundaries:

   | Boundary | `Principal` | `Credential` | `RunInfo` | Idempotency key | `Approval` |
   |---|---|---|---|---|---|
   | cancel shield (`context.WithoutCancel`) | kept | kept | kept | kept | kept |
   | `Detached` harness ctx | copied | re-issued via `CredentialSource` | copied | copied | dropped |
   | `FlowAsTool` child | inherited | inherited unless `subflows` isolation forbids | new (`ParentRunID` set) | dropped | dropped |
   | `Waker` poll / resume on another pod | restored from checkpoint | re-issued via `CredentialSource` | restored | restored | set by `Resume` |
   | `sandbox` process | none | none (secrets via `CredentialSource` only) | none | none | none |

## Requirements

### Requirement: Identity

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

### Requirement: Session ownership and resume boundaries

#### Scenario: cross-tenant send forbidden
ID: `identity.session-forbidden-cross-tenant`
- WHEN a principal of tenant B calls `Send` on a session owned by tenant A
- THEN `ErrSessionForbidden` is returned and no store is read

#### Scenario: read scope allows inspect
ID: `identity.session-scope-read`
- WHEN a principal of the owner's tenant with scope `session:read` but a different subject calls `Inspect`
- THEN it succeeds; without the scope it fails with `ErrSessionForbidden`

#### Scenario: resume inside run refused
ID: `identity.resume-inside-run-refused`
- WHEN a tool running inside a run calls `flow.Resume` with the run's own pending token
- THEN `ErrResumeInsideRun` is returned and the token is not consumed

#### Scenario: approver from transport only
ID: `identity.approver-from-transport-only`
- WHEN transport code calls `Resume(ctx, token, Approve())` with principal `op-7` in `ctx`
- THEN the journal and audit record `Approver: op-7`; there is no API to pass a different approver

#### Scenario: credential not on principal
ID: `identity.credential-not-on-principal`
- WHEN a checkpoint, audit record or span is written for a run whose ctx carries a `Credential`
- THEN none of them contains the token, and `Principal` has no field that could hold it

### Requirement: Context values

#### Scenario: accessors outside a run
ID: `identity.accessor-outside-run`
- WHEN `PrincipalFrom`, `CredentialFrom`, `ApprovalFrom`, `RunInfoFrom`, `IdempotencyKey` and `SharedState` are called on `context.Background()`
- THEN each returns `ok == false` and zero values without panicking

#### Scenario: values survive the cancel shield
ID: `identity.values-survive-shield`
- WHEN a `SideEffect` tool runs under the cancel shield after the caller's ctx is cancelled
- THEN `PrincipalFrom`, `CredentialFrom` and `RunInfoFrom` still return the run's values inside the tool

#### Scenario: detached run re-issues credential
ID: `identity.detached-reissues-credential`
- WHEN a `Detached` flow is started with a `Credential` in the caller's ctx
- THEN the harness ctx carries the caller's `Principal` and `RunInfo`, `CredentialSource.Credentials` is called for the principal, and the caller's `Credential` value is not reused

#### Scenario: credential not in sandbox
ID: `identity.credential-not-in-sandbox`
- WHEN a `sandbox` tool set executes a command while a `Credential` is present in the run ctx
- THEN the process environment and mounted files contain no value derived from the `Credential`

#### Scenario: idempotency key set by transport only
ID: `identity.idempotency-key-transport-only`
- WHEN a tool calls `WithIdempotencyKey` on its own ctx and invokes a sub-flow
- THEN `task lint` fails on the depguard rule and, at runtime, the sub-flow's `IdempotencyKey` returns `ok == false`

### Requirement: Run trees

#### Scenario: tree budget
ID: `identity.tree-budget`
- WHEN a hub flow invokes three sub-flows via `FlowAsTool` and `MaxCost` is set on the hub
- THEN cumulative cost across all four runs is charged against the one limit and the hub aborts when it is exceeded

#### Scenario: nested spans and recovery
ID: `identity.nested-spans-and-recovery`
- WHEN a sub-flow run is stale after a crash
- THEN `Recover` reclaims the root and replays the tree; spans nest under the root `invoke_agent`
