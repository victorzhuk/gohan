# Proposal

## Why

The `cache` capability declares a contract that no code implements. `openspec/specs/cache/spec.md:16-22` fixes the `CacheStep` rules, the `cache.Key` composition and the `std/cache` exact-match cache, yet `std/cache` does not exist (`std/` holds `assembly.go`, `context`, `egress`, … and no `cache` directory), no `AllowCache` symbol ships anywhere, and no `cache.Key` call site exists. The only cache-shaped code today is the private prefix memo in `std/assembly.go:20-51` (`prefixMu`/`prefixLast`/`prefixHave`/`prefixReq`), keyed by pointer identity at `:75-101`, and it carries no manifest hash or prompt version, so it cannot satisfy `cache.key-includes-manifest`.

The gap is load-bearing for another M1 change. `m1-model-retirement-cost` owns `lifecycle.cache-write-socialized` and is `blocked` on this one (`openspec/changes/m1/design.md:107-113`, its `tasks.md:34-38`): its fold reads a cache seam that is unauthored. `Pricing.SocializeCacheWrites` already ships (`core/types/model_profile.go:85`) and is already read by the limiter (`core/chains/limits.go:135-136`), but no run ever reports `Usage.CacheWriteTokens` or `Usage.CachedInputTokens` from a harness cache, so the fold is unreachable.

## What Changes

- **Added** an exact-match cache over the assembled model request prefix: the byte-stable blocks preceding the last `CacheBreak`. A hit returns the stored prefix; a miss assembles and repopulates.
- **Added** `cache.Key`, the content-addressed key the contract names: tenant, principal scope class, manifest hash (prompts, chains, toolset, model `Version`), resolved prompt version, normalized request.
- **Added** the write/read rules: entries only after a succeeded run, never for `Uncertain` runs, guard blocks, refusals or `max_tokens` finishes; never shared across tenants or scope classes.
- **Added** `Build` enforcement of `CacheStep`: refused on a `Conversation` flow, or on a flow with a non-`ReadOnly` tool, unless the flow sets `AllowCache()`.
- **Added** usage reporting: a hit reports `Usage.CachedInputTokens` and no `Usage.CacheWriteTokens`; a repopulating miss reports `Usage.CacheWriteTokens`. The retirement change's fold (`Pricing.SocializeCacheWrites`) consumes these; this change emits them and does not price.
- **Added** `gohan.cache.hit` / `gohan.cache.miss` counters, per the `telemetry` metric policy (`openspec/specs/telemetry/spec.md:111`).

**BREAKING**: a flow that carries a `CacheStep` on a `Conversation` flow, or with a non-`ReadOnly` tool, now fails at the flow constructor until it sets `AllowCache()`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `cache`: the *Cache contract* requirement gains its implementing normative text (keyed entry, `Build` refusal, never-cache-failures) while its three registered scenarios keep their IDs and WHEN/THEN lines; the exact-match prefix cache, its usage reporting and its metrics land as new requirements.

## Impact

- Code: new `std/cache` (key builder + exact-match memory store); `std/assembly.go` consults it instead of relying only on the private pointer memo (`std/assembly.go:103-131`); `Build` gains the `CacheStep` checks and the `AllowCache()` option; the model-call path fills `Usage.CachedInputTokens`/`Usage.CacheWriteTokens` (`core/types/model_types.go:33-43`).
- Contract: `cache.Key`'s composition and the prefix-cache write/read rules become normative; no existing struct changes.
- Metrics: `gohan.cache.hit`, `gohan.cache.miss` (`openspec/specs/telemetry/spec.md:111`); usage attrs `gohan.usage.cached_input` / `gohan.usage.cache_write` already ship as key constants (`core/types/telemetry.go:75-76`).
- Dependency: `m1-model-retirement-cost` consumes the seam and keeps `lifecycle.cache-write-socialized`; this change does not claim that ID.
- Not in scope: `adapter/redis` (only `adapter/otel` exists today; `docs/overview.md:111` places it in M3), and any semantic/similarity cache (ADR-0071 — "gohan ships no semantic cache", `openspec/specs/cache/spec.md:16`).
