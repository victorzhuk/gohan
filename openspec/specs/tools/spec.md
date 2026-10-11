# Tools

Capability: `tools` · Spec v1.4 (ADR-0129) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `tools` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.6 Tools

```go
type Effect int

const (
	ReadOnly Effect = iota
	Idempotent
	SideEffect
)

type RiskTier int

const (
	RiskLow RiskTier = iota
	RiskMedium
	RiskHigh
)

type ToolSpec struct {
	Name           string
	Description    string
	Schema         json.RawMessage
	Effect         Effect
	RequiredScopes []string
	Timeout        time.Duration
	ReadBack       string
	MaxOutput      int
	Deferred       bool
	Risk           RiskTier
	Capabilities   Capabilities
	Network        bool
	Trust          Trust
	Executor       Executor
	Egress         *EgressPolicy
	Verify         func(ctx context.Context, args json.RawMessage, r ToolResult) (Outcome, error)
}

type PrivateRanges int

const (
	DenyPrivate PrivateRanges = iota
	AllowPrivate
)

type EgressPolicy struct {
	Allow         []string
	Schemes       []string
	PrivateRanges PrivateRanges
	MaxRedirects  int
	MaxBytes      int64
	Timeout       time.Duration
}

type EgressDenied struct {
	URL    string
	Reason string
}

var ErrEgressPolicyRequired = errors.New("gohan: tool reaches the network without an EgressPolicy")

type Executor int

const (
	ByHarness Executor = iota
	ByProvider
)

type ErrManifestDrift struct{ Tool string }
type ErrToolCollision struct{ Name string; Sources []string }

var ErrToolName = errors.New("gohan: tool name must match ^[a-z][a-z0-9_]{0,63}$")
type ErrToolSetDrift struct{ Missing, Extra []string }

var ErrToolDescription = errors.New("gohan: tool description carries an instruction directive")

// Retryable marks err as retryable for the model-visible result of a tool call.
func Retryable(err error) error

// (illustrative) functional options for NewTool
type ToolOption func(*ToolSpec)

type Tool interface {
	Spec() ToolSpec
	Call(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

func NewTool[In, Out any](name, desc string, fn func(context.Context, In) (Out, error), opts ...ToolOption) Tool
```

**Tool author contract.**

- `In` is an infrastructure type (never a domain type): a struct whose fields carry `json` (name; `omitempty` ⇒ optional, otherwise required), `desc` (description), `enum` (comma-separated allowed values), `min`/`max` (numeric bounds, or length for strings and slices) and `pattern` (regexp) tags. Nested structs, slices and maps with string keys are allowed to depth 4; `any`, `map[string]any` and `json.RawMessage` fields are rejected at `Build` unless the tool is built with `WithRawArgs()`. The schema walker is a reflection walker in `core` over `encoding/json/v2` naming; no third-party schema library (Q2 resolved).
- `Out` mapping: `[]Block` passes through unchanged; `ToolResult` passes through (the tool sets `Ref`, `Outcome`, `Error` itself); `string` becomes one `Text` block; any other value is marshalled to JSON in one `Text` block (or `Raw` for providers with native structured results).
- Panics inside `fn` are recovered by the tool step: `Failed(Permanent)` for `ReadOnly`/`Idempotent`, `Outcome: Unknown` for `SideEffect`; the stack is written to the audit record (never to the model) and `gohan.tool.panic{tool}` increments.
- Defaults: `Timeout` by effect — `ReadOnly` 10 s, `Idempotent` 30 s, `SideEffect` 60 s; `MaxOutput` 64 KiB. Both are overridable per tool.
- Context available to `fn`: `RunInfoFrom`, `PrincipalFrom`, `CredentialFrom`, `IdempotencyKey`, `SharedState` (the closed set of `identity` rule 8); none of these is derivable from `In`, and `fn` never calls a context setter.

