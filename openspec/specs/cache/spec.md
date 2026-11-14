# Cache contract

Capability: `cache` · Spec v1.0 baseline (restructured from gohan-spec v0.13; ADR-0071 owns the v1 cache contract) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `cache` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.8b Cache contract

gohan ships no semantic cache. Any step of kind `CacheStep` in the model chain's outer slot is subject to a contract that `Build` enforces:

- Refused on `Conversation` flows and on any flow whose registered tools include a non-`ReadOnly` effect, unless the flow sets `AllowCache()` (which `Explain` prints as a warning).
- The key is built by `cache.Key(ctx, req)` and always includes: tenant, principal scope class (per-user vs shared, declared by the step), manifest hash (prompts, chains, toolset, model `Version`), resolved prompt version, and the normalized request; a step that supplies its own key must embed `cache.Key` output in it.
- Entries are never written for `Outcome != Succeeded`, guard blocks, refusals (`ContentPolicy`), `Uncertain` runs, or responses with `Finish == max_tokens`.
- Hits are emitted as one chunk, report the served prefix as `Usage.CachedInputTokens`, and are tagged `gohan.cache.hit` with the cached `Version`; a repopulating miss reports the written prefix as `Usage.CacheWriteTokens`; misses record `gohan.cache.miss`. (This supersedes the ADR-0071 rule that a hit is charged zero usage.)
- `std/cache` provides the key builder and an exact-match cache (memory, Redis via `adapter/redis`) intended for `Extract`, `Classify` and `Route`. Similarity-based matching is user-supplied and must set an explicit threshold and a per-domain invalidation hook; the contract above still applies.


## Requirements

### Requirement: Cache contract

The cache contract governs the outer-slot `CacheStep` and the exact-match cache the harness owns. An entry MUST be keyed by `cache.Key` and MUST NOT be shared across tenants or scope classes. `Build` MUST refuse a `CacheStep` on a `Conversation` flow, or on any flow with a non-`ReadOnly` tool, unless the flow sets `AllowCache()`. No entry MUST be written for an `Uncertain` run, a guard block, a refusal, or a `max_tokens` finish.

#### Scenario: refused on conversation
ID: `cache.refused-on-conversation`
- WHEN a `CacheStep` is added to a `Conversation` flow without `AllowCache()`
- THEN the flow constructor fails naming the step and the rule

#### Scenario: key includes manifest
ID: `cache.key-includes-manifest`
- WHEN prompts change (new manifest hash)
- THEN previously cached entries miss

#### Scenario: never cache failures
ID: `cache.never-cache-failures`
- WHEN a run ends `Uncertain` or a guard blocks the output
- THEN no cache entry is written

### Requirement: Exact-match prefix cache

The harness MUST cache the assembled model request prefix — the byte-stable blocks that precede the last `CacheBreak` — in an exact-match store. `cache.Key` MUST include the tenant, the principal scope class, the manifest hash of the prompts, chains, toolset and model `Version`, the resolved prompt version, and the normalized request. A lookup MUST hit only on a byte-identical key; a miss MUST assemble the request and repopulate the entry.

#### Scenario: identical prefix hits
ID: `cache.prefix-hit`
- WHEN a run assembles a request whose key matches a stored entry
- THEN the stored prefix is returned and the request is not assembled again

#### Scenario: repopulating miss writes the prefix
ID: `cache.prefix-miss-repopulates`
- WHEN no entry matches the key and the run proceeds
- THEN the request is assembled and its prefix is written under that key

#### Scenario: scope is never crossed
ID: `cache.scope-isolation`
- WHEN two runs differ in tenant or in principal scope class
- THEN neither run reads the other's entry

### Requirement: Cache usage reporting

A cache hit MUST report the served prefix as `Usage.CachedInputTokens` and MUST report no `Usage.CacheWriteTokens`. A miss that repopulates the cache MUST report the written prefix as `Usage.CacheWriteTokens`. The cache MUST NOT price tokens itself; the run's cost is charged from `Usage` against the profile's `Pricing`, and `Pricing.SocializeCacheWrites` folds the write premium into the cached-read rate.

#### Scenario: hit reports cached input
ID: `cache.hit-usage`
- WHEN a run's model call is served from the cache
- THEN its usage reports the prefix as cached input and reports no cache-write tokens

#### Scenario: repopulating miss reports cache write
ID: `cache.miss-write-usage`
- WHEN a run's model call misses and repopulates the cache
- THEN its usage reports the written prefix as cache-write tokens

### Requirement: Cache metrics

A cache hit MUST increment `gohan.cache.hit`, tagged with the cached `Version`, and a cache miss MUST increment `gohan.cache.miss`, subject to the `telemetry` capability's metric policy.

#### Scenario: hit and miss counted
ID: `cache.hit-miss-metrics`
- WHEN a model call is served from the cache
- THEN `gohan.cache.hit` increments; and WHEN it misses, `gohan.cache.miss` increments
