# Design

## Context

The `Constrained` strategy composes three frozen seams: `Caps.Constrained` on the profile (`model`), the rejected-combination validation at `Build` (`build`), and the validation/repair machinery in `std/structured` (`structured-output`). The behavior exists in code and scattered scenarios; this change states the composition as one hardening contract and records it in ADR-0185, the first governing ADR for the surface.

## Goals / Non-Goals

- Name the resolver's negotiation rule, the adapter enforcement duty, the app-side fallback and the failure classes as normative requirements on the `structured-output` capability.
- Non-goals: adapter transport changes (`adapter/openai`, `adapter/anthropic` already enforce rule 4), new strategies, new ports or types, changes to `ReasonFirst` semantics beyond the existing scenario.

## Decisions

- **Delta on `structured-output` only.** The strategy is owned by that capability; `build` and `model` already carry their own normative rules and the delta cross-references them instead of restating.
- **Failure classes split by cause.** Schema unrepresentable under the ADR-0072 strict-compatibility rules is a configuration error (static at `Build`, `ClassPermanent` per call when dynamic); value-bound violations of a represented schema stay on the `ValidateRepair`/`ErrStructuredOutput` path. This mirrors D72 and keeps `ErrStructuredOutput` the single validation failure signal.
- **Fallback is resolver-visible.** `ToolSchema` fallback appears in the resolved matrix like any other strategy resolution, so deployments can audit which profiles run app-side loops.

## Risks / Trade-offs

- Tests for the dynamic-schema and unrepresentable-mechanism scenarios run against scripted models and recorded fixtures; no live provider is required.
- Stricter `Build` rejection may surface previously tolerated schemas; mitigation is the existing `NewTool` strict-compatible derivation, which produces representable schemas by construction.

## Migration Plan

No migration: the change specifies existing behavior and hardens it. Scenario ids are registered proposed at admission (task 1.1) and wired to subtests in `std/structured` and the adapter call-path tests that already exist.

## Open Questions

None.
