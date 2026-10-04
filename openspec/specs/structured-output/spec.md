# Structured output

Capability: `structured-output` · Spec v1.1 (ADR-0120) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `structured-output` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### Strategy

`StructuredOutput` strategies (`Constrained`, `ToolSchema`, `ValidateRepair`, option `ReasonFirst`) are declared in `build` (§Strategies) and hardened per the requirements below; schema derivation rules live in `tools`.


### Partial results

`Extract[Out]` and a typed `Conversation` emit `ResultDelta` (`streams`) for the JSON text as it streams. `std/structured.Partial[Out]` parses the accumulated text into a deep-partial view (tolerant `jsontext` mode: unterminated strings and containers are closed, the trailing incomplete value is dropped) for clients that render as the object fills. Partial values are never validated, returned or stored: `Done.Result` is the only validated value.

```go
func Partial[Out any](acc string) (Out, error)
```

## Requirements

### Requirement: Structured output

#### Scenario: constrained
ID: `structured-output.constrained`
- WHEN the profile has `Caps.Constrained` and no override
- THEN `ModelOptions.ResponseSchema` is set and no repair turn occurs

#### Scenario: validate and repair
ID: `structured-output.validate-and-repair`
- WHEN the model returns invalid JSON for `Out` under `ValidateRepair(max=2)`
- THEN one repair turn with the validation error is sent; after `max` failures `ErrStructuredOutput` is returned

### Requirement: Partial results

#### Scenario: result delta partial
ID: `structured-output.result-delta-partial`
- WHEN `Extract[Invoice]` streams `{"total": 12, "lines": [{"sku": "A"`
- THEN `Partial[Invoice]` over the accumulated deltas yields `Total: 12` and one line with `SKU: "A"`

#### Scenario: partial never validated
ID: `structured-output.partial-never-validated`
- WHEN the streamed JSON is cut off by `max_tokens`
- THEN no partial value is returned in `Done.Result`, the turn follows the truncation rule, and clients that consumed `ResultDelta` receive the failure in `Done`

### Requirement: Structured output hardening

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
