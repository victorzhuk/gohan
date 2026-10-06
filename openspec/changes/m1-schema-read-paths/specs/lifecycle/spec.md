# Spec Delta

## MODIFIED Requirements

### Requirement: Retirement, schema evolution, cost

The implementation MUST apply pure, registered N→N+1 upcasters to serialized versioned records before decoding them for reads. Upcasters MUST depend only on input bytes. A missing upcaster MUST return an error matching `types.ErrSchemaTooOld`; a reader MUST NOT return a stale shape. A read MUST NOT modify the stored bytes. A successful upcast MUST increment `gohan.schema.upcast` with integer `from` and `to` attributes once for the complete chain. Additive changes need no upcaster.

`storetest.Schemas` MUST replay immutable recorded fixtures for every released schema version of every stored shape through each shape's current reader. Each fixture MUST declare the schema version at which its record was written. A registry-only transformation does not satisfy reader replay.

#### Scenario: upcast on read
ID: `lifecycle.upcast-on-read`
- WHEN a `Message` stored at schema v1 is read by v3 code
- THEN upcasters v1→v2→v3 run, the stored record is unchanged, and `gohan.schema.upcast` increments once with `from=1` and `to=3`
- AND an incomplete chain returns an error matching `types.ErrSchemaTooOld` without returning a stale message

#### Scenario: historical fixtures
ID: `lifecycle.historical-fixtures`
- WHEN `storetest.Schemas` runs
- THEN every recorded fixture from released versions loads through the current reader for its shape without error
- AND each fixture declares the schema version at which its stored bytes were written

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

#### Scenario: cost tags forwarded
ID: `lifecycle.cost-tags-forwarded`
- WHEN a run has `CostTags{Feature: "assist", Environment: "prod"}`
- THEN spans carry them and providers that accept metadata receive them; `gohan.cost.per_run{flow}` records the run's total

#### Scenario: cache write socialized
ID: `lifecycle.cache-write-socialized`
- WHEN `SocializeCacheWrites` is on and one run repopulates the prefix cache
- THEN that run is not charged the write premium; cached reads carry the overhead rate
