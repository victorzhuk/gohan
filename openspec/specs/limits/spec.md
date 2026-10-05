# Run limits and uncertainty

Capability: `limits` · Spec v1.5 (ADR-0119) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `limits` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.13b Run limits and outcome uncertainty

```go
type RunLimits struct {
	MaxTurns     int
	MaxToolCalls int
	MaxCost      float64
	MaxWallClock time.Duration
	SoftRatio    float64
	MaxDepth            int
	MaxParallelChildren int
	MaxParallelTools    int
	ConsumerStall       time.Duration
	MaxSandboxSeconds   float64
	MaxBlobBytes        int64
}

var (
	InteractiveLimits = RunLimits{ConsumerStall: 30 * time.Second, MaxTurns: 6, MaxToolCalls: 20, MaxWallClock: 60 * time.Second, SoftRatio: 0.8, MaxDepth: 2, MaxParallelChildren: 8, MaxParallelTools: 8}
	AgenticLimits     = RunLimits{ConsumerStall: 120 * time.Second, MaxTurns: 50, MaxToolCalls: 200, MaxWallClock: 30 * time.Minute, SoftRatio: 0.8, MaxDepth: 2, MaxParallelChildren: 8, MaxParallelTools: 8}
	BatchLimits       = RunLimits{MaxTurns: 20, MaxToolCalls: 100, MaxWallClock: 10 * time.Minute, SoftRatio: 0.9, MaxDepth: 1, MaxParallelChildren: 4, MaxParallelTools: 4}
)

type LimitExceededError struct {
	Limit string
	Value float64
}

type UncertainOutcomeError struct {
	Out       any
	Uncertain []CallKey
}
```

Defaults for fields a preset leaves zero: `MaxTurns 20`, `MaxToolCalls 50`, `MaxWallClock 10m`, `SoftRatio 0.8`, `MaxBlobBytes 256 MiB` (`messages`); `MaxSandboxSeconds` 0 is unbounded, as `ConsumerStall` 0 and `MaxCost` 0 are; every preset sets its own `MaxTurns`/`MaxToolCalls` (`Interactive` 6/20, `Agentic` 50/200, `Batch` 20/100). Reaching `MaxTurns` or `MaxToolCalls` ends the run with `Done{Reason: StopLimit}` — not resumable, with the partial outcome reported normally. Exceeding `MaxCost` or `MaxWallClock` aborts the run with `*LimitExceededError`, not resumable (ADR-0147); a steer-drain turn counts against `MaxTurns` and runs only while turns remain (`flow`, `runtime`); crossing `SoftRatio` emits `LimitWarning` once. Limits are enforced in the chains, so they apply under any backend.

Defaults: `std.Interactive()`, `std.Agentic()` and `std.Batch()` install the matching `*Limits` value; a flow with zero `RunLimits` and no preset fails `Build` (unbounded runs are never implicit). `MaxCost` defaults to 0 = unlimited and `Build` warns when it is unset outside `Batch`. Cost is computed by the model chain's telemetry step at message end from `Usage × Pricing` (cached input at `CachedInput`, cache writes per `SocializeCacheWrites`) plus `Usage.SandboxSeconds × Pricing.SandboxSecond`, accumulated on the run and the tree, and checked against `MaxCost` after every model call and tool call.

A flow whose run finished with journal entries in `Outcome: Unknown` returns `*UncertainOutcomeError{Out, Uncertain}` instead of a clean result; `Done.Uncertain` carries the same list for `Conversation`. Before that, the harness runs each tool's `Verify` (`tools`) to reconcile what it can; business code decides the rest (alert, compensate). gohan never reports a run as clean over an unknown write.


## Requirements

### Requirement: Defaults and cost

#### Scenario: unbounded run rejected
ID: `limits.unbounded-rejected`
- WHEN a flow is built with zero `RunLimits` and no preset
- THEN `Build` fails naming the flow

#### Scenario: cost accumulates across tree
ID: `limits.cost-accumulates`
- WHEN a parent and two children each spend 0.04 with `MaxCost: 0.10` on the tree
- THEN the third spend triggers `*LimitExceededError{Limit: "MaxCost"}` and `Done.Cost` on the root equals the sum spent

### Requirement: Run limits

#### Scenario: hard cost abort
ID: `limits.hard-cost-abort`
- WHEN cumulative cost exceeds `MaxCost`
- THEN the run aborts with `*LimitExceededError{Limit: "MaxCost"}` before the next model call; `LimitWarning` was emitted once at `SoftRatio`

#### Scenario: wall clock
ID: `limits.wall-clock`
- WHEN a run exceeds `MaxWallClock` during a tool call
- THEN the tool's ctx is cancelled and the run aborts

#### Scenario: provider calls count
ID: `limits.provider-calls-count`
- WHEN `MaxToolCalls` is 3 and one model response performs three provider searches
- THEN the next tool call of any executor exceeds the limit and the run aborts

#### Scenario: wall clock is monotonic
ID: `limits.wall-clock-monotonic`
- WHEN the run's budget is measured while the system wall clock is stepped backwards by one hour
- THEN `MaxWallClock` still expires after the configured duration of elapsed monotonic time

#### Scenario: limits under foreign backend
ID: `limits.limits-under-foreign-backend`
- WHEN the same limits are configured on an eino graph flow
- THEN the same scenarios pass
