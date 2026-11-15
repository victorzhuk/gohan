# Structured output

Capability: `structured-output` · Spec v1.1 (ADR-0120) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `structured-output` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### Strategy

`StructuredOutput` strategies (`Constrained`, `ToolSchema`, `ValidateRepair`, option `ReasonFirst`) are declared in `build` (§Strategies) and hardened per the requirements below; schema derivation rules live in `tools`.


### Partial results

`Extract[Out]` and a typed `Conversation` emit `ResultDelta` (`streams`) for the JSON text as it streams. Two entry points read the accumulated text: `std/structured.Partial[Out]` parses it into the declared type `Out` and validates it, and `std/structured.PartialView` parses it into a deep-partial view (`PartialValue`, tolerant `jsontext` mode: unterminated strings and containers are closed) for clients that render as the object fills. `PartialView` values are never validated, returned or stored: `Done.Result` is the only validated value, and it is produced by `Extract`, never by `PartialView`.

```go
// PartialValue is the deep-partial view: containers closed, values unvalidated.
type PartialValue map[string]any

// Partial parses the accumulated text into the declared type and validates it.
func Partial[Out any](acc string) (Out, error)

// PartialView parses the accumulated text into an unvalidated deep-partial view.
func PartialView(acc string) (PartialValue, error)

var ErrStructuredOutput = errors.New("gohan: model output failed schema validation")
```

## Requirements

### Requirement: Structured output

`Extract` MUST validate model output against the declared schema and repair under `ValidateRepair` before returning `ErrStructuredOutput`.

#### Scenario: constrained
ID: `structured-output.constrained`
- WHEN the profile has `Caps.Constrained` and no override
- THEN `ModelOptions.ResponseSchema` is set and no repair turn occurs

#### Scenario: validate and repair
ID: `structured-output.validate-and-repair`
- WHEN the model returns invalid JSON for `Out` under `ValidateRepair(max=2)`
- THEN one repair turn with the validation error is sent; after `max` failures `ErrStructuredOutput` is returned

### Requirement: Partial results

`PartialView` MUST return an unvalidated deep-partial view, and `Done.Result` MUST carry only the value `Extract` validated.

#### Scenario: result delta partial
ID: `structured-output.result-delta-partial`
- WHEN `Extract[Invoice]` streams `{"total": 12, "lines": [{"sku": "A"`
- THEN `PartialView` over the accumulated deltas yields `Total: 12` and one line with `SKU: "A"`

#### Scenario: partial never validated
ID: `structured-output.partial-never-validated`
- WHEN the streamed JSON is cut off by `max_tokens`
- THEN no partial value is returned in `Done.Result`, the turn follows the truncation rule, and clients that consumed `ResultDelta` receive the failure in `Done`

### Requirement: Structured output hardening

`Extract` MUST validate constrained-decoding bounds, and schema-valid refusals MUST be classified `ContentPolicy`.

#### Scenario: bounds validated after constrained decoding
ID: `structured-output.bounds-validated-after-constrained-decoding`
- WHEN a constrained provider returns `quantity: 0` for a schema with `minimum: 1`
- THEN `Extract` returns `ErrStructuredOutput` (or repairs under `ValidateRepair`) and the value never reaches business code

#### Scenario: refusal as JSON
ID: `structured-output.refusal-as-json`
- WHEN the model returns `{"answer": "I cannot assist with that"}` matching the schema
- THEN the call is classified `ContentPolicy` and `gohan.model.refusal_as_json` increments

#### Scenario: truncated tool args
ID: `structured-output.truncated-tool-args`
- WHEN a `ToolUse` arrives with `Finish == max_tokens`
- THEN the tool is not executed, the model receives a truncation result, and the turn is retried once with a larger `MaxTokens`

#### Scenario: reason first
ID: `structured-output.reason-first`
- WHEN an `Agentic` flow uses `Constrained` output
- THEN the request allows an unconstrained reasoning phase before the constrained block, and `Explain` shows `ReasonFirst: on`

#### Scenario: strict schema
ID: `structured-output.strict-schema`
- WHEN `NewTool` derives a schema
- THEN it contains `additionalProperties: false` and an explicit `required` list, and providers with strict mode receive `strict: true`

### Requirement: Constrained capability negotiation

The strategy resolver MUST select `Constrained` only for a profile declaring `Caps.Constrained`, an explicit `Constrained` on a profile without the cap MUST fail the flow constructor at `Build`, and a per-request `ResponseSchema` on a profile without the cap MUST fail the call with `ClassPermanent` before any request is sent.

#### Scenario: resolver honours the cap
ID: `structured-output.cap-negotiation`
- WHEN a flow resolves its `StructuredOutput` strategy against two profiles, one with and one without `Caps.Constrained`
- THEN the resolver picks `Constrained` for the capped profile and `ToolSchema` for the other, and the resolved matrix reports both resolutions

#### Scenario: dynamic schema without the cap fails permanently
ID: `structured-output.dynamic-schema-without-cap`
- WHEN a caller supplies a `ResponseSchema` per request to a profile that lacks `Caps.Constrained`
- THEN the call fails with `ClassPermanent` before any request is sent

### Requirement: Adapter-side constrained enforcement

Under `Constrained` the request MUST carry the schema through the provider's constrained mechanism, and an adapter that cannot represent the mechanism MUST fail with a `ClassPermanent` `ModelError` naming it; silent degradation to unconstrained decoding MUST NOT occur.

#### Scenario: openai strict response format
ID: `structured-output.openai-strict-response-format`
- WHEN a profile with `Caps.Constrained` runs `Constrained` on an OpenAI-compatible endpoint
- THEN the request carries `response_format` with `strict: true` and the derived strict-compatible schema

#### Scenario: unrepresentable mechanism fails permanently
ID: `structured-output.unrepresentable-mechanism`
- WHEN an adapter cannot represent the constrained mechanism for the endpoint
- THEN the call fails with `ClassPermanent` naming the unsupported mechanism and no unconstrained content is sent

### Requirement: App-side fallback resolution

When the resolved profile lacks `Caps.Constrained`, the strategy resolver MUST pick `std/structured.ToolSchema` at `Build`, and `Extract` MUST apply the same app-side validation and repair to the tool-arguments JSON as to constrained output.

#### Scenario: tool schema fallback validates
ID: `structured-output.tool-schema-fallback`
- WHEN a flow runs `Extract[Out]` against a profile without `Caps.Constrained`
- THEN the request emits `Out` as a tool schema, the model's tool arguments are validated against the schema, and invalid output repairs under `ValidateRepair` before `ErrStructuredOutput`

### Requirement: Schema representation failure classes

A schema the provider cannot represent under the strict-compatibility rules MUST fail `Build` when known statically and fail the call with `ClassPermanent` naming the unsupported construct when supplied dynamically; output that matches the schema but violates value bounds MUST stay on the `ValidateRepair`/`ErrStructuredOutput` path.

#### Scenario: unrepresentable schema rejected at build
ID: `structured-output.unrepresentable-schema`
- WHEN a flow declares a schema with recursion depth 5 under `Constrained`
- THEN `Build` fails naming the construct and the depth rule

#### Scenario: bounds stay a validation failure
ID: `structured-output.bounds-stay-validation`
- WHEN a constrained provider returns output matching the schema but violating a value bound
- THEN `Extract` repairs under `ValidateRepair` or returns `ErrStructuredOutput`, and no permanent model error is raised
