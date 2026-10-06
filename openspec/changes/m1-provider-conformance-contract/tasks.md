# Tasks

## 1. Authoring

- [x] 1.1 Record the assigned model IDs, the two Build-actor claims, and the adapter-actor IDs that remain unearned. Evidence: `design.md` Context, D1, and D5.
- [x] 1.2 Replace undecided seams with ADR-0157 decisions and live registration, Build validation, error, and test entry points. Evidence: `design.md` D3-D5 and Open questions.
- [x] 1.3 Record deterministic fixtures by behavior and `FixtureKind`, including Build seeding for fidelity and static `Extra`. Evidence: `design.md` D5.
- [x] 1.4 Add the model capability delta with scenario sections and normative keywords. Evidence: `specs/model/spec.md`.
- [x] 1.5 Validate this package. Evidence: `openspec validate m1-provider-conformance-contract --strict --no-interactive` → `Change 'm1-provider-conformance-contract' is valid`.
## 2. Shared reference contract implementation

- [x] 2.1 Implement ADR-0157 decision 8: add `NativeSpec.ModelOptions types.ModelOptions` and reject static governed-field collisions in `Stack.resolveNative` before Build succeeds. Preserve dynamic per-request collision handling as the adapter's call-time `ClassPermanent` duty. Verify: `go test -short -timeout 2m ./core/... -run 'Test.*(Fidelity|Extra)'`.
  - evidence: `core/native_resolve.go` merges `spec.ModelOptions` into the assembled `ModelRequest.Options` (per-request wins field by field, `Extra` per key) and keeps the static governed-key rejection; `core/native_extra_merge_test.go` (`TestNativeExtraMerge`) proves both directions. The nine governed keys stay frozen in `types.GovernedModelOptionKeys()`.
- [x] 2.2 Extend `testkit/conformance/fixtures.go` and `model.go` with the fixtures and public suite entry points in design D2–D3. Preserve existing `Model` callers. Verify: `go test -short -timeout 2m ./testkit/conformance -run 'TestModelConformance|TestDefaultFixtures'`.
  - evidence: `testkit/conformance/model.go` carries the cache-breakpoint and schema-unsupported shared assertions, and `fixtureModel` replays both; existing `Model` callers unchanged.
- [x] 2.3 Add deterministic reference-double cases only for shared fixture replay and the two Build-owned behaviors. The double must FAIL when a simulated implementation violates each asserted behavior. Per-key isolation and key redaction are adapter behavior proofs owned by the provider packages, not reference-double claims. Verify: `go test -short -timeout 2m ./testkit/conformance -run 'Test.*(Conformance|Scenario|Build)'`.
  - evidence: `testkit/conformance` fixture replay over the shared assertions, `core/model_build_scenarios_test.go` for the Build-owned behaviours; per-key isolation and key redaction deliberately unclaimed here.
- [x] 2.4 Prove suite assertion sensitivity with deliberately non-conforming reference doubles for the shared assertions. Provider packages prove per-key isolation and key redaction through their adapter integration tests. Verify: `go test -short -timeout 2m ./testkit/conformance -run 'TestModelConformanceRejects'`.
  - evidence: seven negative doubles under `TestModelConformanceRejects`, each rejected by the suite.
- [x] 2.5 Add a public Build test for missing `KindFile` fidelity and static `max_tokens` collision. Dynamic collision must be tested at the adapter call boundary by later adapter owners. Verify: `go test -short -timeout 2m ./core/... -run 'Test.*(Fidelity|Extra)'`.
  - evidence: `core/model_build_scenarios_test.go` (`TestBuildFidelity`, `TestModelBuildScenarios`) already carries both; confirmed green and left byte-identical.

## 3. Adapter conformance obligations

Blocked: `adapter/` holds only `otel`. `adapter/openai` and `adapter/anthropic` do not exist, so these three tasks cannot start here. They stay open and owned by the provider-adapter work that introduces those modules; the suite, fixtures and reference doubles section 2 lands are what those adapters run. Recorded in `design.md` under "Blocker resolution (live seams)".

- [ ] 3.1 OpenAI provider owner runs recorded fixtures for `model.status-to-class`, `model.retry-after-parsed`, `model.cache-explicit-breakpoints`, `model.schema-never-ignored`, `model.usage-estimated`, `model.raw-round-trip`, `model.isolation-per-key`, and `model.key-never-in-telemetry`. Verify: `go test -short -timeout 2m ./adapter/openai/... -run 'TestModelConformance'`.
- [ ] 3.2 Anthropic provider owner runs the same eight registered cases against its recorded fixtures. Verify: `go test -short -timeout 2m ./adapter/anthropic/... -run 'TestModelConformance'`.
- [ ] 3.3 Prove public Send behavior for `Extra` collision, schema refusal, cache markers, Retry-After propagation, Raw fidelity, and key secrecy in each real adapter. Verify: `go test -short -timeout 2m ./adapter/... -run 'Test.*(Conformance|Send)'`.

## 4. Admission and repository gates

- [x] 4.1 Resolve the design blockers by naming the live static/dynamic Extra construction and serialization functions, error representation, and key telemetry emission site before coder admission. Verify: `openspec validate m1-provider-conformance-contract --strict --no-interactive`.
  - evidence: `design.md` "Blocker resolution (live seams)" names all four with `file:line`; `openspec validate m1-provider-conformance-contract --strict --no-interactive` → valid.
- [ ] 4.2 Run repository gates after implementation and adapter owners land. Verify: `task lint && task test && task spec && task api:check`.
  - state: `task lint` 0 issues, `task test` green, `task spec` `missing_in_scope=0`, `task api:check` reports the `otel` module compatible; the gate is re-run once the adapter owners land.
