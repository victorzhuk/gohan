# ADR-0071: No semantic cache in v1. A `CacheStep` kind is governed by a cache contract enforced at `Build`: refused on `Conversation` flows and on flows with non-`ReadOnly` tools unless `AllowCache()`; key must include tenant, manifest hash and prompt version; never written for failed, blocked, refused or uncertain results. `std/cache` ships the key builder and an exact-match cache for single-shot recipes only

Status: accepted · Origin: gohan-spec v0.13 decision D71

## Decision

No semantic cache in v1. A `CacheStep` kind is governed by a cache contract enforced at `Build`: refused on `Conversation` flows and on flows with non-`ReadOnly` tools unless `AllowCache()`; key must include tenant, manifest hash and prompt version; never written for failed, blocked, refused or uncertain results. `std/cache` ships the key builder and an exact-match cache for single-shot recipes only.

## Context and evidence

The outer slot bypasses every downstream safeguard; semantic caching misroutes at plausible similarity scores, poisons on hallucinations and contaminates multi-turn context; real hit rates are 10–70 %.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
