# Chains, prompts and Explain

Capability: `chains` · Spec v1.1 (ADR-0128) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `chains` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.8 Middleware and chains

```go
type ModelFunc func(ctx context.Context, req ModelRequest) iter.Seq2[ModelChunk, error]
type ModelMiddleware func(next ModelFunc) ModelFunc

type ToolFunc func(ctx context.Context, call ToolUse) (ToolResult, error)
type ToolMiddleware func(next ToolFunc) ToolFunc

type StepKind int

const (
	KindGuard StepKind = iota
	KindGate
	KindJournal
	KindLimit
	KindRetry
	KindFallback
	KindHedge
	KindTelemetry
	KindRedact
	KindCache
	KindUser
	KindHooks
	KindRouter
	KindBudget
)

type Step[M any] struct {
	Name     string
	Kind     StepKind
	Applies  func(ToolSpec) bool
	Use      M
}

type ToolChain []Step[ToolMiddleware]
type ModelChain []Step[ModelMiddleware]

type StepError struct {
	Step string
	Err  error
}
```

Core defines chains as **data** and validates ordering constraints (declared per `StepKind`, e.g. `Journal` must be inside `Gate`; `Retry` must be inside `Fallback`; `Hedge` must be outside `Fallback` and inside `Router`; `Budget` must be outside `Hooks`). Core ships **no steps**. `agent.New` takes chains explicitly; a flow built with empty chains calls the raw model and tools. Every step is named; a panic or error inside a step is wrapped as `StepError{Step}` so stack traces and logs name the step. `Applies` lets a step skip tools by spec (used by `std` to exempt `ReadOnly` tools from journaling and shielding).

