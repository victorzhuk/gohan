# ADR-0185: The `Constrained` decoding surface

Status: proposed

## Context

`Constrained` structured output lets a provider enforce the declared `ResponseSchema` at decode time instead of relying on app-side validation loops. The contract accumulated across three capabilities: `build` rejects the strategy on profiles without `Caps.Constrained`, `model` rule 4 forbids adapters from silently ignoring a schema, and `structured-output` owns validation, repair and `ErrStructuredOutput`. No ADR records how these seams compose or which failure class applies when a profile or a provider cannot honour a schema. Evidence for the hardening rules: grammar engines treat bounds as advisory, malformed tool arguments are a top-3 pipeline failure, constrained decoding costs up to 10 pp on multi-step reasoning (ADR-0072).

## Decision

1. **Negotiation is capability-gated, never inferred.** The strategy resolver selects `Constrained` only when the resolved profile declares `Caps.Constrained`. An explicit `Constrained` on a profile without the cap fails the flow constructor at `Build` (the existing rejected combination). A `ResponseSchema` supplied per request to a profile without the cap fails the call with `ClassPermanent` before any request is sent — the dynamic case `build` delegates to call time.
2. **Adapters enforce, never degrade.** Under `Constrained` the request carries the schema through the provider's constrained mechanism: `response_format` with `strict: true` on OpenAI-compatible endpoints, one forced tool carrying the schema on Anthropic. An adapter that cannot represent the mechanism fails with a `ClassPermanent` `ModelError` naming the unsupported mechanism. Silent degradation to unconstrained decoding is forbidden.
3. **The app-side fallback is a resolved strategy, not an accident.** When the resolved profile lacks `Caps.Constrained`, the resolver at `Build` picks `std/structured.ToolSchema`; `Extract` performs the same app-side validation and optional `ValidateRepair` on the tool-arguments JSON. Callers observe one strategy per flow × profile in the resolved matrix.
4. **Failure classes are split by cause, not by layer.** Output that matches the schema but violates value bounds is a validation failure handled by `ValidateRepair` and `ErrStructuredOutput` after constrained decoding. A schema the provider cannot represent under the strict-compatibility rules (`additionalProperties: false`, explicit `required`, no recursion, depth ≤ 4, enums ≤ 50) is a configuration error: `Build` fails statically when the schema is known at build time; a dynamically supplied schema fails the call with `ClassPermanent` naming the unsupported construct.

## Consequences

- `std/structured` owns the resolver and the classification; adapters keep only their existing rule-4 enforcement. No port or type changes; `docs/design/compatibility.md` unaffected.
- The strict-compatibility schema rules of ADR-0072 become the conformance boundary every profile and adapter is tested against.
- Providers that gain richer constraint mechanisms later extend enforcement per adapter without touching this surface.

## Risks / Trade-offs

- Constrained decoding costs up to 10 pp on multi-step reasoning; `ReasonFirst` (default on for `Agentic`) keeps the reasoning phase unconstrained.
- Rejecting unrepresentable schemas at `Build` surfaces errors earlier but may reject schemas a tolerant provider would serve; strict-compatible derivation from `NewTool` keeps the common case representable.
