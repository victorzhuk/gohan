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
