# ADR-0076: Scoped stepper: core defines `Stepper{Start, Step}` over serializable `State`; `Runtime` embeds it; `gohan.Drive` is the single driver producing events, applying limits, running `Replay` and re-driving `Resuming` runs. `native` steps at effect granularity; `eino`/`adkgo` at turn granularity (declared). No engine-backed adapter in v1

Status: accepted · Origin: gohan-spec v0.13 decision D76

## Decision

Scoped stepper: core defines `Stepper{Start, Step}` over serializable `State`; `Runtime` embeds it; `gohan.Drive` is the single driver producing events, applying limits, running `Replay` and re-driving `Resuming` runs. `native` steps at effect granularity; `eino`/`adkgo` at turn granularity (declared). No engine-backed adapter in v1.

## Context and evidence

`Replay` and resume recovery must be re-execution over recorded results, testable on every backend; the seam for engine-driven loops exists without a core change.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
