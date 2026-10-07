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

The implementation MUST treat flags in two classes. Frozen flags MUST be evaluated once at run start with the session hash as the targeting key, MUST be written to `RunInfo.Flags`, the checkpoint `State` and the run-start audit record, and MUST be read from that snapshot for every evaluation inside the run, so a later provider change cannot alter a live run. The frozen carrier MUST be `runtime.FlagSnapshot` (`map[string]any`): `RunInfo.Flags` and the checkpoint `State.Flags` both hold that type, so the checkpoint shape is unchanged. `Replay` MUST assert the recorded snapshot and MUST return `ErrFlagDrift` before any model call when the snapshot cannot be honoured.

Live flags MUST be re-evaluated before every model call and every non-`ReadOnly` tool call, including on resume after approval. The tool-call half MUST be applied in the permission gate's live-control position, after the hard scope deny and before the taint hook, as an action that can allow, force `Ask` or deny; the model-call half MUST be applied by a model-chain step that runs before the call is issued. A live flag can only disable a tool, force `Ask` or block a flow and can never widen a hard block, a taint deny or a registered capability. An approval MUST NOT override a live deny.

A live evaluation MUST carry `EvaluatedAt` and `FromDefault`. When `now - EvaluatedAt > FreshnessLimit` or `FromDefault` is true, the next effect MUST suspend with `AwaitingControl`; the suspension MUST resume through the empty wake, and when no fresh state arrives within `MaxControlWait` the effect MUST be denied and the run MUST continue with a `Failed(Permanent)` tool result.

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
