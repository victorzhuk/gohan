# Model and profiles

Capability: `model` · Spec v1.5 (ADR-0130) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `model` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.5 Model and ModelProfile

```go
type Model interface {
	Profile() ModelProfile
	Generate(ctx context.Context, req ModelRequest) iter.Seq2[ModelChunk, error]
}

type ModelRequest struct {
	System   []Block
	Tools    []ToolSpec
	Messages []Message
	Options  ModelOptions
	Metadata RequestMetadata
}

type RequestMetadata struct {
	CostTags CostTags
}

type ModelOptions struct {
	MaxTokens      int
	Temperature    *float64
	ToolChoice     ToolChoice
	ResponseSchema json.RawMessage
	Priority       int
	AffinityKey    string
	Extra          map[string]any
}

type DeltaKind int

const (
	DeltaText DeltaKind = iota
	DeltaReasoning
	DeltaToolArgs
)

type FinishReason string

const (
	FinishStop      FinishReason = "stop"
	FinishToolUse   FinishReason = "tool_use"
	FinishMaxTokens FinishReason = "max_tokens"
	FinishRefusal   FinishReason = "refusal"
	FinishError     FinishReason = "error"
)

type ToolChoice string

const (
	ToolChoiceAuto ToolChoice = "auto"
	ToolChoiceNone ToolChoice = "none"
	ToolChoiceAny  ToolChoice = "any"
)

type AffinityKeyStrategy int

const (
	AffinityNone AffinityKeyStrategy = iota
	AffinitySessionHash
	AffinityTenantHash
)

type ModelChunk struct {
	Kind    DeltaKind
	Delta   string
	ToolUse *ToolUse
	Usage   *Usage
	Finish  FinishReason
}

type Usage struct {
	InputTokens       int
	CachedInputTokens int
	OutputTokens      int
	CacheWriteTokens  int
	SandboxSeconds    float64
	ProviderToolCalls map[string]int
	KeyID             string
	ModelVersion      string
	Estimated         bool
}

type ModelProfile struct {
	Name          string
	Version       string
	Sunset        time.Time
	Successor     string
	Region        string
	Endpoint      string
	QuotaPool     string
	Caps          Caps
	ContextWindow int
	Pricing       Pricing
	MaxInFlight   int
	Affinity      AffinityKeyStrategy
	LatencyClass  LatencyClass
	Timeout       ModelTimeout
	Keys          KeyMode
}

type KeyMode int

const (
	PlatformKey KeyMode = iota
	TenantKey
	TenantOrPlatformKey
)

type ProviderCredential struct {
	ID        string
	Token     string
	ExpiresAt time.Time
}

type ProviderKeySource interface {
	ProviderKey(ctx context.Context, profile, tenant string) (ProviderCredential, error)
}

type ProviderKeyValidator interface {
	Validate(ctx context.Context, profile string, c ProviderCredential) error
}

var ErrNoProviderKey = errors.New("gohan: no provider key for this tenant and profile")

type TokenBudget struct {
	Limit     int
	Reserved  int
	Estimated int
	Ratio     float64
}

type TokenEstimator interface {
	Estimate(req ModelRequest, caps Caps) int
}

type TokenCounter interface {
	Count(ctx context.Context, req ModelRequest) (int, error)
}

func ContextBudget(p ModelProfile, req ModelRequest, est TokenEstimator, ratio float64) TokenBudget

var ErrNoTokenEstimator = errors.New("gohan: no token estimator configured")

type ModelTimeout struct {
	Connect    time.Duration
	FirstChunk time.Duration
	Idle       time.Duration
}

type Caps struct {
	Tools         bool
	ParallelTools bool
	Constrained   bool
	Images        bool
	Streaming     bool
	Temperature   bool
	StrictVersion bool
	Cache         CacheMode
	CacheBreakpoints int
	Compaction    CompactionMode
	ReasoningVisible bool
	Fidelity      map[BlockKind]Fidelity
	ProviderTools map[string]ProviderToolCap
	Blobs         BlobCaps
	StreamsToolArgs bool
}

type BlobCaps struct {
	MaxBytes      int64
	MaxPerRequest int
	MaxPixels     int
	Formats       []string
}

type BlobUploader interface {
	Upload(ctx context.Context, b Blob, mime string) (string, error)
}

type ProviderToolCap struct {
	Approval bool
}

type ErrorClass int

const (
	ClassRateLimited ErrorClass = iota + 1
	ClassTransient
	ClassContextOverflow
	ClassContentPolicy
	ClassDeprecated
	ClassAuth
	ClassPermanent
	ClassVersionDrift
)

type ModelError struct {
	Class      ErrorClass
	Provider   string
	Status     int
	Code       string
	RetryAfter time.Duration
	Err        error
}

func (e *ModelError) Error() string
func (e *ModelError) Unwrap() error
func (e *ModelError) Retryable() bool

type CompactionMode int

const (
	CompactionNone CompactionMode = iota
	CompactionTextCap
	CompactionOpaqueCap
)

type Pricing struct {
	Input              float64
	CachedInput        float64
	CacheWrite         float64
	Output             float64
	BatchDiscount      float64
	SocializeCacheWrites bool
	SandboxSecond      float64
	ProviderCall       map[string]float64
}

type CacheMode int

const (
	CacheNone CacheMode = iota
	CacheAuto
	CacheExplicit
)

type LatencyClass int

const (
	Interactive LatencyClass = iota
	Agentic
	Batch
)
```

