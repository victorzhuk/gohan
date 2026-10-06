# Spec Delta

## MODIFIED Requirements

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

## ADDED Requirements

### Requirement: Exact-match prefix cache

The harness MUST cache the assembled model request prefix — the byte-stable blocks that precede the last `CacheBreak` — in an exact-match store. `cache.Key` MUST include the tenant, the principal scope class, the manifest hash of the prompts, chains, toolset and model `Version`, the resolved prompt version, and the normalized request. A lookup MUST hit only on a byte-identical key; a miss MUST assemble the request and repopulate the entry.

#### Scenario: identical prefix hits
- WHEN a run assembles a request whose key matches a stored entry
- THEN the stored prefix is returned and the request is not assembled again

#### Scenario: repopulating miss writes the prefix
- WHEN no entry matches the key and the run proceeds
- THEN the request is assembled and its prefix is written under that key

#### Scenario: scope is never crossed
- WHEN two runs differ in tenant or in principal scope class
- THEN neither run reads the other's entry

### Requirement: Cache usage reporting

A cache hit MUST report the served prefix as `Usage.CachedInputTokens` and MUST report no `Usage.CacheWriteTokens`. A miss that repopulates the cache MUST report the written prefix as `Usage.CacheWriteTokens`. The cache MUST NOT price tokens itself; the run's cost is charged from `Usage` against the profile's `Pricing`, and `Pricing.SocializeCacheWrites` folds the write premium into the cached-read rate.

#### Scenario: hit reports cached input
- WHEN a run's model call is served from the cache
- THEN its usage reports the prefix as cached input and reports no cache-write tokens

#### Scenario: repopulating miss reports cache write
- WHEN a run's model call misses and repopulates the cache
- THEN its usage reports the written prefix as cache-write tokens

### Requirement: Cache metrics

A cache hit MUST increment `gohan.cache.hit`, tagged with the cached `Version`, and a cache miss MUST increment `gohan.cache.miss`, subject to the `telemetry` capability's metric policy.

#### Scenario: hit and miss counted
- WHEN a model call is served from the cache
- THEN `gohan.cache.hit` increments; and WHEN it misses, `gohan.cache.miss` increments
