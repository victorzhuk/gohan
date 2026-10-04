# ADR-0046: Chains are data: `[]Step{Name, Middleware}`; `Explain` prints resolved chains, assembled prompt, strategy resolution and persistence writes per turn; step failures are wrapped in `StepError{Step}`

Status: accepted · Origin: gohan-spec v0.13 decision D46

## Decision

Chains are data: `[]Step{Name, Middleware}`; `Explain` prints resolved chains, assembled prompt, strategy resolution and persistence writes per turn; step failures are wrapped in `StepError{Step}`.

## Context and evidence

"Three hours to find a bug that took four minutes to fix" is a debugging-opacity failure.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