Text and reasoning stream as typed deltas; `ToolUse` blocks are emitted complete. `Extra` is passed verbatim to the provider (e.g. vLLM `extra_body`), so new engine features need no gohan release.

`QuotaPool` names the shared provider account (organization key) behind the endpoint. All limiting is keyed by pool, and `adapter/redis` shares the bucket across every service that uses the same pool name, sized to the real organization quota. Admission control by class: `Interactive` requests get a reserved share of the pool; `Batch` requests are admitted only while pool queue-wait is under a threshold and are deferred as it rises; `Agentic` sits between. Retry backoff is jittered by policy.

`Budget` has scopes: `run` and `tenant` (hard abort), `flow` (hard abort), `pool/day` (breaker across services, backed by the same store as the limiter). `gohan.cost.anomaly` fires when spend rate exceeds a rolling baseline by a configurable factor.

`Sunset` is the provider's announced retirement date; `Successor` names the profile that replaces it. `Build` fails (or warns with `SunsetWarnOnly()`) when `now > Sunset − NoticeWindow` (default 30 days) and validates every flow's `ModelOptions` against the successor's `Caps` so the migration is a profile edit gated by evals, not a discovery on shutdown day. The manifest lists pinned versions with their sunsets for a CI check. Providers normalize deprecation headers and errors into `ModelError{Class: Deprecated}`, which routes to `Successor` when declared and otherwise surfaces; it is never rendered as a fallback message.

`Pricing` is per token; `SocializeCacheWrites` folds cache-write cost into an overhead rate on cached reads instead of charging the run that repopulated the cache; `BatchDiscount` is applied pro rata per item by token share when a batch completes.

### Provider tool usage accounting

The model capability owns provider tool usage accounting: an adapter MUST count each provider-executed tool call it observes in a response under the tool's name in `Usage.ProviderToolCalls`, and the run's cost MUST include that count priced at `Pricing.ProviderCall[name]`.

### Provider tool block conversion

A provider tool block MUST NOT be treated as `Raw`: the adapter MUST convert it per `tools` *Provider-executed tools* rule 4, while payloads the provider requires back stay inside the result as `Raw` and round-trip per rule 8 below.

### Request cost tags

`ModelRequest` MUST carry a `Metadata RequestMetadata` field, and `RequestMetadata` MUST carry `CostTags CostTags` — the run's `RunInfo.CostTags` (`core/types/identity.go:42,80`). The provider request assembly MUST populate `ModelRequest.Metadata.CostTags` from the run; the prefix-cache key builder already reads the same three values (`std/assembly.go:92-94`).

