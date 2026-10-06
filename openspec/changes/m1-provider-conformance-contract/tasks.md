# Tasks

## 1. Authoring

- [x] 1.1 Record the assigned model IDs, the two Build-actor claims, and the adapter-actor IDs that remain unearned. Evidence: `design.md` Context, D1, and D5.
- [x] 1.2 Replace undecided seams with ADR-0157 decisions and live registration, Build validation, error, and test entry points. Evidence: `design.md` D3-D5 and Open questions.
- [x] 1.3 Record deterministic fixtures by behavior and `FixtureKind`, including Build seeding for fidelity and static `Extra`. Evidence: `design.md` D5.
- [x] 1.4 Add the model capability delta with scenario sections and normative keywords. Evidence: `specs/model/spec.md`.
- [x] 1.5 Validate this package. Evidence: `openspec validate m1-provider-conformance-contract --strict --no-interactive` → `Change 'm1-provider-conformance-contract' is valid`.
## 2. Shared reference contract implementation

- [ ] 2.1 Implement ADR-0157 decision 8: add `NativeSpec.ModelOptions types.ModelOptions` and reject static governed-field collisions in `Stack.resolveNative` before Build succeeds. Preserve dynamic per-request collision handling as the adapter's call-time `ClassPermanent` duty. Verify: `go test -short -timeout 2m ./core/... -run 'Test.*(Fidelity|Extra)'`.
- [ ] 2.2 Extend `testkit/conformance/fixtures.go` and `model.go` with the fixtures and public suite entry points in design D2–D3. Preserve existing `Model` callers. Verify: `go test -short -timeout 2m ./testkit/conformance -run 'TestModelConformance|TestDefaultFixtures'`.
- [ ] 2.3 Add deterministic reference-double cases only for shared fixture replay and the two Build-owned behaviors. The double must FAIL when a simulated implementation violates each asserted behavior. Per-key isolation and key redaction are adapter behavior proofs owned by the provider packages, not reference-double claims. Verify: `go test -short -timeout 2m ./testkit/conformance -run 'Test.*(Conformance|Scenario|Build)'`.
- [ ] 2.4 Prove suite assertion sensitivity with deliberately non-conforming reference doubles for the shared assertions. Provider packages prove per-key isolation and key redaction through their adapter integration tests. Verify: `go test -short -timeout 2m ./testkit/conformance -run 'TestModelConformanceRejects'`.
- [ ] 2.5 Add a public Build test for missing `KindFile` fidelity and static `max_tokens` collision. Dynamic collision must be tested at the adapter call boundary by later adapter owners. Verify: `go test -short -timeout 2m ./core/... -run 'Test.*(Fidelity|Extra)'`.

## 3. Adapter conformance obligations

- [ ] 3.1 OpenAI provider owner runs recorded fixtures for `model.status-to-class`, `model.retry-after-parsed`, `model.cache-explicit-breakpoints`, `model.schema-never-ignored`, `model.usage-estimated`, `model.raw-round-trip`, `model.isolation-per-key`, and `model.key-never-in-telemetry`. Verify: `go test -short -timeout 2m ./adapter/openai/... -run 'TestModelConformance'`.
- [ ] 3.2 Anthropic provider owner runs the same eight registered cases against its recorded fixtures. Verify: `go test -short -timeout 2m ./adapter/anthropic/... -run 'TestModelConformance'`.
- [ ] 3.3 Prove public Send behavior for `Extra` collision, schema refusal, cache markers, Retry-After propagation, Raw fidelity, and key secrecy in each real adapter. Verify: `go test -short -timeout 2m ./adapter/... -run 'Test.*(Conformance|Send)'`.

## 4. Admission and repository gates

- [ ] 4.1 Resolve the design blockers by naming the live static/dynamic Extra construction and serialization functions, error representation, and key telemetry emission site before coder admission. Verify: `openspec validate m1-provider-conformance-contract --strict --no-interactive`.
- [ ] 4.2 Run repository gates after implementation and adapter owners land. Verify: `task lint && task test && task spec && task api:check`.
