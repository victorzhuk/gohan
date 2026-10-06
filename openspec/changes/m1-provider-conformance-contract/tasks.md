# Tasks

## 1. Authoring

- [x] 1.1 Record the ten assigned matrix IDs, the two IDs this change can earn, and the eight adapter-owned IDs that remain unearned. Evidence: `design.md` Context and D1.
- [x] 1.2 Record fixture requirements, existing seams, and unresolved live implementation identifiers. Evidence: `design.md` D2–D5. The unresolved `Extra` assembly and key telemetry call sites block implementation admission.
- [x] 1.3 Add the model capability delta with scenario sections and normative keywords. Evidence: `specs/model/spec.md`.
- [x] 1.4 Validate this package. Evidence: `openspec validate m1-provider-conformance-contract --strict --no-interactive` → `Change 'm1-provider-conformance-contract' is valid`.
## 2. Shared reference contract implementation

- [ ] 2.1 Extend `testkit/conformance/fixtures.go` and `model.go` with the fixtures and public suite entry points in design D2–D3. Preserve existing `Model` callers. Verify: `go test -short -timeout 2m ./testkit/conformance -run 'TestModelConformance|TestDefaultFixtures'`.
- [ ] 2.2 Add deterministic reference-double cases for cache break order and limit, unsupported schema with zero sends, Raw same-provider round trip and cross-provider drop accounting, key isolation, key redaction, static/dynamic Extra collision, and missing fidelity. Use exact registered IDs as subtest names only for the two Build-owned IDs. Verify: `go test -short -timeout 2m ./testkit/conformance -run 'Test.*(Conformance|Scenario)'`.
- [ ] 2.3 Prove suite sensitivity with a deliberately non-conforming reference double for each new assertion family. Verify: `go test -short -timeout 2m ./testkit/conformance -run 'TestModelConformanceRejects'`.
- [ ] 2.4 Add a public Build test for missing `KindFile` fidelity and static `max_tokens` collision. Dynamic collision must be tested at the adapter call boundary by later adapter owners. Verify: `go test -short -timeout 2m ./core/... -run 'Test.*(Fidelity|Extra)'`.

## 3. Adapter conformance obligations

- [ ] 3.1 OpenAI provider owner runs recorded fixtures for `model.status-to-class`, `model.retry-after-parsed`, `model.cache-explicit-breakpoints`, `model.schema-never-ignored`, `model.usage-estimated`, `model.raw-round-trip`, `model.isolation-per-key`, and `model.key-never-in-telemetry`. Verify: `go test -short -timeout 2m ./adapter/openai/... -run 'TestModelConformance'`.
- [ ] 3.2 Anthropic provider owner runs the same eight registered cases against its recorded fixtures. Verify: `go test -short -timeout 2m ./adapter/anthropic/... -run 'TestModelConformance'`.
- [ ] 3.3 Prove public Send behavior for `Extra` collision, schema refusal, cache markers, Retry-After propagation, Raw fidelity, and key secrecy in each real adapter. Verify: `go test -short -timeout 2m ./adapter/... -run 'Test.*(Conformance|Send)'`.

## 4. Admission and repository gates

- [ ] 4.1 Resolve the design blockers by naming the live static/dynamic Extra construction and serialization functions, error representation, and key telemetry emission site before coder admission. Verify: `openspec validate m1-provider-conformance-contract --strict --no-interactive`.
- [ ] 4.2 Run repository gates after implementation and adapter owners land. Verify: `task lint && task test && task spec && task api:check`.
