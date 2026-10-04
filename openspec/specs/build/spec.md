# Strategies and Build

Capability: `build` · Spec v1.6 (ADR-0134) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `build` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.12 Strategies and Build

| Strategy | Implementations |
|---|---|
| `Assembler` | `StablePrefix` (default), `AnthropicExplicitCache`, `Passthrough` |
| `StructuredOutput` | `Constrained`, `ToolSchema`, `ValidateRepair`; option `ReasonFirst` (default on for `Agentic`); all run app-side validation and an optional `Validate func(Out) error` |
| `Router` | `Static`, `ByLatencyClass`, `Decider`, `Cascade` |
| `AffinityKeyStrategy` | `None`, `SessionHash`, `TenantHash` |
| `Limiter` | `std/limit` (local), `adapter/redis` (quota pools, admission) |
| `RetryPolicy` | `retry.Exponential`, `retry.RetryAfter` |
| `ContextPolicy` | projections `Truncate`, `ClearToolResults`; compactors `Summarize`, `ProviderCompact` (see `context`) |
| `ResumeStrategy` | `Replay`, `Native` |

Resolution: flow option > model profile > default derived from `Caps`.

```go
type Stores struct {
	SessionLog  SessionLog
	Checkpoints Checkpoints
	Journal     Journal
	Runs        Runs
	AuditLog    AuditLog
	EventLog    EventLog
	OutputStore OutputStore
	NotesStore  NotesStore
	Feedback    FeedbackStore
}

// (illustrative) names may change during M0; see contract tiers
type Option func(*config) error
func Build(opts ...Option) (*Stack, error)
func WithModels(models ...Model) Option
func WithStores(s Stores) Option
func WithPrompts(p PromptSet) Option
func WithToolChain(c ToolChain) Option
func WithModelChain(c ModelChain) Option
func WithCredentialSource(cs CredentialSource) Option
func WithFlags(f Flags) Option
func WithRedactor(r Redactor) Option
func WithSandbox(sb Sandbox) Option
// std presets are Options too: std.Interactive(), std.Agentic(), std.Batch()
```

Options not listed in the code block are `(illustrative)` until M0: `SequentialTools()`, `OnStall(Detach)`, `WithStreamBuffer(n)`, `WithLimits(RunLimits)`, `WithBudget(scope, Budget)`, `WithToolPolicy(source, ToolPolicy)`, `WithPinnedManifest(path)`, `WithRouter`, `WithLimiter`, `WithRetry`, `WithGuards(stage, ...Guard)`, `WithFallback`, `WithWaker`, `WithTracerProvider`, `WithMeterProvider`, `WithRetention(RetentionPolicy)`, `WithRetentionSource`, `Ephemeral()`, `WithNotifier(Notifier)`, `WithHealthTimeout(d)`. `memory.New() Stores` bundles the memory implementations.

`Build` also computes the `ReleaseManifest` and its `ID` (`release`); `Stack.Manifest()` returns it. Validation happens in `Build` and in every flow constructor; nothing is validated lazily per request. Rejected combinations include: `Constrained` without `Caps.Constrained`; `AnthropicExplicitCache` on a non-Anthropic endpoint; `SessionHash` affinity without a configured affinity header; `Windowed` on a non-streaming model; `Native` resume on a backend without native checkpoints; `SideEffect` tools without idempotency acknowledgement; `Scheduled` suspension without a `Waker`. The resolved flow × profile × strategy matrix is logged once at startup.


## Requirements

### Requirement: Build validation

#### Scenario: exfil derived from egress
ID: `build.exfil-derived-from-egress`
- WHEN a tool's `EgressPolicy.Allow` lists only `*.internal.example` with `PrivateRanges: AllowPrivate`
- THEN `Capabilities.Exfil` is false; adding `api.example.com` makes it true and the trifecta check counts it

#### Scenario: blob caps
ID: `build.blob-caps`
- WHEN a flow declares `File` input and the profile's `Caps.Blobs.Formats` has no PDF entry
- THEN `Build` fails naming the block kind and the profile

#### Scenario: provider tool unsupported
ID: `build.provider-tool-unsupported`
- WHEN a flow registers `provider.CodeExec()` and the profile's `Caps.ProviderTools` has no `code_exec` entry
- THEN `Build` fails naming the tool and the profile

#### Scenario: impossible combination
ID: `build.impossible-combination`
- WHEN a flow requests `Constrained` on a profile without `Caps.Constrained`
- THEN the flow constructor returns an error naming flow, profile and strategy

#### Scenario: opaque compaction under cross-provider fallback
ID: `build.opaque-compaction-fallback`
- WHEN a flow resolves `ProviderCompact` on a profile with `Caps.Compaction == CompactionOpaqueCap` and its `Fallback` crosses providers
- THEN `Build` returns an error naming flow, profile and strategy

#### Scenario: resolved matrix
ID: `build.resolved-matrix`
- WHEN `Build` succeeds
- THEN one structured log record lists every flow × profile × strategy resolution
