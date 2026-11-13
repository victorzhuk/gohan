# Proposal

## Why

`Constrained` is the structured-output strategy that turns a declared `ResponseSchema` into a provider-side decoding constraint instead of an app-side validation loop. The contract exists in pieces today: `Caps.Constrained` gates the strategy in `build`, `model` rule 4 forbids silent schema drops, and `structured-output` owns validation and repair. No single record states how the three seams compose, and the strategy hardening — negotiation, adapter-side enforcement, the app-side fallback the resolver picks at `Build`, and the failure classes when a profile or a provider cannot honour the schema — is admitted only as scattered scenarios. M3 admits the strategy as a hardened capability per `docs/overview.md` line 115.

## What Changes

- State the negotiation contract on the strategy surface: the resolver selects `Constrained` only when the resolved profile declares `Caps.Constrained`, an explicit strategy on a profile without the cap fails the flow constructor at `Build`, and a `ResponseSchema` supplied per request to a profile without the cap fails the call with `ClassPermanent` before any request is sent.
- State the adapter-side enforcement contract: under `Constrained`, the request MUST carry the schema through the provider's constrained mechanism (`response_format` with `strict: true` on OpenAI-compatible endpoints, one forced tool on Anthropic), and an adapter MUST fail with a `ClassPermanent` `ModelError` naming the unsupported mechanism rather than silently ignoring the schema.
- State the app-side fallback: when the resolved profile lacks `Caps.Constrained`, the strategy resolver at `Build` picks `std/structured.ToolSchema`, and `Extract` still performs app-side validation and repair on the tool-arguments JSON.
- Classify the failure classes: an endpoint that accepts the schema but returns output violating it is a validation failure handled by the existing `ValidateRepair`/`ErrStructuredOutput` path; a provider that cannot represent the schema (unsupported keywords, depth beyond the strict-compatibility rules) is a `ClassPermanent` configuration error at `Build`.

Existing scenarios, ids and error classes are unchanged. Everything stays on frozen seams (`Caps.Constrained`, `ModelOptions.ResponseSchema`, `std/structured.ToolSchema`, `ValidateRepair`, `ErrStructuredOutput`); the change adds no port, no type and no strategy.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `structured-output`: capability negotiation through `Caps.Constrained`, adapter-side enforcement of the constrained mechanism, the app-side `ToolSchema` fallback resolution at `Build`, and the failure classes for profiles and providers that cannot honour a `ResponseSchema`.

## Impact

Implementation touches `std/structured` (resolver and failure classification) and the adapter call paths only where they already enforce rule 4; `adapter/openai` and `adapter/anthropic` transport is out of scope. Spec deltas only; `openspec/scenarios.json` registration and `docs/design/types.md` regeneration happen at admission (task 1.1), per ADR-0171. No frozen contract changes. One ADR, `docs/adr/0185-constrained-decoding-surface.md`, records the decision as the first governing ADR for this surface.

A single delta file in `specs/structured-output/` is sufficient: the strategy is owned by the `structured-output` capability, whose spec already declares the strategies in its Contract and owns validation, repair and the `ReasonFirst` option. The `build` rejected-combination list and `model` rule 4 already carry their own normative statements; this change cross-references them, it does not restate them in a second delta.
