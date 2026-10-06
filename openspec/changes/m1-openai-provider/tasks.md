# Tasks

## 1. Authoring

- [x] 1.1 Record the eight adapter-owned `model.*` IDs this change runs without re-claiming, the two Build-owned IDs it does not claim, and the dependency on `m1-provider-conformance-contract` and `m1-provider-metadata-contract`. Evidence: `proposal.md` Why/Capabilities; `design.md` Context and D10.
- [x] 1.2 Decide the module layout, public API, request assembly, streaming/usage, error, cache, schema, `Extra`, `Raw`, key-secrecy and fixture-harness shapes. Evidence: `design.md` D1-D9.
- [x] 1.3 Declare `skip_specs: true` with the reason no spec-level behaviour changes. Evidence: `.openspec.yaml`; `proposal.md` Capabilities.
- [x] 1.4 Validate this package. Evidence: `openspec validate m1-openai-provider --strict --no-interactive` → `Change 'm1-openai-provider' is valid`.

## 2. Module and public API

- [ ] 2.1 Create `adapter/openai/go.mod` (module `github.com/victorzhuk/gohan/adapter/openai`, `go 1.27.1`, require the root module) and add `./adapter/openai` to `go.work`. Verify: `go build ./...` from `adapter/openai`.
- [ ] 2.2 Implement package `openai` per `design.md` D1: `Config`, `Transport`, `Provider`, `New` (profile validation), `Profile`, `Generate`. Verify: `go build ./...` from `adapter/openai`.
- [ ] 2.3 Implement `request.go` assembly (D2) and `stream.go` SSE decode, `Usage` fill and estimate (D3), and `errors.go` mapping a non-2xx response body and `Retry-After`/`x-ratelimit-reset-*` through `types.ClassifyProviderError` into `*types.ModelError`. Verify: `go test -timeout 2m -short ./...` from `adapter/openai`.
- [ ] 2.4 Implement the capability mappings per `design.md`: dynamic `Extra` governed-key refusal (D4), schema refusal/emission (D5), cache modes and `gohan.cache.breakpoints_dropped` (D6), `Raw` round-trip to `openai` and drop elsewhere (rule 8), key resolution, `Usage.KeyID` and token secrecy (D7). Verify: `go test -timeout 2m -short ./...` from `adapter/openai`.

## 3. Recorded fixtures and conformance harness

- [ ] 3.1 Add `adapter/openai/testdata/*.json` recordings for every fixture in `conformance.DefaultFixtures()` (`testkit/conformance/fixtures.go:64-168`): the seven error fixtures with the recorded status/code/`Retry-After`, the usage and no-usage responses, the truncated tool call, the unmodelled-block round trip, the cache response, and the schema-unsupported refusal. Verify: `go test -timeout 2m -short -run TestModelConformance ./...` from `adapter/openai`.
- [ ] 3.2 Add `conformance_test.go` (package `openai_test`) with the `fixtureTransport` that reads `gohan.conformance.fixture` from the outgoing body and returns the recording, and `newModel func(conformance.Fixture) types.Model` (D8).
- [ ] 3.3 Add `TestModelConformance` calling `conformance.ModelScenarios(t, newModel, fixtures)` so `model.status-to-class`, `model.retry-after-parsed`, `model.cache-explicit-breakpoints`, `model.schema-never-ignored`, `model.usage-estimated` and `model.raw-round-trip` run through the adapter's public `Generate` path. Verify: `go test -timeout 2m -short -run TestModelConformance ./...` from `adapter/openai`.
- [ ] 3.4 Add `TestProviderKeyIsolation` (subtest `model.isolation-per-key`) and `TestKeyNeverInTelemetry` (subtest `model.key-never-in-telemetry`) with a `ProviderKeySource` double and a sentinel token, per D8. Verify: `go test -timeout 2m -short -run 'TestProviderKeyIsolation|TestKeyNeverInTelemetry' ./...` from `adapter/openai`.

## 4. Public Send proofs (task 3.3 obligations)

- [ ] 4.1 Prove dynamic `Extra` collision returns `ClassPermanent` before any transport call, naming the governed key. Verify: `go test -timeout 2m -short -run Test.*ExtraCollision ./...` from `adapter/openai`.
- [ ] 4.2 Prove schema refusal before sending on a non-`Constrained` profile and the emitted `response_format` on a `Constrained` one. Verify: `go test -timeout 2m -short -run Test.*Schema ./...` from `adapter/openai`.
- [ ] 4.3 Prove cache markers: `CacheExplicit` emits up to `Caps.CacheBreakpoints` markers in order, drops the rest with `gohan.cache.breakpoints_dropped`, and fills `CacheWriteTokens`; `CacheAuto`/`CacheNone` drop breaks. Verify: `go test -timeout 2m -short -run Test.*Cache ./...` from `adapter/openai`.
- [ ] 4.4 Prove `Retry-After` propagation into `ModelError.RetryAfter` and `Raw` fidelity on the recorded round trip. Verify: `go test -timeout 2m -short -run 'Test.*RetryAfter|Test.*Raw' ./...` from `adapter/openai`.

## 5. Repository gates

- [ ] 5.1 Wire the module into the gates per `design.md` D9: add the `adapter/openai` test lines to `Taskfile.yml` `spec:coverage`, `spec:gate`, `test` and `test:race`; `api:check` already globs `adapter/*`. Verify: `task spec:gate GATE=M1` reports `missing_in_scope=0` for the eight `model.*` IDs.
- [ ] 5.2 Run repository gates after implementation lands. Verify: `task lint && task test && task spec && task api:check`.

## 6. Dependencies and admission

- [ ] 6.1 Record that cost-tag forwarding stays unimplemented until `m1-provider-metadata-contract` lands the request-metadata field; the adapter's `Config` carries the seam only. Evidence: `design.md` D10.
- [ ] 6.2 Resolve implementation admission by naming the live seam for every obligation: the transport, the request-body fixture key, `types.ClassifyProviderError` as the single classifier, and the two dedicated ID-named tests for the suite's unasserted IDs. Verify: `openspec validate m1-openai-provider --strict --no-interactive`.
