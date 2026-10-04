# Feature flags

Capability: `flags` · Spec v1.0 baseline (restructured from gohan-spec v0.13; ADR-0078 and ADR-0079 own the flags contract) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `flags` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.8c Flags

```go
// (illustrative) names may change during M0; see contract tiers
type FlagContext struct {
	Tenant  string
	Subject string
	Session string
	Flow    string
}

type Flags interface {
	Bool(ctx context.Context, key string, def bool, fc FlagContext) (bool, Freshness, error)
	String(ctx context.Context, key string, def string, fc FlagContext) (string, Freshness, error)
	Int(ctx context.Context, key string, def int64, fc FlagContext) (int64, Freshness, error)
}

type Freshness struct {
	EvaluatedAt time.Time
	FromDefault bool
}

type FlagSnapshot map[string]string
```

Two flag classes, declared in wiring:

- **Frozen** (rollout, variant, tenant enablement): evaluated once at run start with the session hash as targeting key (sticky variants), snapshotted into `RunInfo.Flags`, the checkpoint `State` and the run-start audit record, and asserted on `Replay` (`ErrFlagDrift` if the snapshot cannot be honoured). Consumers are existing seams: `Router` (profile choice), `ToolFilter`, `PromptRef.Label`, `RunLimits` overrides, chain step `Applies`.
- **Live** (`KillSwitch` kind): re-evaluated before every model call and every non-`ReadOnly` tool call, including on resume after approval; can only reduce — disable a tool, force `Ask`, block a flow. An approval cannot override a live deny; flags never grant scopes or effects.

Freshness: each live evaluation carries `EvaluatedAt` and `FromDefault`. If `now - EvaluatedAt > FreshnessLimit` or `FromDefault` is true, the next effect suspends with `AwaitingControl` (ADR-0079) until fresh state arrives or `MaxControlWait` elapses, after which the effect is denied. `std/flags` ships a static/env provider, the snapshot logic and the freshness watcher; `adapter/openfeature` bridges the OpenFeature Go SDK (provider-agnostic: GO Feature Flag, flagd, LaunchDarkly, …) and exposes provider freshness rather than the SDK's silent defaults.


## Requirements

### Requirement: Frozen and live flags

#### Scenario: frozen snapshot
ID: `flags.frozen-snapshot`
- WHEN a run starts with rollout flag `assist_v2 = "b"`
- THEN `RunInfo.Flags` and the checkpoint carry the value and every evaluation within the run returns it, even if the provider changes

#### Scenario: default is not fresh
ID: `flags.from-default-suspends`
- WHEN a live kill-switch evaluation returns `FromDefault: true` before a `SideEffect` tool call
- THEN the run suspends with `AwaitingControl`

#### Scenario: stale then denied
ID: `flags.stale-then-deny`
- WHEN a live evaluation is older than `FreshnessLimit` and no fresh state arrives within `MaxControlWait`
- THEN the effect is denied and the run continues with a `Failed(Permanent)` tool result

#### Scenario: flags never widen
ID: `flags.never-widen`
- WHEN a live flag would enable a tool that wiring did not register or raise an effect
- THEN nothing changes; flags can only disable, force `Ask` or block

#### Scenario: replay asserts snapshot
ID: `flags.replay-asserts-snapshot`
- WHEN `Replay` resumes a run whose frozen snapshot cannot be honoured by the current provider
- THEN `ErrFlagDrift` is returned before any model call
