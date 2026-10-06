# Design

## Context

See `proposal.md` — Why. The constraints the approach must respect:

- The contract already fixes the key composition, the `Build` refusals and the never-cache-failures rule (`openspec/specs/cache/spec.md:16-22`); the three registered scenarios are the `CacheStep` rules (`openspec/specs/cache/spec.md:29-42`).
- `core/chains/chain.go:32` already declares `KindCache` as a `StepKind` and the ordering table at `:59-69` governs the model chain; no `CacheStep` value, key builder or store ships (`grep` for `AllowCache`/`cache.Key`/`CacheStep` finds no non-test symbol).
- The only cache-shaped code is the private prefix memo in `std/assembly.go:20-51`, whose key (`prefixID`, `:75-101`) is pointer identity plus scalars and CostTags (`:92-94`), with no manifest hash or prompt version; `cachedPrefix`/`storePrefix` (`:103-115`) memoize one filter-free request.
- The usage fields the pricing seam reads exist: `Usage.CachedInputTokens` and `Usage.CacheWriteTokens` (`core/types/model_types.go:33-43`). The limiter already folds them: `writePrice = p.CacheWrite`, overridden to `p.CachedInput` when `p.SocializeCacheWrites` (`core/chains/limits.go:131-138`).
- `Pricing.SocializeCacheWrites` ships (`core/types/model_profile.go:85`); no run reports cache tokens from a harness cache, so the fold is unreachable — the seam `m1-model-retirement-cost` is blocked on.
- Metric policy is the `telemetry` capability's: `gohan.cache.hit`/`gohan.cache.miss` (`openspec/specs/telemetry/spec.md:111`), usage attrs `gohan.usage.cached_input`/`gohan.usage.cache_write` (`:62`, constants `core/types/telemetry.go:75-76`).

## Goals / Non-Goals

**Goals:**

- Own the harness cache seam as one package, `std/cache`: the `cache.Key` builder and an exact-match memory store.
- Make the assembled request prefix the cached unit, and give a hit/miss a defined effect on `Usage` and on the two counters.
- Enforce the `CacheStep` rules at `Build` so the registered scenarios hold on the public path.

**Non-Goals:**

- Any semantic or similarity cache (ADR-0071; `openspec/specs/cache/spec.md:16`).
- `adapter/redis`; only `adapter/otel` exists and `docs/overview.md:111` places Redis in M3. M1 ships the memory store behind the same seam.
- Pricing: this change emits `Usage`; the fold is `m1-model-retirement-cost`'s, which owns `lifecycle.cache-write-socialized`.
- Provider-side cache markers (`Caps.Cache`, `CacheExplicit` breakpoints) — that is the `model` adapter contract (`openspec/specs/model/spec.md:271`) and belongs to the provider changes.

## Decisions

### D1. The cache lives in `std/cache`; core owns no store

`std/cache` provides `Key` and the exact-match store, matching the contract sentence that names it (`openspec/specs/cache/spec.md:22`). `core` owns only the `CacheStep` kind (`core/chains/chain.go:32`) and the `Build` checks; no cache value type or store is added to `core`, so a host that never uses a cache pays nothing. Alternative rejected: putting the store in `core` would make every embed pay for a feature that is opt-in per flow.

### D2. The key is content-addressed and replaces pointer identity as the authority

`cache.Key(ctx, req)` returns the contract key: tenant, principal scope class, manifest hash (prompts, chains, toolset, model `Version`), resolved prompt version, normalized request. The pointer memo at `std/assembly.go:20-51` is a per-process fast path only; it is never the contract key, because it captures no manifest hash and so cannot make `cache.key-includes-manifest` fail. On a filter-free assembly the memo is consulted first (no hashing cost on the hot path), and a memo miss falls through to the content-keyed store. Alternative rejected: keeping the pointer key as the cache key — it is process-local and identity-blind, so a prompt edit would serve a stale prefix.

### D3. Write and read rules

A write happens only for a run that succeeded and produced a cacheable finish: no entry for `Uncertain`, a guard block, a `ContentPolicy` refusal, or a `max_tokens` finish (`openspec/specs/cache/spec.md:20`, `cache.never-cache-failures`). A read is a lookup by `cache.Key` output; a hit returns the stored prefix and skips re-assembly. Entries are tenant- and scope-class-scoped, so `cache.refused-on-conversation`'s key discipline and the scope separation both hold.

### D4. Usage is the pricing seam; the cache never prices

A hit fills `Usage.CachedInputTokens` for the served prefix and leaves `Usage.CacheWriteTokens` at zero; a repopulating miss fills `Usage.CacheWriteTokens`. The cache computes no cost. `core/chains/limits.go:131-138` already prices from `Usage` and folds the write at the cached-read rate when `Pricing.SocializeCacheWrites` is on, so the two `Usage` fields are the entire interface the retirement change needs. This change does not claim `lifecycle.cache-write-socialized`; it makes the fold reachable.

### D5. Metrics follow the telemetry policy

A hit increments `gohan.cache.hit`, a miss `gohan.cache.miss` (`openspec/specs/telemetry/spec.md:111`); the `cache` capability declares no metric labels of its own, so the policy is the `telemetry` capability's and stays there.

### D6. `Build` enforces the `CacheStep` rules

`Build` refuses a `CacheStep` on a `Conversation` flow, and on any flow whose registered tools include a non-`ReadOnly` effect, unless the flow sets `AllowCache()`; `Explain` prints the opt-out as a warning (`openspec/specs/cache/spec.md:18`). `AllowCache()` is a new public option — no symbol ships today.

### D7. The main-spec hit sentence is amended at apply time

`openspec/specs/cache/spec.md:21` still reads "charged zero usage (ADR-0071)". At apply time this change amends that Contract line: a hit reports the served prefix as `Usage.CachedInputTokens`, is tagged `gohan.cache.hit` with the cached `Version`, and is no longer described as charged zero usage — superseding the ADR-0071 rule. The Contract edit plus the generated types index land with the main-spec edit in the same apply.

## Risks / Trade-offs

- **Content hashing on every assembly** → the pointer memo (D2) short-circuits the common repeat case, so the hash runs only on a memo miss.
- **Prefix staleness across a prompt edit** → the key carries the manifest hash by construction; `cache.key-includes-manifest` fails any implementation that omits it.
- **Cross-tenant leakage** → the key carries tenant and scope class; the scope-separation scenario fails any shared bank.
- **The pointer memo could mask a stale prefix** → it is retained only when the manifest is unchanged in the process; the contract key remains the authority and the memo is a hit fast path, never a source of a key.
- **Redis absent in M1** → the memory store is the only backend, behind the same seam; a later adapter adds a store without a spec change.

## Migration Plan

No stored data migrates. `AllowCache()` is new; a flow that today has no `CacheStep` (none ships) is unaffected. The `Build` refusal is the only breaking edge and it fires only on configurations that set a `CacheStep`, which no shipped code does.

## Open Questions

None. The store backend beyond memory is deferred to M3 by `docs/overview.md:111` and does not change the specs, the approach or the task breakdown.
