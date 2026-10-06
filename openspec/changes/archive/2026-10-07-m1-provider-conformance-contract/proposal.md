# Proposal: m1-provider-conformance-contract

## Why

`testkit/conformance.Model` already checks error classes, usage, truncation, Raw blocks, streaming cancellation and leaks. Its fixtures use descriptive names rather than the registered model scenario IDs. It does not check cache breakpoints, schema refusal, key isolation, key secrecy, or governed `Extra` collisions. No provider adapter exists yet. This change defines the shared contract and a reference double. It earns only the two Build-owned obligations: `model.extra-collision` and `model.undeclared-fidelity`.

## What Changes

- Extend `testkit/conformance` with deterministic fixture data, exact scenario-named suite entry points, and a reference double for contract development.
- Specify the Build contract for static and dynamic `ModelOptions.Extra` collisions and profile fidelity validation.
- Preserve provider adapter behavior as a shared contract. The OpenAI and Anthropic adapter lanes must run the suite against recorded provider responses and earn the eight adapter-owned IDs.
- Do not claim registered coverage for `model.status-to-class`, `model.retry-after-parsed`, `model.cache-explicit-breakpoints`, `model.schema-never-ignored`, `model.usage-estimated`, `model.raw-round-trip`, `model.isolation-per-key`, or `model.key-never-in-telemetry` in this change.

## Capabilities

### Modified Capabilities

- `model`: add reusable conformance contract and Build validation behavior.

## Impact

The authoring contract targets `testkit/conformance/model.go`, `testkit/conformance/fixtures.go`, `core/native_resolve.go`, model request option assembly, provider telemetry, and adapter packages that later consume the suite. No normative `openspec/specs/` amendment is included in this authoring package. The live public implementation seams for dynamic `Extra`, key telemetry emission, and adapter request assembly do not exist yet. This package records those gaps as implementation-admission blockers rather than assigning unresolved APIs to coders.
