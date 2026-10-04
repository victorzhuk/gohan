# ADR-0089: Cost attribution: `RunInfo.CostTags{Feature, Environment, CostCenter}` set by transport and forwarded as provider metadata where supported; `Pricing.CacheWrite` with a `SocializeCacheWrites` policy; batch flows record per-item cost pro-rated by token share; `gohan.cost.per_run{flow}` unit-cost histogram

Status: accepted · Origin: gohan-spec v0.13 decision D89

## Decision

Cost attribution: `RunInfo.CostTags{Feature, Environment, CostCenter}` set by transport and forwarded as provider metadata where supported; `Pricing.CacheWrite` with a `SocializeCacheWrites` policy; batch flows record per-item cost pro-rated by token share; `gohan.cost.per_run{flow}` unit-cost histogram.

## Context and evidence

Chargeback needs tags at the source and a unit cost, not a total; cache-write and batch pricing are otherwise misattributed.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