`NewTool` derives the JSON schema from `In` once at construction, in strict-compatible shape: `additionalProperties: false`, explicit `required`, no `$ref` recursion; `Build` warns at depth > 4 or enums > 50 and suggests tiering. Providers with a strict mode receive `strict: true`. `Out` is marshaled into a `Text` block (or `Raw` for providers with native structured results).

Options: `WithEffect`, `WithScopes`, `WithTimeout`, `WithRisk`, `Deferred()`, `WithFingerprintFields(...)`. `Risk` defaults from `Effect` (`ReadOnly`→Low, `Idempotent`→Medium, `SideEffect`→High) and feeds the gate's defaults and the `ApprovalRequest`. `SideEffect` tools SHALL use `IdempotencyKey(ctx)` for external calls; `NewTool` with `SideEffect` requires `WithIdempotency()` acknowledgement or `WithJournalTx()` (postgres).

```go
type Trust int

const (
	Trusted Trust = iota
	Untrusted
)

type ToolPolicy struct {
	Trust         Trust
	MaxEffect     Effect
	DescribeGuard Guard
}
```

Tools registered from own code default to `Trusted`. Tools imported from eino, adk-go, MCP or any external source default to `Untrusted`: `MaxEffect` caps their declared effect at `ReadOnly` unless explicitly raised in wiring, and `Description` plus schema descriptions pass `DescribeGuard` at build time (rejects instruction-like text, hidden directives, exfiltration patterns). `Stack.Manifest()` lists every tool with `hash(name, description, schema, effect, scopes, trust)`; `gohan.WithPinnedManifest(path)` makes `Build` fail on any hash change (`ErrManifestDrift`), so a changed upstream spec must be re-reviewed like a lockfile bump.

`Deferred` (a `ToolSpec` field) marks a tool that is registered and governed but whose definition is not assembled into the prompt until discovered. `std/toolsearch` provides the `search_tools` meta-tool (`ReadOnly`; takes a query, returns matching specs) and the activation logic: a discovered tool joins the run's active set for the rest of the run; the active set is run state recorded in the checkpoint and audit and asserted on `Replay` (ADR-0062). Deferred tools still count for scope checks and the gate when called. `Build` computes the token cost of always-loaded definitions per profile and warns above `MaxToolContextShare` (default 5 % of `ContextWindow`); `Explain` prints the number. The MCP adapter (v0.4) registers server tools as deferred by default and honours catalog cache hints.

`ReadBack` names a `ReadOnly` tool that verifies this tool's effect (e.g. `get_booking` for `create_booking`); it is offered to the model after an `Unknown` outcome. `MaxOutput` caps inline content (default 64 KiB); the rest goes to the output store.

`Verify` (optional, `ReadOnly` by definition) reads authoritative state and returns the true outcome. The harness runs it after every `SideEffect` result whose outcome is `Unknown`, on `Resume`/`Recover` for every journal entry still `Unknown`, and before `Done` while `Uncertain` is non-empty; a reconciled entry's recorded result becomes `Outcome: Succeeded` or `Outcome: Failed` in the journal with an audit record, and `*UncertainOutcomeError` is returned only for entries with no `Verify` or whose `Verify` errored. `Verify` never re-executes the effect and is itself journaled by `CallKey` + `"/verify"`. The arguments it receives are the canonical arguments the journal retained when the call was reserved, never a reconstruction from history; a `Failed` outcome preserves the `ToolError` the effect recorded, because `Verify` reports an outcome and not a failure detail; and a reconciliation whose journal write or audit record cannot be persisted leaves the entry `Unknown` and returns that error instead of reporting the key reconciled.

Error mapping: a Go `error` becomes `Outcome: Failed` with `Kind: Permanent`; `gohan.Retryable(err)` marks it `Retryable`; a timeout or context deadline on a `SideEffect` tool becomes `Outcome: Unknown` (on `ReadOnly`/`Idempotent` tools it is `Retryable`). `SuspendError`, `AbortError` and cancellation pass through. The model never receives a bare retryable error for a `SideEffect` call.

