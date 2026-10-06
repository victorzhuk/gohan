# Spec Delta

## MODIFIED Requirements

### Requirement: Provider adapter contract

The implementation MUST carry the *Provider adapter contract* on the public adapter path. The model capability owns provider tool usage accounting: an adapter MUST count each provider-executed tool call it observes in a response under the tool's name in `Usage.ProviderToolCalls`, and the run's cost MUST include that count priced at `Pricing.ProviderCall[name]` (the pricing path already exists at `core/chains/limits.go:142-144`; the missing step is the adapter fill, recorded at `openspec/changes/m1/design.md:98`). A provider tool block MUST NOT be treated as `Raw`: the adapter MUST convert it per `tools` *Provider-executed tools* rule 4, while payloads the provider requires back stay inside the result as `Raw` and round-trip per rule 8 below. `ModelRequest` MUST carry a `Metadata RequestMetadata` field, and `RequestMetadata` MUST carry `CostTags CostTags` — the run's `RunInfo.CostTags` (`core/types/identity.go:42,80`). The provider request assembly MUST populate `ModelRequest.Metadata.CostTags` from the run; the prefix-cache key builder already reads the same three values (`std/assembly.go:92-94`). An adapter whose provider documents a request-metadata channel MUST map `Metadata.CostTags` onto that channel in the provider's documented form. An adapter whose provider has no such channel MUST leave the values unsent, MUST NOT fail the call, and MUST NOT synthesise a channel. The values are run attributes, never model content: no adapter, telemetry sink or log record MUST emit them as content — they MUST NOT appear in span content capture or in any log record. `Metadata.CostTags` is not `Extra`: `Extra` is a flow's verbatim pass-through (this spec, *Pass-through*), while `Metadata` is harness-assembled run attribution. `adapter/openai` and `adapter/anthropic` are the implementations of this field; each names the concrete Send function that performs the mapping when it is authored. These obligations bind the adapter call path; the shared conformance suite that exercises them is owned by `m1-provider-conformance-contract` and run by `m1-openai-provider` and `m1-anthropic-provider`.

#### Scenario: status maps to class
ID: `model.status-to-class`
- WHEN the conformance fixtures replay 429, 500, 401, 400, a context-length error and a retired-model error
- THEN the adapter returns `*ModelError` with `ClassRateLimited`, `ClassTransient`, `ClassAuth`, `ClassPermanent`, `ClassContextOverflow`, `ClassDeprecated` respectively

#### Scenario: retry-after parsed
ID: `model.retry-after-parsed`
- WHEN a 429 carries `Retry-After: 7`
- THEN `ModelError.RetryAfter == 7s`; `ClassRateLimited` is never retried on the same endpoint, so the request fails over at once and, when every endpoint is exhausted, the value is returned to the client as `Retry-After`

#### Scenario: undeclared fidelity fails build
ID: `model.undeclared-fidelity`
- WHEN a flow's tools can return `KindFile` and the profile's `Caps.Fidelity` has no entry for it
- THEN `Build` fails naming the profile and the kind

#### Scenario: explicit cache breakpoints
ID: `model.cache-explicit-breakpoints`
- WHEN a request carries three `CacheBreak`s on a profile with `CacheExplicit` and `CacheBreakpoints: 2`
- THEN two provider markers are sent in order, one break is dropped, and `Usage.CacheWriteTokens` is filled from the response

#### Scenario: schema never ignored
ID: `model.schema-never-ignored`
- WHEN a `ResponseSchema` reaches an adapter whose profile lacks `Caps.Constrained`
- THEN the call fails with `ClassPermanent` before any request is sent

#### Scenario: usage estimated when absent
ID: `model.usage-estimated`
- WHEN a provider response carries no usage block
- THEN `Usage.Estimated` is true and token counts are non-zero estimates

#### Scenario: extra cannot override governed fields
ID: `model.extra-collision`
- WHEN `Extra` contains `max_tokens`
- THEN `Build` fails for a static value and the call returns `ClassPermanent` for a dynamic one

#### Scenario: raw round-trip
ID: `model.raw-round-trip`
- WHEN a provider returns a block kind gohan does not model
- THEN it is stored as `Raw{Provider}`, re-sent unchanged to the same provider and dropped for another with `gohan.block.dropped{kind=raw}`

#### Scenario: provider tool usage
ID: `model.provider-tool-usage`
- WHEN a response contains two provider search calls
- THEN `Usage.ProviderToolCalls["web_search"]` is 2 and the run's cost includes `2 × Pricing.ProviderCall["web_search"]`