### Provider request metadata

An adapter whose provider documents a request-metadata channel MUST map `Metadata.CostTags` onto that channel in the provider's documented form. An adapter whose provider has no such channel MUST leave the values unsent, MUST NOT fail the call, and MUST NOT synthesise a channel.

### Cost tag confidentiality

The values are run attributes, never model content: no adapter, telemetry sink or log record MUST emit them as content — they MUST NOT appear in span content capture or in any log record.

### Cost tag attribution

`Metadata.CostTags` is not `Extra`: `Extra` is a flow's verbatim pass-through (this spec, *Pass-through*), while `Metadata` is harness-assembled run attribution. `adapter/openai` and `adapter/anthropic` are the implementations of this field; each names the concrete Send function that performs the mapping when it is authored.

### Adapter metadata conformance

These obligations bind the adapter call path; the shared conformance suite that exercises them is owned by `m1-provider-conformance-contract` and run by `m1-openai-provider` and `m1-anthropic-provider`.

### Provider adapter contract

Every `Model` implementation (`adapter/openai`, `adapter/anthropic`, gateways, `gohantest.ScriptedModel`) SHALL:

1. **Errors.** Return every failure as `*ModelError`. Mapping: HTTP 429 and provider quota codes → `ClassRateLimited` with `RetryAfter` parsed from `Retry-After` / `x-ratelimit-reset-*` when present; 5xx, connection errors, `Connect`/`FirstChunk` timeouts → `ClassTransient`; context-length errors → `ClassContextOverflow`; safety refusals at the request level → `ClassContentPolicy`; model-retired/not-found → `ClassDeprecated`; 401/403 → `ClassAuth`; 400 and everything else → `ClassPermanent`; a `Usage.ModelVersion` that differs from `Profile.Version` on a profile with `Caps.StrictVersion` → `ClassVersionDrift`. `RetryAfter` rides on the error so the transport can send `Retry-After`; the chain never retries `ClassRateLimited` on the same endpoint, so `retry.RetryAfter` does not apply to it (`chains`).
2. **Fidelity.** Declare `Caps.Fidelity` for every `BlockKind` the adapter may receive; `KindText`, `KindToolUse`, `KindToolResult` default to `Preserved`, all other kinds must be declared explicitly. `Build` fails when a flow can produce a kind the profile leaves undeclared. `Degraded` conversions are deterministic and documented per adapter.
3. **Cache.** `CacheNone`: `CacheBreak` blocks are dropped. `CacheAuto`: provider-side prefix caching, `CacheBreak` dropped, `Usage.CachedInputTokens` filled from the provider's usage. `CacheExplicit`: the first `Caps.CacheBreakpoints` `CacheBreak`s map to provider cache markers in order, later ones are dropped with `gohan.cache.breakpoints_dropped`; cache-write tokens are reported in `Usage.CacheWriteTokens`.
4. **Structured output.** `ResponseSchema` on a profile without `Caps.Constrained` is a `ClassPermanent` `ModelError` at call time; adapters never silently ignore a schema. `std/structured.ToolSchema` is the app-side fallback the strategy resolver picks at `Build`.
5. **Usage.** Fill `Usage` on the final chunk: input, cached input, cache write, output tokens, `ModelVersion` as served; zero is never reported when the provider gives no usage — the adapter sets `Usage.Estimated = true` and a tokenizer estimate.
6. **Streaming.** Emit typed deltas in provider order; `ToolUse` blocks complete; a `max_tokens` finish marks any open `ToolUse` as truncated (`structured-output`); honour `Timeout` per ADR-0104; stop promptly on ctx cancellation (iterator contract below).
7. **Pass-through.** `Extra` keys that collide with governed request fields (`model`, `messages`, `tools`, `tool_choice`, `max_tokens`, `temperature`, `response_format`/`output_schema`, `stream`) fail `Build` when static and the call (`ClassPermanent`) when dynamic. `AffinityKey` and `Priority` map to documented headers/parameters or are ignored with a `Build` warning.
8. **Raw.** Provider blocks gohan does not model round-trip as `Raw{Provider, Value}` to the same provider and are `Dropped` elsewhere. Provider-executed tool blocks are not `Raw`: they are converted per `tools` *Provider-executed tools* and `Usage.ProviderToolCalls` counts them by tool name.
9. Pass `conformance.Model(t, newModel, fixtures)` (`docs/design/testing.md`).

