# Structured output

## ADDED Requirements

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