#### tool/exec

```go
func exec.New(spec gohan.ToolSpec, cmd exec.Cmd, opts ...exec.Option) gohan.Tool
```

- argv slice built from typed args by a user function; never a shell string
- env allowlist (empty by default), working dir, timeout with process-group kill
- stdout/stderr capped (default 64 KiB each), truncated with marker
- non-zero exit → `Outcome: Failed` with exit code and capped stderr; timeout on a `SideEffect` command → `Outcome: Unknown`
- default `Effect` = `SideEffect`
- runs on the host without isolation; model-authored code goes through the `sandbox` capability, and `Build` warns when `tool/exec` is registered without `AllowHostExec()`

#### std/tool/http

```go
type FetchOption func(*fetchConfig)

func WithOutputStore(store stores.OutputStore, tel types.Telemetry) FetchOption
```

`FetchOption` is the one option type `http.Fetch` accepts; `fetchConfig` is the private struct it mutates and carries the store and telemetry the rendered-response cap-and-store step uses. Both fields may stay zero: without a store an overflowing response is bounded to a head excerpt and a no-store marker. `WithOutputStore` wires both at once and both parameters may be `nil`. `http.Fetch(policy)` with no options stays the call shape the *Fetched response cap and store* requirement pins.

#### std/outputs

```go
const DefaultMaxOutput = 64 * 1024

type SanitizeFunc func(ctx context.Context, stream string, data []byte) (clean []byte, leaks int, err error)

type OutputStoreParams struct {
	Store      stores.OutputStore
	Run        types.RunInfo
	Tool       string
	Max        int
	Stream     string
	Data       []byte
	ForceStore bool
	Sanitize   SanitizeFunc
	Tel        types.Telemetry
}

func StoreOutput(ctx context.Context, s OutputStoreParams) ([]types.Block, string, error)
```

`DefaultMaxOutput` is the per-stream inline cap and the tools contract default for `ToolSpec.MaxOutput`. `SanitizeFunc` rewrites one stream's bytes before they are capped or stored and reports how many occurrences it replaced. `OutputStoreParams` carries one stream through the shared step in this order: `Sanitize`, resolve the effective `Max`, store, render excerpt and marker, count. `Max` at or below zero resolves to `DefaultMaxOutput`; `ForceStore` stores under the cap; `Store` and `Tel` may be `nil`. `StoreOutput` returns the blocks, the stored document's ref (`""` when nothing was stored) and an error: a sanitize or store error returns no blocks and no ref, and the leak count `Sanitize` reports is emitted once as `gohan.sandbox.secret_leak`.


## Requirements

### Requirement: Egress

`EgressPolicy` is the one vocabulary for where a tool may connect: `Allow` lists host globs with optional ports; `Schemes` defaults to `https`; `PrivateRanges` defaults to `DenyPrivate` (RFC 1918, loopback, link-local including cloud metadata, IPv6 ULA, CGNAT); `MaxRedirects` defaults to 3 and every hop is re-validated; `MaxBytes` defaults to 8 MiB; `Timeout` defaults to `ToolSpec.Timeout`. A tool declares it with `WithEgress(p)`; `Build` fails with `ErrEgressPolicyRequired` for a tool that declares network capability (`WithExfil()`, `Effect` above `ReadOnly` with `WithHTTP()`, or a `std/tool/http` tool) without one, and derives `Capabilities.Exfil` from the policy: any `Allow` entry outside the private ranges makes the tool exfil-capable. `sandbox.SandboxPolicy.Egress` is the same type.

