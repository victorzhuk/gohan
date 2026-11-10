# Proposal

## Why

Workspace replacements can hide adapters that do not compile against their declared published root dependency.
Syntactic API comparison misses semantic breaks, and unchecked iterator errors prevent the accepted lint floor.

## What Changes

- Keep all landed adapters pinned to the published root `v0.2.0` (landed by 7069d1f) without third-party upgrades.
- Add verification-only adapter dependency checks with `GOWORK=off`, no replacements, tidy cleanliness and conformance.
- Add CI coverage without producing tags or publications.
- Compare public APIs using standard-library type information and separate module dependency graphs.
- Permit only the narrowly verified historical adapter baseline correction in temporary comparison copies.
- Preserve all iterator assertions while handling fourteen existing unchecked errors.
- Keep examples as a development workspace consumer.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `runtime`: verification of shipped adapter dependency and public API compatibility contracts.

The delta states the approved distribution contract from parent decision 11.
It adds no runtime method, dependency-loading API or new product capability.

## Impact

This child owns parent tasks 8.1-8.6.
It updates adapter manifests, Taskfile, CI, `tools/apicheck`, relevant iterator tests and existing release/compatibility/testing docs.
It integrates all five preceding children before cross-module verification.

## Acceptance

Every landed adapter independently resolves its published root requirement with verified availability and checksums.
No replacement or workspace success can satisfy a release verdict.
Semantic fixtures detect generic method removal, inferred type changes and imported type identity changes.
The checker accepts permitted additive keyed-struct growth and retains v0/v1 reporting tests.
`task api:check`, both `task lint` legs and `task examples:test` satisfy the approved floor.

## Boundaries

No actual release, tag rewrite, production migration or third-party upgrade is authorized.
Root `v0.2.0` was published by owner ruling before this change; this change's machinery publishes nothing and claims no publication.
No compile-time adapter assertion may require an unpublished root optional interface.
Do not edit the independently owned research-report implementation to obtain a passing lint verdict.
Preserve its five original edits and handle iterator failures only outside that ownership boundary.
If a required error occurs inside those edits, coordinate its owner rather than overwrite their work.
Parent integration retains the complete merged-tree and M2 acceptance.
