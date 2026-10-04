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
- Hits are emitted as one chunk, charged zero usage (ADR-0071), and tagged `gohan.cache.hit` with the cached `Version`; misses record `gohan.cache.miss`.
- `std/cache` provides the key builder and an exact-match cache (memory, Redis via `adapter/redis`) intended for `Extract`, `Classify` and `Route`. Similarity-based matching is user-supplied and must set an explicit threshold and a per-domain invalidation hook; the contract above still applies.


## Requirements

### Requirement: Cache contract

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