**Provider keys.** Adapters obtain the credential for every call from the stack's `ProviderKeySource` (`agent.WithProviderKeys(src)`; `std/keys` ships env- and map-backed sources) with the tenant taken from `PrincipalFrom(ctx)`, never from an argument. `Keys` per profile: `PlatformKey` (default) asks with an empty tenant; `TenantKey` asks with the tenant and fails closed with `ErrNoProviderKey` when the source has none; `TenantOrPlatformKey` falls back to the platform key only when the source returns `ErrNoProviderKey`, never on a provider `ClassAuth` — a revoked or rejected tenant key surfaces as `gohan.provider_key_rejected` with the key id. Breaker, limiter, `MaxInFlight`, quota pool and hedge budget are keyed by `(profile, ProviderCredential.ID)` so one tenant's exhausted account isolates to that tenant. `ProviderCredential.Token` is never persisted, logged, exported to spans, audit, `Explain`, cassettes (recorded with authorization headers stripped) or error details; `ID` is an opaque handle recorded as `gohan.model.key_id` and `Usage.KeyID` for billing attribution. Sources may cache by `ExpiresAt`; `Build` calls `Validate` (optional `ProviderKeyValidator`) for platform keys and fails on rejection; tenant keys are validated by the onboarding flow with the same method.

**Token budget.** `ContextBudget` computes `Limit = ContextWindow − MaxTokens − Margin` (`Margin` = 2 % of `ContextWindow`, at least 512) and `Estimated = est.Estimate(req, caps) × ratio`. `TokenEstimator` is a port whose default is `std/tokens.Heuristic`: per block, text `bytes/4`, code and JSON `bytes/3`, text with more than a quarter non-ASCII bytes `bytes/2`, images by the provider formula in `Caps`, each tool schema once; it is deterministic for identical input. `Build` fails with `ErrNoTokenEstimator` when no estimator is configured (the `std` presets install the heuristic). `TokenCounter` is an optional interface on `Model` backed by a provider count endpoint; when present the harness uses it only for the compaction decision (`context`), never per request. Every rule in `assembly` and `context` that mentions tokens reads `ContextBudget`.

`Timeout.Idle` is measured on the provider read into the stream buffer, not on the consumer (`streams` *Slow consumers*). `Timeout` defaults from `LatencyClass` when zero: `Interactive` 5 s / 10 s / 15 s, `Agentic` 10 s / 30 s / 60 s, `Batch` 30 s / 120 s / 300 s (connect / first chunk / idle between chunks). A `Connect` or `FirstChunk` expiry is a `Transient` error (retry and fallback apply); an `Idle` expiry after the first chunk is `Permanent` for that call. **Mid-stream failure** (any error after the first chunk, including `Idle` expiry): the partial assistant message is appended to `SessionLog` with `Finish: FinishError`; deltas already streamed are not retracted; the run ends with `StopFailed`, and no `Done` is emitted for a failed run: the failure travels as the stream's terminal error tuple and, durably, as a recorded `TerminalError` payload that `Attach` treats as terminal; `Replay` treats a `FinishError` message as terminal and never re-sends it to a provider; the next user turn starts fresh from that history.

`Version` pins the exact model the profile was validated against. Providers report the served version in `Usage.ModelVersion`; a mismatch increments `gohan.model.version_drift` and, with `Caps.StrictVersion`, fails the call as `ClassVersionDrift`, which is never retried on that endpoint and fails over to the next one (`chains`); when every endpoint drifts the error surfaces. Fallback targets are validated at build for `Caps` compatibility with every flow that can reach them (tools, constrained output, images, streaming) and get their own `ContextPolicy` (projections, then compaction) before the request is sent.

