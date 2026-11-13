# Tasks

Risk depth: existing-service-strict. No skipped, flaky or infrastructure-failed scenario passes. Stop on changed contracts or exhausted recovery limits.

## 1. Contracts

- [ ] 1.1 Add ADR-0181, register the delta's scenario ids in `openspec/scenarios.json` (proposed until admission), sync the delta into the main `engines` and `runtime` specs and regenerate `docs/design/types.md`. Verify `task spec:types` and frozen method compatibility.
  Acceptance: `openspec validate m3-adkgo-runtime --strict` is green and `task spec:types` passes with the new ids registered.
- [ ] 1.2 Create `adapter/adkgo` as its own module with `go.mod` pinning the `google.golang.org/adk/v2` tag named by Q3. Verify the module builds and `task lint` covers it.
  Acceptance: the pinned tag is stated in `go.mod` and every verification in this change cites it.

## 2. Backend and bridges

- [ ] 2.1 Implement the runtime backend over `agent.Context` and the graph engine with `StepGranularity: Turn`, declared through `Runtime.Granularity` and shown by `Explain`. Verify against `runtime.suspend-order` and the Drive contract on the public path.
  Acceptance: `engines.adkgo-runtime-conformance` passes; component events and the turn counter come only from the governed decorators through the run-scoped sink.
- [ ] 2.2 Implement the message bridge codecs and satisfy the shared fidelity contract with them. Verify: the fidelity leg table shows `adkgo` passing with no private adk-go assertion in `testkit/conformance` imports.
  Acceptance: `engines.adkgo-message-fidelity` passes over a full round trip with block order, kinds, origins and blobs equal to the input.
- [ ] 2.3 Implement the tool bridge so foreign tools register, declare their spec and map results through the governed path. Verify against the foreign-tool scenarios with the adkgo backend installed.
  Acceptance: `engines.adkgo-foreign-tool` passes with tool identity, trust marking and result mapping matching the eino leg's assertions.
- [ ] 2.4 Translate component suspension into adk-go's native pause/resume and back into `Suspended`, propagating `ctx` unchanged and honouring cancellation. Verify resume rebuilds state and re-drives from the stored resume input.
  Acceptance: `engines.adkgo-suspend-bridge` passes on the public resume path.

## 3. Fallback and flows

- [ ] 3.1 Implement the declared `Replay` fallback for capabilities the backend lacks: record step results, re-execute `Step` over them, and refuse undeclared silent fallback. Verify against the replay family on the adkgo backend.
  Acceptance: `engines.adkgo-replay-fallback` passes and produces the same `State` sequence up to the suspension point.
- [ ] 3.2 Implement the workflow flows and run the backend-swap and limits suites with the adkgo runtime installed, with unchanged test code.
  Acceptance: `engines.adkgo-workflow-swap` and `engines.adkgo-limits-foreign` pass through each adapter's public path.

## 4. Integration

- [ ] 4.1 Run the full conformance families on all three backends and targeted race checks on suspend and resume. Retain assertion-level evidence, process deadlines and worker bounds.
  Acceptance: `runtime.adkgo-runtime-conformance` and `engines.adkgo-runtime-conformance` green across `native`, `eino`, `adkgo`; no scenario skipped.
- [ ] 4.2 Verify `task lint`, `task spec`, `task test`, `task api:check` and the standalone `adapter/adkgo` checks after inspecting wrappers. Bound command duration and concurrency. Update the `[Unreleased]` changelog entry for shipped behavior.
  Acceptance: all named commands exit green with the new module present.