`std/egress.Client(p, opts...) *http.Client` enforces the policy: it resolves the host, checks every resolved address against the policy and dials the checked address with the original `Host` (rebinding-safe), repeats the check on each redirect hop, strips `Authorization` on a cross-host redirect, enforces scheme, size and time, and routes through `WithProxy(url)` when set while still validating hops. A refusal returns `EgressDenied{URL, Reason}` (`Permanent`) to the model, appends an audit record `egress_denied` with the run id, and increments `gohan.egress.requests{tool, host, decision}`. Every gohan-shipped HTTP tool (`std/tool/http.Fetch`, the blob URL fetch in `messages`) uses it; user tools receive it from `NewTool` through `EgressClient(ctx)`. MCP client transports and provider adapters are configuration, not model input, and are exempt; `Explain` lists their hosts beside the tool policies.

#### Scenario: egress policy required
ID: `tools.egress-policy-required`
- WHEN a tool is registered with `WithExfil()` and no `WithEgress`
- THEN `Build` fails with `ErrEgressPolicyRequired` naming the tool

#### Scenario: metadata endpoint blocked
ID: `tools.egress-metadata-blocked`
- WHEN the model calls `web_fetch` with `http://169.254.169.254/latest/meta-data/`
- THEN the tool returns `EgressDenied{Reason: "link-local"}`, no connection is opened and `gohan.egress.requests{decision=denied}` increments

#### Scenario: redirect re-validated
ID: `tools.egress-redirect-revalidated`
- WHEN an allowed host answers with a 302 to `http://10.0.0.5/`
- THEN the redirect is not followed, the result is `EgressDenied{Reason: "private range"}` and the first response body is not returned

#### Scenario: rebinding safe
ID: `tools.egress-rebinding-safe`
- WHEN an allowed hostname resolves to a public address at check time and to `127.0.0.1` on a second resolution
- THEN the connection is made to the checked public address and no request reaches loopback

#### Scenario: size limit
ID: `tools.egress-size-limit`
- WHEN a response exceeds `MaxBytes`
- THEN the body is cut at the limit, the result is `Failed(Permanent)` naming the limit, and the connection is closed

#### Scenario: egress audited
ID: `tools.egress-audited`
- WHEN a request is denied
- THEN the audit record names the tool, the run id, the host and the reason, and never the request body


### Requirement: Names and identity

A tool name matches `^[a-z][a-z0-9_]{0,63}$`; `NewTool` and `Build` reject anything else with `ErrToolName`, and no adapter ever rewrites a name — a provider that cannot accept a valid name fails `Build` for that profile. Two tools with the same name in one flow (`Deferred`, provider-executed, sandbox, skills and imported sets included) fail `Build` with `ErrToolCollision{Name, Sources}`. The names `search_tools`, `read_output`, `notes_read`, `notes_write`, `memory_read`, `memory_write`, `load_skill` and `read_skill_resource` are reserved for the harness. `ToolSpec.Name` is the identity used by the pinned manifest, session grants, `CallKey`, taint policy, audit and `approve:<name>` scopes: a renamed tool is a new tool and nothing migrates. Imported sets are namespaced at wiring (`interop`) as `<ns>__<tool>`.

A `Tool` value is built once, before `Build`, and shared by every run: its dependencies are constructor arguments captured by `fn` (`tools.Search(repo)`), tenant-scoped resources are chosen inside `fn` from `PrincipalFrom(ctx)`, and `Call` must be safe for concurrent use; nothing per-request is stored on the value.

#### Scenario: name grammar
ID: `tools.name-grammar`
- WHEN `NewTool("orders.get", …)` or `NewTool("Search", …)` is called
- THEN `ErrToolName` is returned and `Build` never sees the tool

#### Scenario: collision fails build
ID: `tools.collision-fails-build`
- WHEN a flow registers a local `search` and a `Deferred` `search`
- THEN `Build` fails with `ErrToolCollision{Name: "search"}` naming both sources

#### Scenario: reserved names
ID: `tools.reserved-names`
- WHEN a user registers a tool named `read_output`
- THEN `Build` fails with `ErrToolCollision` naming the harness as the other source