**Iterator contract** (normative for every `iter.Seq2` returned by gohan or an adapter): the iterator body runs on the caller's goroutine; any helper goroutine it starts terminates before the iterator returns; a public run is the one exception, because the run protections require the run to progress while its consumer has stopped taking events - such a run is driven by one harness-owned run worker and the iterator the caller holds reads recorded events (see `streams`), while provider helpers started inside the run stay joined to their own iterators; it returns within one network read of `ctx.Done()`; when `yield` returns false it releases all resources (HTTP body, stream reader) before returning; it never yields after returning an error. `conformance.Model` verifies early break and cancellation with a goroutine-leak check. Error tuples follow the protocol in `streams`. Consumers that need pull semantics use `iter.Pull` and must call `stop`; core and `std` never do so on the request path.


## Requirements

### Requirement: Provider adapter contract

The implementation MUST carry the *Provider adapter contract* on the public adapter path.

#### Scenario: status maps to class
ID: `model.status-to-class`
- WHEN the conformance fixtures replay 429, 500, 401, 400, a context-length error and a retired-model error
- THEN the adapter returns `*ModelError` with `ClassRateLimited`, `ClassTransient`, `ClassAuth`, `ClassPermanent`, `ClassContextOverflow`, `ClassDeprecated` respectively

#### Scenario: retry-after parsed
ID: `model.retry-after-parsed`
- WHEN a 429 carries `Retry-After: 7`
- THEN `ModelError.RetryAfter == 7s`; `ClassRateLimited` is never retried on the same endpoint, so the request fails over at once and, when every endpoint is exhausted, the value is returned to the client as `Retry-After`

#### Scenario: undeclared fidelity fails build
ID: `model.undeclared-fidelity`
- WHEN a flow's tools can return `KindFile` and the profile's `Caps.Fidelity` has no entry for it
- THEN `Build` fails naming the profile and the kind

#### Scenario: explicit cache breakpoints
ID: `model.cache-explicit-breakpoints`
- WHEN a request carries three `CacheBreak`s on a profile with `CacheExplicit` and `CacheBreakpoints: 2`
- THEN two provider markers are sent in order, one break is dropped, and `Usage.CacheWriteTokens` is filled from the response

#### Scenario: schema never ignored
ID: `model.schema-never-ignored`
- WHEN a `ResponseSchema` reaches an adapter whose profile lacks `Caps.Constrained`
- THEN the call fails with `ClassPermanent` before any request is sent

#### Scenario: usage estimated when absent
ID: `model.usage-estimated`
- WHEN a provider response carries no usage block
- THEN `Usage.Estimated` is true and token counts are non-zero estimates

#### Scenario: extra cannot override governed fields
ID: `model.extra-collision`
- WHEN `Extra` contains `max_tokens`
- THEN `Build` fails for a static value and the call returns `ClassPermanent` for a dynamic one

#### Scenario: raw round-trip
ID: `model.raw-round-trip`
- WHEN a provider returns a block kind gohan does not model
- THEN it is stored as `Raw{Provider}`, re-sent unchanged to the same provider and dropped for another with `gohan.block.dropped{kind=raw}`

#### Scenario: provider tool usage
ID: `model.provider-tool-usage`
- WHEN a response contains two provider search calls
- THEN `Usage.ProviderToolCalls["web_search"]` is 2 and the run's cost includes `2 × Pricing.ProviderCall["web_search"]`

#### Scenario: cost tags forwarded as request metadata
ID: `model.cost-tags-forwarded-as-metadata`
- WHEN a run with `CostTags{Feature: "assist", Environment: "prod"}` is assembled into a `ModelRequest` and sent through an adapter whose provider documents a request-metadata channel
- THEN `ModelRequest.Metadata.CostTags` carries the run's three tags, the adapter sends them on that channel, and no log record or span content captures them

