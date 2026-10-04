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

### Provider adapter contract

Every `Model` implementation (`adapter/openai`, `adapter/anthropic`, gateways, `gohantest.ScriptedModel`) SHALL:

1. **Errors.** Return every failure as `*ModelError`. Mapping: HTTP 429 and provider quota codes → `ClassRateLimited` with `RetryAfter` parsed from `Retry-After` / `x-ratelimit-reset-*` when present; 5xx, connection errors, `Connect`/`FirstChunk` timeouts → `ClassTransient`; context-length errors → `ClassContextOverflow`; safety refusals at the request level → `ClassContentPolicy`; model-retired/not-found → `ClassDeprecated`; 401/403 → `ClassAuth`; 400 and everything else → `ClassPermanent`. `retry.RetryAfter` honours `RetryAfter`.
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

`Timeout.Idle` is measured on the provider read into the stream buffer, not on the consumer (`streams` *Slow consumers*). `Timeout` defaults from `LatencyClass` when zero: `Interactive` 5 s / 10 s / 15 s, `Agentic` 10 s / 30 s / 60 s, `Batch` 30 s / 120 s / 300 s (connect / first chunk / idle between chunks). A `Connect` or `FirstChunk` expiry is a `Transient` error (retry and fallback apply); an `Idle` expiry after the first chunk is `Permanent` for that call. **Mid-stream failure** (any error after the first chunk, including `Idle` expiry): the partial assistant message is appended to `SessionLog` with `Finish: FinishError`; deltas already streamed are not retracted; the run ends with `StopFailed` and `Done` carries the error; `Replay` treats a `FinishError` message as terminal and never re-sends it to a provider; the next user turn starts fresh from that history.

`Version` pins the exact model the profile was validated against. Providers report the served version in `Usage.ModelVersion`; a mismatch increments `gohan.model.version_drift` and, with `Caps.StrictVersion`, fails the call as `Permanent` so the router moves on. Fallback targets are validated at build for `Caps` compatibility with every flow that can reach them (tools, constrained output, images, streaming) and get their own `ContextPolicy` (projections, then compaction) before the request is sent.

**Iterator contract** (normative for every `iter.Seq2` returned by gohan or an adapter): the iterator body runs on the caller's goroutine; any helper goroutine it starts terminates before the iterator returns; it returns within one network read of `ctx.Done()`; when `yield` returns false it releases all resources (HTTP body, stream reader) before returning; it never yields after returning an error. `conformance.Model` verifies early break and cancellation with a goroutine-leak check. Error tuples follow the protocol in `streams`. Consumers that need pull semantics use `iter.Pull` and must call `stop`; core and `std` never do so on the request path.


## Requirements

### Requirement: Provider adapter contract

#### Scenario: status maps to class
ID: `model.status-to-class`
- WHEN the conformance fixtures replay 429, 500, 401, 400, a context-length error and a retired-model error
- THEN the adapter returns `*ModelError` with `ClassRateLimited`, `ClassTransient`, `ClassAuth`, `ClassPermanent`, `ClassContextOverflow`, `ClassDeprecated` respectively

#### Scenario: retry-after parsed
ID: `model.retry-after-parsed`
- WHEN a 429 carries `Retry-After: 7`
- THEN `ModelError.RetryAfter == 7s` and `retry.RetryAfter` waits at least that long before the next attempt on that endpoint

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

### Requirement: Provider keys

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
- THEN `gohan.model.version_drift` increments; with `StrictVersion` the call fails `Permanent` and falls over

### Requirement: Tool argument deltas

#### Scenario: tool args delta kind
ID: `model.tool-args-delta-kind`
- WHEN a provider streams tool-call argument fragments
- THEN the adapter yields `ModelChunk{Kind: DeltaToolArgs, Delta: <fragment>, ToolUse: {ID, Name}}` per fragment and one complete `ToolUse` block at the end; an adapter that receives arguments whole yields exactly one delta before the block

### Requirement: Iterator contract

#### Scenario: early break releases
ID: `model.early-break-releases`
- WHEN a consumer breaks after the first chunk of `Model.Generate`
- THEN the HTTP body is closed and no goroutine remains (leak check passes)

#### Scenario: cancel returns promptly
ID: `model.cancel-returns-promptly`
- WHEN ctx is cancelled while the provider is silent
- THEN the iterator returns `context.Canceled` within the read deadline
