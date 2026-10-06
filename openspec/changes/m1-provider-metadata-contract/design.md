# Design: m1-provider-metadata-contract

## Context

See `proposal.md` for motivation. The milestone record assigns no scenario ID
to this change: it exists to unblock `lifecycle.cost-tags-forwarded`
(`openspec/changes/m1/design.md:167`), which `m1-model-retirement-cost` owns and
consumes, and to give `m1-openai-provider` and `m1-anthropic-provider` a field
to implement against.

State that exists today, read this session:

- `CostTags{Feature, Environment, CostCenter}` ships
  (`core/types/identity.go:42`); `RunInfo.CostTags` carries it on the run
  (`core/types/identity.go:80`).
- The prefix-cache key builder reads all three values
  (`std/assembly.go:92-94`, the `feature`/`cluster`/`costCtr` fields of
  `prefixID`). Nothing forwards them to a provider.
- `ModelRequest` is the fully assembled request one model call receives
  (`core/types/model_request.go:11`) with `System`, `Tools`, `Messages` and
  `Options` (`:26`). It has no metadata field.
- The `model` Provider adapter contract is a Contract block
  (`openspec/specs/model/spec.md:265`) whose requirement
  (`openspec/specs/model/spec.md:292`) carries nine scenarios and a pass-through
  list that names `Extra`, `AffinityKey` and `Priority` only.
- `telemetry` says "`CostTags` and `Residency` are span attributes and are
  forwarded as provider request metadata where the provider supports it"
  (`openspec/specs/telemetry/spec.md:73`) without naming the field.
- `adapter/` holds only `otel`; `adapter/openai` and `adapter/anthropic` do not
  exist, so the mapping is specified, not implemented.

## Goals / Non-Goals

**Goals:**

- Name one request-metadata field precisely enough that both adapter lanes
  implement against the same symbol.
- Bind the forwarding duty: who populates the field, who sends it, who must not,
  and the rule that the values are never content.
- Give `lifecycle`'s cost requirement the field name it needs, without claiming
  its scenario.

**Non-Goals:**

- No adapter module and no provider networking. Each adapter package names its
  concrete Send function; this change fixes the field and the duty.
- No new metric. `gohan.cost.per_run{flow}` stays `m1-model-retirement-cost`'s.
- No `Caps` flag declaring metadata support: an adapter that cannot carry
  metadata simply does not receive it (D4).
- No change to `Extra`, `AffinityKey` or `Priority` pass-through semantics.

## Decisions

### D1. The field is `ModelRequest.Metadata`, typed `RequestMetadata`

`ModelRequest` gains `Metadata RequestMetadata`. Rationale: the value is
per-run run attribution, not a flow-static knob, so it does not belong in
`ModelOptions`, which the conformance contract treats as the flow's static
option set validated at `Build` (`openspec/changes/m1-provider-conformance-contract/design.md`
D4). Alternatives: `ModelOptions.Metadata` — rejected, options are static and
validated once while cost tags vary per run; `Extra["metadata"]` — rejected,
`Extra` is verbatim pass-through with governed-key collision semantics and would
let a flow spoof run attribution.

### D2. `RequestMetadata` reuses `CostTags`

`type RequestMetadata struct { CostTags CostTags }`. Rationale: the three fields
are defined once, in `core/types/identity.go:42`, and the scenario names exactly
those three tags. A duplicated shape would drift. The wrapper leaves room for a
later run attribute without widening `ModelRequest` again.

### D3. The request assembly populates it; the prefix-key builder already reads it

`StablePrefix.Assemble` (`std/assembly.go`) sets
`req.Metadata.CostTags = in.Run.CostTags`. The prefix-key builder already reads
the same three values (`std/assembly.go:92-94`); populating the request is the
missing half the sibling design names as "the provider request assembly
attaches the run's `CostTags`"
(`openspec/changes/m1-model-retirement-cost/design.md` D4). Alternatives: an
adapter reading `RunInfo` from `ctx` — rejected, the field must be on the
assembled request so an adapter stays a pure request-to-provider mapper and so
the value is observable before `Send`.

### D4. The adapter duty is map-or-omit, gated on the provider's documented channel

An adapter whose provider documents a request-metadata channel maps
`Metadata.CostTags` onto it in the provider's documented form; an adapter whose
provider has no such channel leaves the values unsent and does not fail the
call. Rationale: the contract must not invent a channel, and a `Caps` flag would
be a second declaration with no consumer. Alternatives: `Caps.RequestMetadata
bool` — rejected as unnecessary; a `map[string]ProviderToolCap`-style
declaration — rejected, no approval or per-key semantics apply.

### D5. The values are run attributes, never content

The requirement forbids the tags in span content capture and in log records. The
`telemetry` spec already makes `CostTags` a span attribute
(`openspec/specs/telemetry/spec.md:73`); forwarding them to a provider is not
content capture. Rationale: the metadata must reach a provider without entering
the content sinks the `Redactor` governs.

### D6. The `lifecycle` delta carries the complete requirement and names the field

The delta modifies "Retirement, schema evolution, cost" with its complete
description (the notice-window, deprecation, successor and socialisation text
that `m1-model-retirement-cost` authored) and its seven scenarios verbatim,
changing only the cost-tags paragraph to name `ModelRequest.Metadata`. The
COMPLETE-block rule mandates the full text; omitting it would erase the
sibling's normative prose if this change is applied second. No scenario is
claimed here: `lifecycle.cost-tags-forwarded` stays with
`m1-model-retirement-cost`.

### D7. The two adapter lanes are the implementations

`adapter/openai` and `adapter/anthropic` are marked implementers of the field in
the requirement. Neither module exists (`adapter/` holds `otel`), so each
adapter package names its concrete Send function when authored.

## Risks / Trade-offs

- **Two changes modify the same `lifecycle` requirement** → the delta applied
  last wins. Mitigation: this change's block is a superset of the sibling's, so
  no normative text is lost if this change is applied second; if the sibling is
  applied second, the field name remains in the `model` requirement, which is
  the authority for the field.
- **The adapter lanes have no module yet** → the map-or-omit duty is specified
  and left as an implementation prerequisite; the field and the assembly
  population land in core, the provider mapping lands with each adapter.
- **Field-name drift against the sibling's wording** → the sibling design calls
  the field "the request-metadata field on the profile"; the field is on the
  request, not the profile, and this package is the authority. The `model`
  requirement names the exact location.

## Migration Plan

1. Add `Metadata RequestMetadata` to `ModelRequest` and define
   `RequestMetadata` (`core/types/model_request.go:11`).
2. Populate `Metadata.CostTags` from `in.Run.CostTags` in `StablePrefix.Assemble`
   (`std/assembly.go`).
3. When the change is applied: edit the main `model` Contract `ModelRequest`
   block, write the ADR, and regenerate `docs/design/types.md`
   (`openspec/project.md:27`). The root module stays pre-v1 and no release is
   cut; the change adds a zero-valued field, so no `CHANGELOG.md` Breaking entry
   is required.
4. Each adapter maps `Metadata.CostTags` onto its provider's documented channel
   and names the Send function that does so.

Rollback: removing the field and the one assembly assignment restores the
previous shape; no adapter depends on it until one is authored.

## Open Questions

None. The field name, its shape, its population site and the adapter duty are
fixed above; the concrete Send function names are each adapter package's own
decision and do not change this contract.