#### Scenario: provider without a metadata channel
ID: `model.provider-no-metadata-channel`
- WHEN the same request is sent through an adapter whose provider has no request-metadata channel
- THEN the call succeeds with the tags unsent, no error is raised, and no field is invented

### Requirement: Shared provider conformance contract

The implementation MUST provide deterministic shared conformance inputs, and each adapter MUST run the shared suite through its public model call path against recorded responses. Build MUST reject an unsupported output fidelity combination and a static governed-field collision, and a dynamic collision MUST return a permanent model error before the request is sent. Adapters MUST NOT ignore `ResponseSchema` without `Caps.Constrained`.

#### Scenario: shared suite coverage
ID: `model.shared-suite-coverage`
- WHEN the shared suite runs against an adapter's recorded responses
- THEN it checks error class mapping, Retry-After preservation, cache breakpoint order and limit, constrained-schema refusal before sending, usage estimation, Raw provider fidelity, per-key isolation and key redaction, and a reference double that violates an assertion fails the suite

### Requirement: Provider keys

Adapters MUST obtain credentials for every call from `ProviderKeySource` according to the profile's `Keys` mode. `PlatformKey` MUST use an empty tenant. `TenantKey` MUST use the tenant from `PrincipalFrom(ctx)` and fail closed on `ErrNoProviderKey`. `TenantOrPlatformKey` MUST use that tenant and fall back to the platform key only on `ErrNoProviderKey`, never on `ClassAuth`.

#### Scenario: tenant key selected
ID: `model.tenant-key-selected`
- WHEN a profile has `Keys: TenantKey` and the principal's tenant is `t1`
- THEN the adapter calls `ProviderKey(ctx, profile, "t1")` and sends that token, and `Usage.KeyID` is the credential's id

#### Scenario: missing tenant key fails closed
ID: `model.tenant-key-missing-fails-closed`
- WHEN `TenantKey` and the source returns `ErrNoProviderKey`
- THEN the call fails `Permanent` with `gohan.provider_key_missing`, no request reaches the provider and no platform key is used

#### Scenario: no fallback on auth error
ID: `model.no-fallback-on-auth-error`
- WHEN `TenantOrPlatformKey` and the tenant key is rejected with `ClassAuth`
- THEN the call fails with `gohan.provider_key_rejected` naming the key id and the platform key is not tried

#### Scenario: isolation per key
ID: `model.isolation-per-key`
- WHEN tenant `t1`'s key is rate-limited into an open breaker
- THEN calls for tenant `t2` on the same profile are unaffected

#### Scenario: key never in telemetry
ID: `model.key-never-in-telemetry`
- WHEN a call with a tenant key is traced, audited, recorded to a cassette and fails with a provider error
- THEN none of the outputs contains the token; the span carries `gohan.model.key_id` only

#### Scenario: key validated at build
ID: `model.key-validated-at-build`
- WHEN a platform key is invalid and the adapter implements `ProviderKeyValidator`
- THEN `Build` fails naming the profile

### Requirement: Token budget

`ContextBudget` MUST compute `Limit = ContextWindow - MaxTokens - Margin`, with `Margin` equal to 2% of `ContextWindow` and at least 512. It MUST compute `Estimated = est.Estimate(req, caps) × ratio`.

#### Scenario: budget reserves output
ID: `model.budget-reserves-output`
- WHEN `ContextWindow` is 200 000 and `MaxTokens` is 8 000
- THEN `TokenBudget.Limit` is 188 000 and a request estimated at 190 000 tokens is over budget

#### Scenario: heuristic deterministic
ID: `model.heuristic-deterministic`
- WHEN `std/tokens.Heuristic` estimates the same request twice on different pods
- THEN the estimates are identical

#### Scenario: token counter optional
ID: `model.token-counter-optional`
- WHEN a `Model` implements `TokenCounter`
- THEN it is called once at the compaction decision and never on the ordinary request path

