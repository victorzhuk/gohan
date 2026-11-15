Risk depth: existing-service-strict. No skipped or flaky scenario passes. A task is done when its scenario ids pass, `task lint`, `task spec` and `task test` are green.

## 1. Contracts

- [x] 1.1 Add ADR-0185 to the accepted set, sync the approved delta, register the proposed scenario ids and regenerate `docs/design/types.md`.
  Acceptance: `task spec:types` clean; ids present in `openspec/scenarios.json` with proposed origin.
- [x] 1.2 Confirm the resolver, failure classification and fallback touch only `std/structured` seams declared in the delta; flag any core change as a scope deviation.
  Acceptance: no edit outside `std/structured`, adapter call-path tests and the change artifacts; `task api:check` clean.

## 2. Behavior

- [x] 2.1 Assert resolver negotiation: `Constrained` only under `Caps.Constrained`, explicit strategy on an uncapped profile fails the flow constructor, and both resolutions appear in the resolved matrix.
  Acceptance: `structured-output.cap-negotiation` passes.
- [x] 2.2 Assert the per-request `ResponseSchema` on an uncapped profile fails with `ClassPermanent` before any request is sent.
  Acceptance: `structured-output.dynamic-schema-without-cap` passes.
- [x] 2.3 Assert adapter-side enforcement through the existing `adapter/openai` and `adapter/anthropic` call-path tests: strict `response_format` under the cap, forced-tool mapping, and a `ClassPermanent` `ModelError` naming the mechanism when the endpoint cannot represent it.
  Acceptance: `structured-output.openai-strict-response-format` and `structured-output.unrepresentable-mechanism` pass.
- [x] 2.4 Assert the `ToolSchema` fallback: the resolver picks it at `Build` for an uncapped profile, and `Extract` validates and repairs the tool-arguments JSON identically.
  Acceptance: `structured-output.tool-schema-fallback` passes.
- [x] 2.5 Assert the representation failure class: a schema beyond the strict-compatibility rules fails `Build` statically naming the construct, and a dynamically supplied one fails the call with `ClassPermanent`.
  Acceptance: `structured-output.unrepresentable-schema` passes.
- [x] 2.6 Assert the bounds path: constrained output violating a value bound repairs under `ValidateRepair` or returns `ErrStructuredOutput` without raising a permanent model error.
  Acceptance: `structured-output.bounds-stay-validation` passes.

## 3. Integration

- [x] 3.1 Run the affected `std/structured` and adapter suites with process deadlines and worker bounds.
  Acceptance: targeted suites green with no skipped subtest in the assigned scenarios.
- [x] 3.2 Verify `task lint`, `task spec`, `task test`, `task api:check`; update the `[Unreleased]` changelog entry.
  Acceptance: all four tasks green; changelog names the hardened constrained surface.