#### Scenario: rename is a new tool
ID: `tools.rename-is-new-tool`
- WHEN a tool pinned in the manifest is renamed
- THEN `Build` reports the old name missing and the new one unpinned, and existing session grants for the old name never match the new one

#### Scenario: tool value shared and concurrent
ID: `tools.tool-value-shared-and-concurrent`
- WHEN two runs call the same `Tool` value at once for different tenants
- THEN each call reads its own principal from `ctx` and neither observes the other's arguments or results


### Requirement: Tool contract

#### Scenario: unknown tool
ID: `tools.unknown-tool`
- WHEN the model calls `create_priority_ticket`, which is not registered
- THEN the model receives a `Failed(Permanent)` result naming the unknown tool, `gohan.tool.unknown` increments, and no tool executes

#### Scenario: args validated at completion
ID: `tools.args-validated-at-completion`
- WHEN argument fragments were previewed and the complete arguments contain a duplicate key
- THEN validation fails once at completion, the call is `Failed(Permanent)`, the client receives `ToolFinished` with the failure, and the tool never runs

#### Scenario: invalid args on raw Tool
ID: `tools.invalid-args-on-raw-tool`
- WHEN a hand-written `Tool` receives args violating its `Schema`
- THEN the chain rejects them before `Call` and the model receives a `Failed(Permanent)` result with the validation error

#### Scenario: classified error
ID: `tools.classified-error`
- WHEN a tool returns `gohan.Retryable(err)`
- THEN the model-visible result has `Error.Kind: Retryable`

#### Scenario: large output stored
ID: `tools.large-output-stored`
- WHEN a tool returns 200 KiB and `MaxOutput` is 64 KiB
- THEN the inline result contains a head excerpt and a `Ref`, `read_output(ref)` returns the full content, and `gohan.output.stored` increments

#### Scenario: notes survive reset
ID: `tools.notes-survive-reset`
- WHEN the agent writes to `notes_write` and the run ends; a new run starts in the same session with truncated history
- THEN the first model request of the new run contains the notes in `SlotSession`


### Requirement: Tool author contract

#### Scenario: schema from tags
ID: `tools.schema-from-tags`
- WHEN `In` has `Currency string \`json:"currency" desc:"ISO code" enum:"EUR,USD"\`` and `Nights int \`json:"nights,omitempty" min:"1" max:"30"\``
- THEN the derived schema has `currency` required with the enum and description, `nights` optional with the bounds, and `additionalProperties: false`

#### Scenario: untyped args rejected
ID: `tools.untyped-args-rejected`
- WHEN `In` contains a `map[string]any` field and the tool is not built with `WithRawArgs()`
- THEN `Build` fails naming the tool and the field

#### Scenario: out passthrough
ID: `tools.out-passthrough`
- WHEN `fn` returns `[]Block{Image{...}, Text{...}}`
- THEN the tool result carries those blocks unchanged and no JSON wrapping occurs

#### Scenario: panic recovered
ID: `tools.panic-recovered`
- WHEN `fn` of a `SideEffect` tool panics after starting an external call
- THEN the run continues with `Outcome: Unknown`, the stack is in the audit record only, and `gohan.tool.panic` increments

#### Scenario: default timeout by effect
ID: `tools.default-timeout`
- WHEN a `ReadOnly` tool without `WithTimeout` blocks for 11 s
- THEN it is cancelled at 10 s and the result is `Failed(Retryable)`


### Requirement: Tool spec pinning

#### Scenario: poisoned description
ID: `tools.poisoned-description`
- WHEN an imported tool's description contains an instruction directive
- THEN `Build` fails with `ErrToolDescription` naming the tool

#### Scenario: rug pull
ID: `tools.rug-pull`
- WHEN the pinned manifest was produced with tool `search` at hash H and the imported tool now yields hash H'
- THEN `Build` fails with `ErrManifestDrift{Tool: "search"}`

