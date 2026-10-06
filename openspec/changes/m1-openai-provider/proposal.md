# Proposal: m1-openai-provider

## Why

The `m1` acceptance matrix records every `model.*` row as `missing` with "no provider adapter exists; `adapter/openai` and `adapter/anthropic` are not directories" (`openspec/changes/m1/design.md` §2.4). Eight registered model scenario IDs name what an adapter returns, and `m1-provider-conformance-contract` seals them as adapter-actor obligations it does not earn: its tasks 3.1 and 3.3 assign the recorded-fixture run and the public `Send` proofs to the provider packages (`openspec/changes/m1-provider-conformance-contract/tasks.md:34-39`). This change authors the OpenAI adapter package that executes those obligations.

## What Changes

- Add a new Go module `adapter/openai` (module `github.com/victorzhuk/gohan/adapter/openai`), following the `adapter/otel` module layout (`adapter/otel/go.mod:1`), and add it to `go.work`.
- Implement `types.Model` (`core/types/model.go:11`) over the OpenAI chat-completions wire API: request assembly from `ModelRequest` (`core/types/model_request.go:113`), SSE stream decoding, `Usage` fill, and `*ModelError` mapping through `types.ClassifyProviderError` (`core/types/model_profile.go:132`).
- Ship recorded provider responses as adapter `testdata` and a fixture transport, then run the shared suite `conformance.ModelScenarios` (`testkit/conformance/model.go:49`) so the eight registered `model.*` IDs run through the adapter's public `Generate` path.
- Prove public `Send` behaviour for `Extra` collision, schema refusal, cache markers, `Retry-After` propagation, `Raw` fidelity and key secrecy (tasks 3.3).
- Wire the new module into the repository gates: `go.work`, and the `spec:coverage`, `spec:gate`, `test` and `test:race` targets that today enumerate `adapter/otel` only (`Taskfile.yml:20,32,70,82`).
- Record the dependency on `m1-provider-metadata-contract` (the request-metadata field that carries cost tags to the provider) and on `m1-provider-conformance-contract` (the shared suite and fixtures). Neither is consumed as shipped code: `m1-provider-metadata-contract` is scaffolded only, so this change names the field as an implementation prerequisite rather than importing it.

Non-breaking: this adds a module that has no implementation today; no shipped public behavior changes and no release is cut.

## Capabilities

The change is implementation and recorded fixtures. Every behaviour it earns is already normative: `openspec/specs/model/spec.md:265-337` (*Provider adapter contract*, rules 1-9) and `:339-360` (*Provider keys*). The eight registered scenario IDs are claimed once by the shared suite; this change runs them and must not re-claim them (see design D1). The OpenAI-specific wire choices (JSON schema strictness, `extra_body`, `AffinityKey` header, engine priority) are implementation detail, not new spec requirements, so the package declares `skip_specs: true` and adds no spec delta.

### New Capabilities
- (none)

### Modified Capabilities
- (none)

## Impact

New module `adapter/openai` (production package `openai` plus `testdata` recordings and conformance tests). Edited: `go.work` (module list) and `Taskfile.yml` (`spec:coverage`, `spec:gate`, `test`, `test:race` module enumeration; `api:check` already globs `adapter/*`). No `openspec/specs/` edit, no root-module code change, no other change directory touched. Depends on `m1-provider-conformance-contract` (suite and fixtures) and records `m1-provider-metadata-contract` as a prerequisite for cost-tag forwarding. `adapter/openai` does not exist today, so its transport, request assembly and public entry point are named in `design.md` before implementation admission.
