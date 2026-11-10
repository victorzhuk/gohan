# Tasks

Use the existing-service-strict contract and limits in `design.md`.

## 0. Local pre-fix evidence

- [x] 0.1 Preserve existing module, API and iterator validation baselines before local repairs. Record pre-fix behavior failures for declared-dependency conformance and semantic tools/apicheck fixtures. Include source revision, bounded command, fixture, expected behavior and failing assertion. Reject compilation, missing-command and infrastructure errors as behavior reproductions. Keep dependency availability and lint diagnostics separately identified. Supply this evidence to integration-owned parent 1.3. The declared-dependency record is the observed pre-repin `GOWORK=off` compile failures of adapters declaring root `v0.1.0` (repaired by 7069d1f pinning root `v0.2.0`, green after) and the published `adapter/otel/v0.1.0` manifest declaring root `v0.0.0` with `replace github.com/victorzhuk/gohan => ../..` (git show adapter/otel/v0.1.0:adapter/otel/go.mod).

## 1. Independent dependency and API verification

- [x] 1.1 Keep all landed adapters pinned to published root `v0.2.0` (landed by 7069d1f) without third-party upgrades. Verify published availability/checksums, `GOWORK=off`, no replacements and clean manifests. Preserve examples as a development workspace consumer and verify `task examples:test`. Parent 8.1.
- [x] 1.2 Add a verification-only adapter release task and CI coverage. Verify independent compilation, tidy cleanliness and conformance. Prove replacement refusal with a positive fixture. Verify no tag or publication results. Parent 8.2.
- [x] 1.3 Add semantic API fixtures for generic method removal, inferred variable types, imported type identity and permitted additive keyed-struct growth. Verify the previous checker fails the required assertions before replacement. Parent 8.3.
- [x] 1.4 Implement standard-library type comparison with module-aware dependency/export loading. Keep baseline/candidate graphs separate. Verify and print only the approved historical baseline correction for the published `adapter/otel/v0.1.0` pair (matching root `v0.1.0` release commit). Reject unrelated load errors. Verify all semantic fixtures, existing v0/v1 reporting and `task api:check`. Parent 8.4.
- [x] 1.5 Handle fourteen existing unchecked iterator errors without disabling checks or dropping assertions. Preserve research-report ownership. Verify `task lint` reaches and passes both root and examples legs. Parent 8.5. (Apply-time audit located eleven live sites — seven in the admission set, repaired by d04f450, and four named sites in core/chain_benchmark_test.go and core/shutdown_test.go, repaired by 6ce75f3; the remaining authoring count was already handled by integrated sibling repairs. Reconciliation retained in change evidence.)
- [x] 1.6 Update existing release, compatibility and testing docs and changelog. Verify every documented command exists. Verify no workspace replacement can satisfy release acceptance. Parent 8.6.

## 2. Child acceptance

- [x] 2.1 Verify all landed adapters independently after integrating every behavior child, each resolving published root `v0.2.0`. Preserve root standard-library-only constraints and unchanged third-party versions.
- [x] 2.2 Verify the historical baseline correction requires verified `adapter/otel/v0.1.0` tag/commit evidence (matching root `v0.1.0` release commit) and changes only the approved root requirement (`v0.0.0`+replacement → `v0.1.0`) in a temporary copy.
- [x] 2.3 Supply declared-dependency, semantic compatibility, lint and examples evidence to parent integration. Do not claim publication, M2 completion or the parent broad gate from subset checks.