**Hedging.** `std/hedge.New(HedgePolicy{After, Targets, Budget, MaxExtra})` is a `KindHedge` step. When the primary attempt has produced no body chunk within `After` (default: the endpoint's rolling P90 time-to-first-chunk from `std/telemetry`, never below 800 ms; a fixed `time.Duration` may be given), it starts at most `MaxExtra` (default 1) further attempts on the router's next targets — never the same endpoint — and the first attempt to deliver a body chunk wins; the others are cancelled through their ctx. `Budget` (default 0.05) is a token bucket of hedges per base call; when empty no hedge fires and `gohan.hedge.budget_exhausted` increments. A hedge is refused when the target's breaker is open or half-open, when the call declares provider-executed tools (`tools`), when `Caps.Streaming` is false, or when the profile's `LatencyClass` is `Batch`. The losing attempt's `Usage` is added to the run's usage and cost as `gohan.usage.hedge_loser`, counted against `RunLimits.MaxCost` and quota pools; the journal records one model step with `Attempts` and the winner's endpoint; `Usage.ModelVersion` is the winner's; cassettes record and replay the winner only. `Retry` applies per attempt, `Fallback` when every attempt fails, and shutdown, cancellation and `ConsumerStall` cancel all attempts. Metrics: `gohan.hedge.fired{endpoint}`, `gohan.hedge.won{endpoint}`, `gohan.hedge.budget_exhausted`.

`gohan/std` provides the recommended chains as short exported functions. `std.ToolChain(opts)` and `std.ModelChain(opts)` return the orders below; `std.Interactive()`, `std.Agentic()`, `std.Batch()` bundle chains, prompts, guards and limits for a latency class. They are meant to be read and, when needed, copied into a service and edited. User middleware goes into the named slots or anywhere ordering validation allows.

```
Tool:
  telemetry
  → resolve + validate     unknown name → Failed result + gohan.tool.unknown; args vs Schema → Failed(Permanent)
  → limits                 RunLimits.MaxToolCalls
  → [user: outer]          kind: User
  → scope check            RequiredScopes vs Principal; hard Deny
  → permission gate        Decider + Effect defaults; Ask → suspend
  → hooks                  BeforeTool / AfterTool / OnToolError, loop detector
  → journal + idem key     Applies: Effect != ReadOnly. Reserve by CallKey and Fingerprint / replay / pin key / Complete
  → [user: inner]
  → tool-result guard      indirect injection, secrets, PII
  → output store           content > MaxOutput → Ref + truncated inline
  → cancel shield          Applies: Effect == SideEffect. context.WithoutCancel + tool timeout; result persisted before cancellation is honoured
  → timeout                SideEffect timeout → Outcome: Unknown
  → tool

Model:
  telemetry                incl. TTFT, TPOT, usage, cost
  → limits                 RunLimits.MaxTurns / MaxCost / MaxWallClock; hard abort
  → [user: outer]          kind: User; CacheStep kind only under the cache contract (§6.8b)
  → budget                 per tenant / flow / run; charged on reported usage
  → hooks                  BeforeModel / AfterModel / OnModelError
  → router                 selects endpoint(s) among validated profiles
  → hedge                  optional; one extra attempt on the next target when the primary's first body chunk is late (std/hedge)
  → fallback               class-based, across endpoints, only before first chunk; re-fits context per target
      per endpoint:
      → breaker            circuit breaker per endpoint (opens on error-rate / consecutive RateLimited+Transient)
      → limiter            rate + in-flight bulkhead (MaxInFlight)
      → retry              class-based, only before first chunk; waits `ModelError.RetryAfter` when a retried class carries one (`ClassRateLimited` is never retried)
      → [user: inner]     kind: User
      → provider
```

Model error classes and default policy (`errors.go`):

| `ModelError.Class` | Retry (same endpoint) | Fallback (next endpoint) | Surface |
|---|---|---|---|
| `ClassRateLimited` | never | immediately | if all endpoints exhausted |
| `ClassTransient` (5xx, `Connect`/`FirstChunk` timeouts, connection) | yes, bounded, backoff | after retries | if exhausted; an `Idle` expiry after the first chunk is `Permanent` for that call (`model`) |
| `ClassContextOverflow` | once, after `ContextPolicy` (projections, then compaction) against the same profile | yes, re-fit against target | if still overflowing |
| `ClassContentPolicy` | never | never | always, as the `gohan.content_policy` problem from `ModelError{Class: ClassContentPolicy}` |
| `ClassDeprecated` | never | to `Successor` only | if no successor; never as a fallback message |
| `ClassAuth`, `ClassPermanent` | never | never | always |
| `ClassVersionDrift` (`Caps.StrictVersion`) | never | immediately | if every endpoint drifted |

Providers normalize their errors into these classes; the conformance suite checks the mapping with recorded fixtures. Breaker state and every failover decision are recorded as span events and `gohan.model.failover` metrics.

Hook outcomes (sugar over middleware):

| Outcome | Model hooks | Tool hooks |
|---|---|---|
| `Continue` | proceed | proceed |
| `Replace(x)` | use message `x` | use result `x` |
| `Deny(reason)` | – | not executed; model sees error result |
| `Suspend(reason, payload)` | suspend run | suspend run |
| `Abort(err)` | stop, not resumable | stop, not resumable |

Hooks run in registration order; first non-`Continue` wins. The loop detector (built-in hook) aborts or denies when the same tool with identical args is called more than N times in a run.

### 6.8a Prompts and Explain

```go
// (illustrative) names may change during M0; see contract tiers
type PromptSet struct {
	FenceOpen, FenceClose string
	DataNotInstructions   string
	OutcomeUnknown        string
	ReadBackHint          string
	OutputRefHint         string
	RepairInstruction     string
	NotesPreamble         string
	OperatorTurn          string
	Version               string
}

// (illustrative) built by Build; holds resolved flows, chains, profiles and the ReleaseManifest
type Stack struct{}

type Explanation struct {
	Flow     string
	Profile  string
	Steps    []StepInfo
	Prompts  map[string]string
	Skills   map[string]string
	Release  string
}

type StepInfo struct {
	Name    string
	Kind    StepKind
	Applies []string
}

func (s *Stack) Explain(f any) Explanation
```

Core has no default `PromptSet`; `std.DefaultPrompts` is an exported value. `Build` requires a `PromptSet` whenever any step or provider uses one (validated at startup, named per consumer), and the manifest records its hash and `Version`. No other string authored by gohan ever reaches a model.

`Explain` returns, for a flow: the resolved tool and model chains step by step with `Applies` outcomes for each registered tool; the fully assembled first request for a sample input with every `PromptSet` string in place and origins marked; the profile, strategy and fallback resolution; guards per stage; limits; and the persistence writes expected per turn. `std.ExplainHandler` serves it over HTTP on a debug port; a service may also wire it as its own `explain` subcommand (JSON when stdout is not a TTY).


## Requirements

### Requirement: Canonical chain

`conformance.Chain` SHALL assert ordering on every backend.

#### Scenario: denied call not journaled
ID: `chains.denied-call-not-journaled`
- WHEN the gate denies a `SideEffect` call
- THEN no journal entry exists for its `CallKey`

#### Scenario: scope before decider
ID: `chains.scope-before-decider`
- WHEN the principal lacks `booking:write` and the decider would return Allow
- THEN the call is denied and the decider is not invoked

#### Scenario: fallback charged
ID: `chains.fallback-charged`
- WHEN the primary endpoint fails before the first chunk and the fallback succeeds
- THEN the budget is charged with the fallback's usage

#### Scenario: no retry after first chunk
ID: `chains.no-retry-after-first-chunk`
- WHEN a provider fails after emitting a delta
- THEN the error surfaces; no retry and no fallback occur

#### Scenario: cache replace is free
ID: `chains.cache-replace-is-free`
- WHEN an outer-slot cache middleware returns `Replace`
- THEN no usage is charged and no provider call occurs

#### Scenario: per-endpoint limits
ID: `chains.per-endpoint-limits`
- WHEN endpoint A is at `MaxInFlight`
- THEN calls routed to endpoint B are not delayed by A's limiter

### Requirement: No hidden behavior

#### Scenario: empty chains
ID: `chains.empty-chains`
- WHEN a flow is built with core only, no `std`, and empty chains
- THEN a run calls the raw model and tools with no added prompt text, no guards, no journal; `Explain` shows zero steps and the assembled request equals instruction + history + input

#### Scenario: prompt strings accounted for
ID: `chains.prompt-strings-accounted-for`
- WHEN `Explain` renders the assembled request for a `std.Interactive()` flow
- THEN every non-user, non-instruction string in it maps to a named `PromptSet` field, and the manifest carries the `PromptSet` hash

#### Scenario: step-named failure
ID: `chains.step-named-failure`
- WHEN a user middleware panics inside the tool chain
- THEN the flow returns `*StepError{Step: "<name>"}` wrapping the panic, and the span records the step

#### Scenario: read-only tools pay nothing
ID: `chains.read-only-tools-pay-nothing`
- WHEN a turn calls only `ReadOnly` tools
- THEN no journal read or write occurs, no cancel shield is applied, and exactly one `SessionLog.Append` happens

#### Scenario: hedge fires on late first chunk
ID: `chains.hedge-fires-on-ttft`
- WHEN the primary endpoint returns headers at once but no body chunk within `After`
- THEN a second attempt starts on the next router target and the first body chunk delivered wins

#### Scenario: hedge loser cancelled and charged
ID: `chains.hedge-loser-cancelled-and-charged`
- WHEN the hedge wins
- THEN the primary attempt's ctx is cancelled, its reported usage is added to the run as `gohan.usage.hedge_loser` and counts against `MaxCost`

#### Scenario: hedge budget cap
ID: `chains.hedge-budget-cap`
- WHEN every call to a degraded endpoint would hedge
- THEN at most `Budget` of them do, the rest wait for the primary or its timeout, and `gohan.hedge.budget_exhausted` increments

#### Scenario: hedge refused with provider tools
ID: `chains.hedge-refused-with-provider-tools`
- WHEN the call declares a provider-executed tool
- THEN no hedge fires however late the first chunk is

#### Scenario: hedge never same endpoint
ID: `chains.hedge-never-same-endpoint`
- WHEN the router has a single target
- THEN the hedge step is inert and `Explain` says so

#### Scenario: hedge single journal step
ID: `chains.hedge-single-journal-step`
- WHEN a hedged call completes
- THEN the journal holds one model step with `Attempts: 2` and the winner's endpoint, and replay reproduces the winner's response

#### Scenario: preset is copyable
ID: `chains.preset-is-copyable`
- WHEN `std.Interactive()`'s body is copied into a service and one step removed
- THEN `Build` validates the remaining order and the flow behaves identically minus that step
