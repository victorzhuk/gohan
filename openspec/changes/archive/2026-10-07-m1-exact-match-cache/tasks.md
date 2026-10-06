# Tasks

## 1. Record the milestone rows and the seam

- [ ] 1.1 Record the three assigned IDs (`cache.refused-on-conversation`, `cache.key-includes-manifest`, `cache.never-cache-failures`) with their `m1/design.md` §2.9 rows, and the one dependency boundary that is not owned here: `m1-model-retirement-cost` keeps `lifecycle.cache-write-socialized` and consumes this change's `Usage` seam. Verify: `proposal.md` Capabilities and Impact name all three IDs and the boundary; `design.md` D4 states the fold is not claimed here. Evidence: `openspec/changes/m1/design.md:107-113`, `openspec/scenarios.json:284-300`.

## 2. `std/cache` key builder and exact-match store

- [x] 2.1 Create `std/cache` with `Key(ctx, req)` returning the contract key — tenant, principal scope class, manifest hash (prompts, chains, toolset, model `Version`), resolved prompt version, normalized request — and verify a table test that two requests differing only in the manifest hash produce different keys, and two identical requests produce equal keys. Verify: `go test -short -timeout 2m ./std/cache/...`.
  - evidence: `std/cache/cache.go` (`Key` over length-framed SHA-256 segments; empty components render as explicit absent markers); `std/cache/cache_test.go` (`cache.key-includes-manifest`, determinism, tenant/scope/subject/prompt-version scoping).
- [x] 2.2 Implement the exact-match memory store behind the seam: `Get(key)` and `Put(key, prefix)`, plus tenant/scope scoping so a key from one tenant or scope class never resolves for another. Verify: unit tests for hit, miss and scope isolation in `std/cache`.
  - evidence: `std/cache/store.go`, `std/cache/store_test.go` (hit, miss, tenant isolation, scope isolation, put-replaces).
- [x] 2.3 Document the seam in the package doc comment: `std/cache` is the only owner of the key builder and the store, `cache` declares no store in `core`, and Redis is a later store behind the same seam. Verify: `go vet ./std/cache/...` and the doc comment names the seam boundary.
  - evidence: the package doc comment in `std/cache/cache.go`; `go vet ./std/cache/...` clean.

## 3. Assembly wiring

- [x] 3.1 Wire `std/assembly.go` to consult the content-keyed store: keep the pointer memo (`std/assembly.go:20-51`) as the filter-free fast path and fall through to `cache.Key` on a memo miss. Verify: `cache.key-includes-manifest` — a test that assembles with prompt set A, then with a changed manifest hash, observes a miss on the second lookup (`go test -short -timeout 2m ./std/... -run TestPrefixCache`).
  - evidence: `std/assembly.go` (`NewStablePrefix`, `WithCacheStore`, `manifestOf` derives the prompt/toolset/chain identities and the profile version); `std/assembly_cache_test.go` (`cache.key-includes-manifest` — prompt A then prompt B yields two misses, zero hits).
- [x] 3.2 Write the prefix on a repopulating miss and return the stored prefix on a hit without re-assembling. Verify: a test asserts one assembly per key across two identical calls and a byte-identical returned prefix.
  - evidence: `std/assembly_cache_test.go` (`one_assembly_per_key`, `store_hit_returns_the_stored_prefix`). Note recorded in `design.md` D4: the key includes the normalized request, so a store lookup follows the build; the served prefix is the stored one and the fresh build is never served, while the pointer memo serves true zero-build hits on identical inputs.

## 4. `Build` enforcement of `CacheStep`

- [x] 4.1 Add the public `AllowCache()` option and the `Build` checks: refuse a `CacheStep` on a `Conversation` flow, and on any flow whose registered tools include a non-`ReadOnly` effect, unless `AllowCache()` is set; print the opt-out as an `Explain` warning. Verify: `cache.refused-on-conversation` passes — `go test -short -timeout 2m ./core/... -run TestCacheStepRefused`.
  - evidence: `core/build_options.go` (`AllowCache()`), `core/native_resolve.go` (the two refusals), `core/explain.go` + `core/chains/explain.go` (`Explanation.Warnings` carries the waiver); `core/cache_step_test.go` (`cache.refused-on-conversation`).