### Requirement: Timeouts and mid-stream failure

`Connect` or `FirstChunk` expiry MUST produce a `Transient` error. An `Idle` expiry after the first chunk MUST be `Permanent` for that call. On mid-stream failure, the partial assistant message MUST be appended to `SessionLog` with `Finish: FinishError`, and the run MUST end with `StopFailed`.

#### Scenario: first-chunk timeout is transient
ID: `model.first-chunk-timeout-transient`
- WHEN a provider sends nothing for longer than `Timeout.FirstChunk`
- THEN the call is classified `Transient`, retried per policy and failed over if exhausted

#### Scenario: idle timeout after first chunk
ID: `model.idle-timeout-permanent`
- WHEN a stream stalls longer than `Timeout.Idle` after its first chunk
- THEN no retry or fallback occurs, the partial message is appended with `FinishError`, and the run ends `StopFailed`

#### Scenario: partial message is terminal on replay
ID: `model.partial-terminal-on-replay`
- WHEN a session whose last assistant message has `FinishError` is resumed or continued
- THEN that message is never re-sent to a provider and the next turn proceeds from the history

### Requirement: Failover and version pinning

`Version` MUST pin the exact model the profile was validated against. With `Caps.StrictVersion`, a served version mismatch MUST fail the call as `ClassVersionDrift`. Fallback targets MUST be validated at build for `Caps` compatibility with every flow that can reach them.

#### Scenario: 429 fails over without retry
ID: `model.429-fails-over-without-retry`
- WHEN the primary returns `RateLimited`
- THEN no retry is attempted on it; the request goes to the next endpoint; `gohan.model.failover{class=rate_limited}` increments

#### Scenario: 5xx retries then fails over
ID: `model.5xx-retries-then-fails-over`
- WHEN the primary returns `Transient` three times (policy max 2 retries)
- THEN two retries occur with backoff before failover

#### Scenario: incompatible fallback rejected at build
ID: `model.incompatible-fallback-rejected-at-build`
- WHEN a flow requires tools and its fallback profile has `Caps.Tools=false`
- THEN `Build` fails naming flow, fallback and capability

#### Scenario: context re-fit on fallback
ID: `model.context-re-fit-on-fallback`
- WHEN the fallback profile's `ContextWindow` is smaller than the assembled request
- THEN `ContextPolicy` (projections, then compaction) runs against the fallback profile before the request is sent

#### Scenario: breaker opens
ID: `model.breaker-opens`
- WHEN an endpoint fails `Transient` above the breaker threshold
- THEN subsequent requests skip it until the half-open probe succeeds, and the router sees it as unavailable

#### Scenario: version drift
ID: `model.version-drift`
- WHEN a provider reports `ModelVersion` different from `Profile.Version`
- THEN `gohan.model.version_drift` increments; with `StrictVersion` the call fails `ClassVersionDrift`, is not retried on that endpoint, and falls over; with no other endpoint left it surfaces

### Requirement: Tool argument deltas

An adapter MUST yield a `DeltaToolArgs` chunk per tool-call argument fragment and one complete `ToolUse` block at the end. An adapter that receives arguments whole MUST yield exactly one delta before the block.

#### Scenario: tool args delta kind
ID: `model.tool-args-delta-kind`
- WHEN a provider streams tool-call argument fragments
- THEN the adapter yields `ModelChunk{Kind: DeltaToolArgs, Delta: <fragment>, ToolUse: {ID, Name}}` per fragment and one complete `ToolUse` block at the end; an adapter that receives arguments whole yields exactly one delta before the block

### Requirement: Iterator contract

The iterator MUST release resources before returning when `yield` returns false. It MUST return within one network read of `ctx.Done()`. It MUST NOT yield after returning an error.

#### Scenario: early break releases
ID: `model.early-break-releases`
- WHEN a consumer breaks after the first chunk of `Model.Generate`
- THEN the HTTP body is closed and no goroutine remains (leak check passes)

