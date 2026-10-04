# ADR-0087: Model retirement: `ModelProfile.Sunset` and `Successor`; error class `Deprecated` normalized from provider headers and errors, never turned into a fallback message, routed to the successor when declared; `Build` fails (configurable to warn) inside `NoticeWindow` before sunset and validates the flow's `ModelOptions` against the successor's `Caps`; the manifest lists pinned versions with sunsets for CI checks

Status: accepted · Origin: gohan-spec v0.13 decision D87

## Decision

Model retirement: `ModelProfile.Sunset` and `Successor`; error class `Deprecated` normalized from provider headers and errors, never turned into a fallback message, routed to the successor when declared; `Build` fails (configurable to warn) inside `NoticeWindow` before sunset and validates the flow's `ModelOptions` against the successor's `Caps`; the manifest lists pinned versions with sunsets for CI checks.

## Context and evidence

82 % of migrations happened after shutdown; ~8 % were silent failures; successors rejected old parameters.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