- [x] 4.2 Apply the never-cache-failures write gate: no entry for an `Uncertain` run, a guard block, a `ContentPolicy` refusal, or a `max_tokens` finish. Verify: `cache.never-cache-failures` passes — a test that ends a run `Uncertain` and observes no entry written.
  - evidence: `core/drive_lifecycle.go` (`writeCacheEntry` gate on the terminal state), `core/cache_step_test.go` (`cache.never-cache-failures`).

## 5. Usage reporting for the pricing seam

- [x] 5.1 Fill `Usage.CachedInputTokens` on a hit and `Usage.CacheWriteTokens` on a repopulating miss, and leave the other at zero; the cache computes no cost. Verify: a test asserts the two `Usage` fields on a hit and on a repopulating miss (`go test -short -timeout 2m ./core/... -run TestCacheUsageReported`), and a follow-up note in `design.md` D4 records that `m1-model-retirement-cost` consumes these fields.
  - evidence: `std/cache` marks the served request with `cache.Outcome`, `core/chains/limits.go` (`ApplyCacheUsage`) folds the mark into the charged usage; `core/cache_usage_test.go` (`TestCacheUsageReported`) asserts hit, repopulating miss and unmarked pricing. `design.md` D4 records the consumer.
- [x] 5.2 Verify the fold is reachable with the seam: a profile with `Pricing.SocializeCacheWrites` charged through `core/chains/limits.go:131-138` prices the repopulating run's write at the cached-read rate. Verify: the pricing test passes; do not re-claim `lifecycle.cache-write-socialized`, which stays with `m1-model-retirement-cost`.
  - evidence: `core/cache_usage_test.go` prices the repopulating write through the same charge path `Pricing.SocializeCacheWrites` reads; the fold itself stays with `m1-model-retirement-cost` task 5.1 and is now unblocked.

- [x] 5.3 Amend `openspec/specs/cache/spec.md:21` at apply time: a hit reports the served prefix as `Usage.CachedInputTokens` and is no longer described as charged zero usage, superseding the ADR-0071 rule (D7), with the generated types index updated in the same edit. Verify: the Contract sentence names `Usage.CachedInputTokens` and no longer says "charged zero usage"; `openspec validate m1-exact-match-cache --strict --no-interactive` exits 0.
  - evidence: applied at archive time with the delta sync; the Contract sentence names `Usage.CachedInputTokens` and the cached `Version` tag, and no longer says "charged zero usage".

## 6. Observability

- [x] 6.1 Increment `gohan.cache.hit` on a hit and `gohan.cache.miss` on a miss, per the `telemetry` metric policy (`openspec/specs/telemetry/spec.md:111`). Verify: a test observes both counters once each across a miss-then-hit sequence; `cache` declares no metric label of its own.
  - evidence: `std/assembly.go` (`countCache`), `std/telemetry/metrics.go` (`MetricCacheHit`, `MetricCacheMiss`), `std/assembly_cache_test.go` (miss-then-hit observes each once; zero value emits none).

## 7. Integration verification

- [x] 7.1 Run the three registered scenario tests through the public path and the package tests. Verify: `task test` green and the three `cache.*` scenario subtests pass.
  - evidence: `task test` green; `task spec:gate GATE=M1` reports no missing `cache.*` id and `unregistered=0`.
- [x] 7.2 Validate the change package. Verify: `openspec validate m1-exact-match-cache --strict --no-interactive` exits 0, and the delta keeps the three registered IDs with their WHEN/THEN lines verbatim.
  - evidence: `Change 'm1-exact-match-cache' is valid`; the delta keeps the three IDs byte-identical.

## Workflow follow-up

- Archive the change after the project's review requirements are satisfied.
- Verify the archived result and that `cache` main spec carries the new requirements.
