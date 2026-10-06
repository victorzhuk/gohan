# Proposal: m1-provider-metadata-contract

## Why

The `model` provider adapter contract names no request-metadata field, so the
`lifecycle` cost requirement cannot forward a run's cost tags to a provider:
`lifecycle.cost-tags-forwarded` is blocked
(`openspec/changes/m1/design.md:167`). `CostTags{Feature, Environment, CostCenter}`
ships (`core/types/identity.go:42`) and reaches only the prefix-cache key builder
(`std/assembly.go:92-94`); `openspec/specs/telemetry/spec.md:73` says the tags are
"forwarded as provider request metadata where the provider supports it" without
naming the field that carries them. Two adapter changes (`m1-openai-provider`,
`m1-anthropic-provider`) must implement against that field, so it must be named
once, precisely.

## What Changes

- Name the request-metadata field. `ModelRequest` (`core/types/model_request.go:11`)
  gains `Metadata RequestMetadata`, and `RequestMetadata` carries the run's
  `CostTags` (`core/types/identity.go:42`).
- Make the forwarding duty normative on the `model` provider adapter contract: the
  request assembly populates `Metadata.CostTags` from the run; an adapter whose
  provider documents a request-metadata channel maps the tags onto it in the
  provider's documented form; an adapter whose provider has no such channel leaves
  them unsent without failing the call.
- State that the metadata values are run attributes, never content: no adapter,
  telemetry sink or log record emits them as content.
- Add the forwarding text the `lifecycle` cost requirement needs, naming the field.
  The registered scenario `lifecycle.cost-tags-forwarded` stays owned by
  `m1-model-retirement-cost`, which consumes this field.

**BREAKING**: none. `ModelRequest` gains a zero-valued field; every existing
construction stays valid.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `model`: the "Provider adapter contract" requirement gains a normative
  request-metadata field and its forwarding duty, with scenarios for a provider
  that accepts metadata and one that does not.
- `lifecycle`: the "Retirement, schema evolution, cost" requirement's cost-tags
  paragraph names `ModelRequest.Metadata` as the field the provider receives the
  tags on. No scenario ID is claimed.

## Impact

Specs: `openspec/specs/model/spec.md` (the "Provider adapter contract" requirement,
and the `ModelRequest` block in its Contract section when the change is applied)
and `openspec/specs/lifecycle/spec.md` (the cost-tags paragraph). Implementation:
`core/types/model_request.go` (`ModelRequest`, `RequestMetadata`), `std/assembly.go`
(the request assembly seam), and the two adapter modules `adapter/openai` and
`adapter/anthropic`, neither of which exists yet (`adapter/` holds `otel` only).
The change consumes the shipped run identity field `RunInfo.CostTags`
(`core/types/identity.go:80`). It claims no metric: `gohan.cost.per_run{flow}`
stays with `m1-model-retirement-cost`.
