# Spec Delta

## MODIFIED Requirements

### Requirement: Retirement, schema evolution, cost

Public `Build` MUST fail when a pinned profile is inside its notice window and
MUST warn instead when the build sets the warn-only option. The notice window
MUST default to 30 days and MUST be settable per build. The error MUST name the
profile and its successor. A profile whose `Sunset` is zero (no announced
retirement) MUST pass. Current state: no notice-window check exists anywhere in
the tree; the fields a check reads, `ModelProfile.Sunset` and
`ModelProfile.Successor`, ship (`core/types/model_profile.go:13-14`) and no
build option names a window.

A provider response classified `ClassDeprecated` MUST NOT be silent. The run
MUST route the call to `Successor` when the profile declares one and MUST
otherwise return `*ModelError{Class: Deprecated}` to the flow. The
implementation MUST NOT synthesise a fallback assistant message for a
deprecated call. The implementation MUST emit `gohan.model.deprecated_calls{profile}`
for every deprecated call, `gohan.model.deprecated_routed{profile,successor}`
when the call reroutes, and `gohan.model.sunset_warning{profile}` for each
build warning. These metric names are new and the implementation MUST emit
them. The classification seam ships: `ClassifyProviderError` maps the codes
`model_not_found`, `model_deprecated` and `model_retired` to `ClassDeprecated`
(`core/types/model_profile.go:123,142`); no non-test caller consumes it —
the only readers are tests and the conformance fixtures.

`Build` MUST validate every flow that can reach a profile against that
profile's `Caps` and against the `Caps` of its declared successor. When the
successor lacks a capability the flow's options require, `Build` MUST fail and
MUST name the option. The capability check reads `Caps.Temperature` and the
other `Caps` fields (`core/types/model_profile.go:55`); no successor validation
exists today.

A run's `CostTags` MUST reach the provider accounting path. The spans MUST
carry the tags and a provider that accepts request metadata MUST receive them;
`gohan.cost.per_run{flow}` MUST record the run's total. Current state:
`CostTags{Feature, Environment, CostCenter}` ships on `RunInfo`
(`core/types/identity.go:42,80`) and reaches local telemetry
(`std/assembly.go:92-94`); no adapter forwards the tags to a provider.

When `Pricing.SocializeCacheWrites` is on, the run that repopulates the prefix
cache MUST NOT be charged the cache-write premium, and the cost MUST fold into
an overhead rate carried by cached reads (`Pricing.CachedInput`,
`core/types/model_profile.go:81,85`). The cache behaviour itself is owned by
`m1-exact-match-cache`; this requirement binds only the socialisation duty on
top of that cache.

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
