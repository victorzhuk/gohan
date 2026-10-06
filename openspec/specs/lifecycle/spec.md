# Retirement, schema evolution and cost

Capability: `lifecycle` · Spec v1.0 baseline (restructured from gohan-spec v0.13; ADR-0087 owns model retirement and sunset handling) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `lifecycle` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### Scope

Model retirement fields live on `ModelProfile` (`model`); `SchemaVersion` and upcasters are defined in `stores`; `CostTags` and `Pricing` in `identity`/`model`. This capability owns the cross-cutting requirements.


## Requirements

### Requirement: Retirement, schema evolution, cost

Public `Build` MUST fail when a pinned profile is inside its notice window and MUST warn instead when the build sets the warn-only option. The notice window MUST default to 30 days and MUST be settable per build. The error MUST name the profile and its successor. A profile whose `Sunset` is zero MUST pass.

A provider response classified `ClassDeprecated` MUST NOT be silent. The run MUST route the call to `Successor` when the profile declares one and MUST otherwise return `*ModelError{Class: Deprecated}` to the flow. The implementation MUST NOT synthesise a fallback assistant message for a deprecated call. The implementation MUST emit `gohan.model.deprecated_calls{profile}` for every deprecated call, `gohan.model.deprecated_routed{profile,successor}` when the call reroutes, and `gohan.model.sunset_warning{profile}` for each build warning.

`Build` MUST validate every flow that can reach a profile against that profile's `Caps` and against the `Caps` of its declared successor. When the successor lacks a capability the flow's options require, `Build` MUST fail and MUST name the option.

A run's `CostTags` MUST reach the provider accounting path, travelling as `ModelRequest.Metadata` (`RequestMetadata.CostTags`). A provider that accepts request metadata MUST receive them; `gohan.cost.per_run{flow}` MUST record the run's total.

When `Pricing.SocializeCacheWrites` is on, the run that repopulates the prefix cache MUST NOT be charged the cache-write premium, and the cost MUST fold into an overhead rate carried by cached reads (`Pricing.CachedInput`).

#### Scenario: sunset inside notice window
ID: `lifecycle.sunset-inside-notice-window`
- WHEN a pinned profile's `Sunset` is 20 days away and `NoticeWindow` is 30
- THEN `Build` fails (or warns under `SunsetWarnOnly`) naming the profile and successor

#### Scenario: deprecated never silent
ID: `lifecycle.deprecated-never-silent`
- WHEN a provider returns a deprecation error
- THEN the call routes to `Successor` if declared, otherwise the flow returns `*ModelError{Class: Deprecated}`; no fallback message is produced

#### Scenario: successor params validated
ID: `lifecycle.successor-params-validated`
- WHEN a successor profile lacks `Caps.Temperature` and a flow sets temperature
- THEN `Build` fails naming the option

#### Scenario: upcast on read
ID: `lifecycle.upcast-on-read`
- WHEN a `Message` stored at schema v1 is read by v3 code
- THEN upcasters v1→v2→v3 run, the stored record is unchanged, and `gohan.schema.upcast{1,3}` increments

#### Scenario: historical fixtures
ID: `lifecycle.historical-fixtures`
- WHEN `storetest.Schemas` runs
- THEN every recorded fixture from released versions loads through current readers without error

#### Scenario: cost tags forwarded
ID: `lifecycle.cost-tags-forwarded`
- WHEN a run has `CostTags{Feature: "assist", Environment: "prod"}`
- THEN spans carry them and providers that accept metadata receive them; `gohan.cost.per_run{flow}` records the run's total

#### Scenario: cache write socialized
ID: `lifecycle.cache-write-socialized`
- WHEN `SocializeCacheWrites` is on and one run repopulates the prefix cache
- THEN that run is not charged the write premium; cached reads carry the overhead rate