#### Scenario: untrusted effect cap
ID: `tools.untrusted-effect-cap`
- WHEN an imported tool declares `SideEffect` and no policy raises `MaxEffect`
- THEN the tool is registered as `ReadOnly`, `gohan.tool.effect_capped` increments, and the gate treats it accordingly


### Requirement: Dynamic tools and inspection

#### Scenario: tool filter per turn
ID: `tools.tool-filter-per-turn`
- WHEN `ToolFilter` hides `create_booking` on turn 1 and shows it on turn 2
- THEN turn 1's request omits its spec and turn 2's includes it; a turn-1 call to it is an unknown-tool error

#### Scenario: inspect from another pod
ID: `tools.inspect-from-another-pod`
- WHEN a run is in progress on pod A
- THEN `Inspect(runID)` on pod B returns its current turn, last `Seq`, pending calls and cost from stores


### Requirement: Deferred tools

#### Scenario: not assembled until discovered
ID: `tools.not-assembled-until-discovered`
- WHEN 40 tools are registered with `Deferred: true` and 4 always-loaded
- THEN the first request contains 5 definitions (4 + `search_tools`) and `Explain` reports their token cost

#### Scenario: activation persists across resume
ID: `tools.activation-persists-across-resume`
- WHEN the model discovers `render_voucher`, the run suspends and resumes via `Replay`
- THEN `render_voucher` is in the active set after resume; a mismatch fails with `ErrToolSetDrift`

#### Scenario: governed while deferred
ID: `tools.governed-while-deferred`
- WHEN the model calls a deferred tool without discovering it
- THEN the call is an unknown-tool error; WHEN it calls it after discovery without the required scope
- THEN the scope check denies it


### Requirement: tool/exec


### Requirement: Provider-executed tools

The implementation MUST carry the *Provider-executed tools* contract on the public path. A provider-executed tool MUST be registered through the ordinary tool registry as a spec with `Executor: ByProvider`, and its effect, trust and `Capabilities.Exfil` MUST be read from that registered spec and never from the provider's block. The harness MUST NOT invoke the tool's `Call` for a `ByProvider` spec; the provider runs it inside the model request. `Build` MUST already have refused a `ByProvider` spec whose name is absent from the profile's `Caps.ProviderTools` with `ErrProviderToolUnsupported` (`core/build.go:244-251`). The permission gate MUST run on the spec before the model request is assembled, so the gate decision is a declaration decision: a `Deny` MUST leave the tool out of the request, an `Ask` MUST use the provider approval flag when `Caps.ProviderTools` marks approval supported and MUST otherwise behave as `Deny`, and an `Allow` MUST declare the tool. An approval request from the provider MUST become a suspension carrying the pending call and the matched argument origins, not an inline wait. A provider tool result MUST carry the origin of the tool that produced it, `OriginProvider{Name}`, on every block it appends.

Tools the provider runs inside the model call (web search, code execution, hosted MCP) are registered like any other tool with `Executor: ByProvider`; `std/tool/provider` ships `WebSearch(opts...)`, `CodeExec()` and `HostedMCP(url, allowed ...string)`, all `Untrusted`, with effect `ReadOnly` for search and `SideEffect` for the other two, and `Exfil: true` for search and hosted MCP. They ship their own `ToolPolicy` raising `MaxEffect` to the declared effect, so the import cap does not apply to them; a wiring that lowers `MaxEffect` below the declared effect still caps it and increments `gohan.tool.effect_capped` (`tools.untrusted-effect-cap`). Rules:

