# Tasks

## 1. Record the milestone rows and the seam

- [ ] 1.1 Record the three assigned IDs (`cache.refused-on-conversation`, `cache.key-includes-manifest`, `cache.never-cache-failures`) with their `m1/design.md` §2.9 rows, and the one dependency boundary that is not owned here: `m1-model-retirement-cost` keeps `lifecycle.cache-write-socialized` and consumes this change's `Usage` seam. Verify: `proposal.md` Capabilities and Impact name all three IDs and the boundary; `design.md` D4 states the fold is not claimed here. Evidence: `openspec/changes/m1/design.md:107-113`, `openspec/scenarios.json:284-300`.

## 2. `std/cache` key builder and exact-match store

- [ ] 2.1 Create `std/cache` with `Key(ctx, req)` returning the contract key — tenant, principal scope class, manifest hash (prompts, chains, toolset, model `Version`), resolved prompt version, normalized request — and verify a table test that two requests differing only in the manifest hash produce different keys, and two identical requests produce equal keys. Verify: `go test -short -timeout 2m ./std/cache/...`.
- [ ] 2.2 Implement the exact-match memory store behind the seam: `Get(key)` and `Put(key, prefix)`, plus tenant/scope scoping so a key from one tenant or scope class never resolves for another. Verify: unit tests for hit, miss and scope isolation in `std/cache`.
- [ ] 2.3 Document the seam in the package doc comment: `std/cache` is the only owner of the key builder and the store, `cache` declares no store in `core`, and Redis is a later store behind the same seam. Verify: `go vet ./std/cache/...` and the doc comment names the seam boundary.

## 3. Assembly wiring

- [ ] 3.1 Wire `std/assembly.go` to consult the content-keyed store: keep the pointer memo (`std/assembly.go:20-51`) as the filter-free fast path and fall through to `cache.Key` on a memo miss. Verify: `cache.key-includes-manifest` — a test that assembles with prompt set A, then with a changed manifest hash, observes a miss on the second lookup (`go test -short -timeout 2m ./std/... -run TestPrefixCache`).
- [ ] 3.2 Write the prefix on a repopulating miss and return the stored prefix on a hit without re-assembling. Verify: a test asserts one assembly per key across two identical calls and a byte-identical returned prefix.

## 4. `Build` enforcement of `CacheStep`

- [ ] 4.1 Add the public `AllowCache()` option and the `Build` checks: refuse a `CacheStep` on a `Conversation` flow, and on any flow whose registered tools include a non-`ReadOnly` effect, unless `AllowCache()` is set; print the opt-out as an `Explain` warning. Verify: `cache.refused-on-conversation` passes — `go test -short -timeout 2m ./core/... -run TestCacheStepRefused`.
- [ ] 4.2 Apply the never-cache-failures write gate: no entry for an `Uncertain` run, a guard block, a `ContentPolicy` refusal, or a `max_tokens` finish. Verify: `cache.never-cache-failures` passes — a test that ends a run `Uncertain` and observes no entry written.

## 5. Usage reporting for the pricing seam

- [ ] 5.1 Fill `Usage.CachedInputTokens` on a hit and `Usage.CacheWriteTokens` on a repopulating miss, and leave the other at zero; the cache computes no cost. Verify: a test asserts the two `Usage` fields on a hit and on a repopulating miss (`go test -short -timeout 2m ./core/... -run TestCacheUsageReported`), and a follow-up note in `design.md` D4 records that `m1-model-retirement-cost` consumes these fields.
- [ ] 5.2 Verify the fold is reachable with the seam: a profile with `Pricing.SocializeCacheWrites` charged through `core/chains/limits.go:131-138` prices the repopulating run's write at the cached-read rate. Verify: the pricing test passes; do not re-claim `lifecycle.cache-write-socialized`, which stays with `m1-model-retirement-cost`.

- [ ] 5.3 Amend `openspec/specs/cache/spec.md:21` at apply time: a hit reports the served prefix as `Usage.CachedInputTokens` and is no longer described as charged zero usage, superseding the ADR-0071 rule (D7), with the generated types index updated in the same edit. Verify: the Contract sentence names `Usage.CachedInputTokens` and no longer says "charged zero usage"; `openspec validate m1-exact-match-cache --strict --no-interactive` exits 0.

## 6. Observability

- [ ] 6.1 Increment `gohan.cache.hit` on a hit and `gohan.cache.miss` on a miss, per the `telemetry` metric policy (`openspec/specs/telemetry/spec.md:111`). Verify: a test observes both counters once each across a miss-then-hit sequence; `cache` declares no metric label of its own.

## 7. Integration verification

- [ ] 7.1 Run the three registered scenario tests through the public path and the package tests. Verify: `task test` green and the three `cache.*` scenario subtests pass.
- [ ] 7.2 Validate the change package. Verify: `openspec validate m1-exact-match-cache --strict --no-interactive` exits 0, and the delta keeps the three registered IDs with their WHEN/THEN lines verbatim.

## Workflow follow-up

- Archive the change after the project's review requirements are satisfied.
- Verify the archived result and that `cache` main spec carries the new requirements.