#### Scenario: cancel returns promptly
ID: `model.cancel-returns-promptly`
- WHEN ctx is cancelled while the provider is silent
- THEN the iterator returns `context.Canceled` within the read deadline

### Requirement: Anthropic adapter wire mapping

The Anthropic adapter SHALL map the provider-neutral `ModelRequest` and `ModelChunk` constructs onto the Anthropic Messages API and back: `CacheBreak` blocks become ordered `cache_control` markers capped by `Caps.CacheBreakpoints`, an Anthropic `stop_reason` becomes a `FinishReason`, a `ResponseSchema` is honoured through a forced tool call only when the profile declares `Caps.Constrained`, and an opaque `Reasoning` signature round-trips to the same endpoint.

#### Scenario: cache control markers mapped in order
ID: `model.anthropic-cache-markers-order`

- WHEN a request carries three `CacheBreak` blocks and the selected profile has `CacheExplicit` with `CacheBreakpoints` 2
- THEN the adapter sends two `cache_control` markers in block order and drops the third

#### Scenario: stop reason mapped to finish reason
ID: `model.anthropic-stop-reason-mapped`

- WHEN the provider stream ends with `end_turn`, `tool_use` or `max_tokens`
- THEN the final chunk carries `FinishStop`, `FinishToolUse` or `FinishMaxTokens` respectively

#### Scenario: response schema honoured through forced tool
ID: `model.anthropic-forced-tool-schema`

- WHEN a `ResponseSchema` reaches a profile that declares `Caps.Constrained`
- THEN the adapter sends one forced tool carrying the schema and returns the tool arguments as the response

#### Scenario: reasoning signature round-trips
ID: `model.anthropic-reasoning-signature`

- WHEN the provider returns an opaque `Reasoning` block that carries a signature
- THEN the block is retained with its signature and re-sent unchanged to the same endpoint

### Requirement: Provider tool streams complete each call independently

Argument previews MUST carry their call identity. Each completed tool block MUST produce one executable `ToolUse` with complete arguments. Indexed calls MUST NOT share argument accumulation. Forced structured-output mapping MUST retain its existing text behavior, and unfinished arguments MUST remain non-executable.

#### Scenario: Anthropic returns multiple tool blocks
ID: `model.anthropic-complete-tool-blocks`
- WHEN an Anthropic response streams two indexed tool blocks with separate argument fragments
- THEN both complete calls retain their own ID, name and arguments and execute through the native conversation as authorized

#### Scenario: Provider arguments stream before completion
ID: `model.provider-argument-preview-identity`
- WHEN either provider streams tool argument fragments
- THEN previews carry the corresponding call identity and exactly one complete block is admitted per completed call

#### Scenario: Forced output and unfinished arguments
ID: `model.forced-output-and-tool-truncation`
- WHEN a response uses forced structured output or ends before tool arguments complete
- THEN forced output retains its text mapping and incomplete arguments are not executed

### Requirement: OpenAI stream termination is final

An OpenAI iterator MUST yield nothing after its consumer returns false or after a terminal stream error. Estimated usage MUST be emitted only on clean completion when reported usage is absent. The response body MUST be released on every exit.

#### Scenario: The consumer stops before usage arrives
ID: `model.openai-consumer-stop-final`
- WHEN a consumer breaks after a text or reasoning chunk
- THEN iteration returns without a later usage yield or panic and the response body closes

#### Scenario: A stream decode fails before usage arrives
ID: `model.openai-stream-error-final`
- WHEN a response contains a terminal decode or read failure
- THEN the iterator yields the failure once and produces no later usage chunk

### Requirement: Optional cache usage details remain optional

A reported usage record without cache details MUST remain valid for `CacheAuto`. Missing details MUST produce zero cache-token counts without a panic.

#### Scenario: Automatic cache mode receives only total usage
ID: `model.openai-cache-details-absent`
- WHEN `CacheAuto` receives nonzero usage totals without prompt-token details
- THEN usage is reported with zero cached tokens and iteration completes safely