1. **Enable path.** The adapter translates the spec into the provider's tool declaration (search `max_uses` from the run's remaining `MaxToolCalls`, domain allow/deny from `WithDomains`, hosted-MCP `allowed_tools` from `allowed`). A profile whose `Caps.ProviderTools` does not list the tool's kind fails `Build` with `gohan.tool.provider_unsupported`.
2. **Gate before the call.** Because the call happens inside the model request, the permission gate runs on the spec before the request is sent: `Deny` → the tool is not declared; `Ask` → declared with the provider's approval flag (`require_approval: always`) when `Caps.ProviderTools` marks approval as supported, otherwise treated as `Deny`; `Allow` → declared. A provider approval request maps to `HumanApproval` with an `ApprovalRequest` built from the pending call; `Resume(Approve())` replays the model call carrying the approval item under the same `CallKey`.
3. **Post-hoc taint.** The model's inputs to a provider tool are only visible after the fact. `std/taint` runs rule 1 of `taint` on them when the response arrives; a tainted input into an `Exfil` provider tool aborts the run `Failed(Permanent)` before any result block is appended or used, and `gohan.taint.post_hoc{tool}` increments.
4. **Provenance.** Adapters convert the provider's call block to a `ToolUse`; the executor is the `Executor: ByProvider` of the registered spec with that name, not a block field, and its result block to a `ToolResult` whose blocks carry `OriginProvider{Name}`; opaque payloads the provider requires back (encrypted search content, citations) stay inside the result as `Raw` and round-trip per `model` rule 8. Fencing, guards and taint apply unchanged.
5. **Cost, limits, journal.** Each provider call counts toward `MaxToolCalls`, is journaled `Completed` directly (never `Reserved`; it cannot be replayed or verified), is audited with `Executor`, and is priced from `Pricing.ProviderCall[name]` using `Usage.ProviderToolCalls`.
6. **Trifecta.** A provider tool with `Exfil` is both the exfiltration leg and the untrusted-content leg of the `taint` build check.

#### Scenario: provider tool registered
ID: `tools.provider-tool-registered`
- WHEN a flow registers `provider.WebSearch()` and the profile lists web search in `Caps.ProviderTools`
- THEN the model request declares the provider's search tool and `Explain` lists it with `Executor: ByProvider`

#### Scenario: gate decides before the call
ID: `tools.provider-tool-gate-before-call`
- WHEN the tool policy denies `web_search` for the principal
- THEN the model request carries no search tool and no provider search occurs

#### Scenario: provider approval as suspension
ID: `tools.provider-approval-as-suspension`
- WHEN a hosted-MCP tool is gated `Ask` and the provider returns an approval request
- THEN the run suspends with `HumanApproval` naming the pending call, and `Resume(Approve())` replays the model call with the approval under the original `CallKey`

#### Scenario: provider result carries origin
ID: `tools.provider-result-origin`
- WHEN a provider search returns result blocks
- THEN they are appended as a `ToolResult` with `OriginProvider{"web_search"}` and the encrypted payload round-trips as `Raw` on the next call


### Requirement: Verification

#### Scenario: unknown reconciled
ID: `tools.verify-reconciles-unknown`
- WHEN a `SideEffect` call times out with `Outcome: Unknown` and the tool declares `Verify` that finds the booking exists
- THEN the entry's recorded result becomes `Outcome: Succeeded`, the model receives the verified result, and `Done.Uncertain` is empty

#### Scenario: verify on recover
ID: `tools.verify-on-recover`
- WHEN a run is reclaimed after a crash with two `Unknown` entries, one with `Verify`
- THEN the one with `Verify` is reconciled before replay continues and the other stays in `Uncertain`

#### Scenario: verify never re-executes
ID: `tools.verify-read-only`
- WHEN `Verify` runs
- THEN no `Call` is issued for the tool and the journal shows a `/verify` entry

#### Scenario: verify error keeps uncertainty
ID: `tools.verify-error`
- WHEN `Verify` returns an error
- THEN the entry stays `Unknown` and `*UncertainOutcomeError` is returned

#### Scenario: timeout kills group
ID: `tools.timeout-kills-group`
- WHEN a command spawns children and exceeds its timeout
- THEN the whole process group is killed and the result is an error result

#### Scenario: output cap
ID: `tools.output-cap`
- WHEN stdout exceeds the cap
- THEN the result is truncated with a marker

