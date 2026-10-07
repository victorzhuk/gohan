# 0159. Run cost tags travel as request metadata, never content

Status: accepted

## Context

`CostTags{Feature, Environment, CostCenter}` ships on `RunInfo` and reaches only local telemetry: the prefix-cache key builder reads the three values, and `gohan.cost.per_run{flow}` prices the run. The `telemetry` spec already says the tags are span attributes and are "forwarded as provider request metadata where the provider supports it", but no field carries them to a provider and no requirement names one. `lifecycle.cost-tags-forwarded` is blocked on exactly that gap, and the `model` provider adapter contract's pass-through list names `Extra`, `AffinityKey` and `Priority` only.

Without a single named field, each adapter would invent its own mapping and the two adapter lanes (`adapter/openai`, `adapter/anthropic`) could not be specified against a common symbol. And without a never-content rule, the same values that must reach a provider could leak into the log records and span content capture the `Redactor` governs.

## Decision

`ModelRequest` gains `Metadata RequestMetadata`, and `RequestMetadata` wraps the existing `CostTags`. The request assembly (`StablePrefix.Assemble`) populates `Metadata.CostTags` from the run. An adapter whose provider documents a request-metadata channel maps the tags onto it in the provider's documented form; an adapter whose provider has no such channel leaves them unsent and does not fail the call. The tags are run attribution, never content: no adapter, telemetry sink or log record emits them as content.

The field sits on the request, not in `ModelOptions`: options are the flow's static, `Build`-validated option set, while cost tags vary per run. The `RequestMetadata` wrapper reuses `CostTags` instead of duplicating its three fields, and leaves room for a later run attribute without widening `ModelRequest` again.

## Consequences

- `lifecycle.cost-tags-forwarded` is unblocked: the requirement names `ModelRequest.Metadata` as the field the provider receives the tags on.
- Both adapter lanes implement against one symbol, so the adapter Send tests can assert the three tags on the outgoing payload.
- A zero-valued `Metadata` never appears on the wire, so record/replay cassettes recorded before the field existed keep their hashes.
- The tags now reach the provider without entering any content sink; the test suite pins this by scanning Debug log records and every span attribute for the tag values.

## Alternatives

- `ModelOptions.Metadata`. Rejected: options are static per flow and validated once at `Build`; cost tags vary per run.
- `Extra["metadata"]`. Rejected: `Extra` is verbatim pass-through with governed-key collision semantics, and a flow could spoof run attribution through it.
- `Caps.RequestMetadata bool` declaring which adapters carry metadata. Rejected: an adapter that cannot carry metadata simply omits the field; a capability flag is a second declaration with no consumer.
- A duplicated `RequestMetadata` shape with its own three fields. Rejected: it would drift from `core/types/identity.go`'s `CostTags`.