#### Scenario: env allowlist
ID: `tools.env-allowlist`
- WHEN the parent has `SECRET=x` and the allowlist is empty
- THEN the child environment does not contain `SECRET`


### Requirement: Fetched response cap and store

A tool that returns a fetched response MUST bound its inline content by `ToolSpec.MaxOutput` and MUST spill the rendered response to a wired `OutputStore`, returning a head excerpt beside a `Ref`. `std/tool/http.Fetch` MUST honour this. A response of at most `MaxOutput` bytes MAY stay whole inline with no `Ref`. A larger response MUST NOT be returned whole inline, and the head excerpt MUST be at most `MaxOutput` bytes plus the truncation marker.

#### Scenario: web_fetch overflow stored
ID: `tools.fetch-output-stored`
- WHEN `web_fetch` receives a response larger than `MaxOutput` with an output store wired
- THEN the result carries a head excerpt plus a `Ref`, `read_output(ref)` returns the full rendered response, and `gohan.output.stored` increments

#### Scenario: web_fetch capped without a store
ID: `tools.fetch-output-no-store`
- WHEN `web_fetch` receives a response larger than `MaxOutput` and no store is wired
- THEN the result carries a bounded head excerpt and a marker naming the dropped bytes and the missing store


### Requirement: Fetched rendered text includes the status line

The rendered response MUST be the HTTP status line followed by the body. The status prefix MUST count against `MaxOutput` and MUST be part of the stored document, so the prefix cannot push inline content past the cap.

#### Scenario: web_fetch call shape stays compatible
ID: `tools.fetch-options-compatible`
- WHEN existing code calls `http.Fetch(policy)` with one argument and a response whose status line plus body fits `MaxOutput`
- THEN the tool returns the whole fetched rendered text with no `Ref`, and no store option is required


### Requirement: Egress response lifetime

The egress client MUST bound the whole response, not the request only. Its time limit MUST stay in force until the body ends. The client MUST release the per-request transport and cancel its time limit exactly once. The release MUST NOT depend on the caller closing the body. A release MUST be safe to run twice. On a terminal read error the client MUST return the read bytes and the original error unchanged. Cleanup MUST NOT change the checked-address dial, proxy validation, or the redirect rules.

#### Scenario: body read bounded by the policy time limit
ID: `tools.egress-body-read-bounded`
- WHEN an allowed host sends response headers and then stalls, and the caller reads the body
- THEN the read stops at the policy time limit with a permanent failure rather than hanging, the transport is released before any caller close, and the returned bytes and error are the ones the underlying body produced

#### Scenario: early request error releases the transport
ID: `tools.egress-early-error-cleanup`
- WHEN a request is refused before a response exists (denied address, disallowed scheme, or unsupported proxy)
- THEN no idle connection survives the refusal

#### Scenario: size-limit refusal releases the transport
ID: `tools.egress-oversize-cleanup`
- WHEN a response exceeds `MaxBytes`
- THEN the body is closed, the size-limit refusal is returned, and no idle connection survives it

#### Scenario: release is idempotent
ID: `tools.egress-close-idempotent`
- WHEN the caller closes the body twice, or closes it after reaching end of file
- THEN the transport is released exactly once and the client does not panic

#### Scenario: cleanup preserves the request checks
ID: `tools.egress-cleanup-preserves-checks`
- WHEN a response ends normally and the transport is released
- THEN the dial still uses the checked address, and the rebinding, proxy and redirect rules are unchanged

### Requirement: Egress connection ownership

The egress client MUST NOT reuse or pool a connection before a measurement shows that reuse keeps the checked-address dial, the proxy validation, and the redirect rules intact. Until then, each request MUST own its transport. The client MUST close that transport's idle connections when the response ends.

#### Scenario: per-request transport is released
ID: `tools.egress-per-request-transport-released`
- WHEN two requests run in sequence over the same client
- THEN each request owns its transport and no idle connection survives the first response

