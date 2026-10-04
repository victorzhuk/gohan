# ADR-0059: `log/slog` everywhere via `WithLogger`; per-run logger with stable attribute keys; never content unless capture is on

Status: accepted · Origin: gohan-spec v0.13 decision D59

## Decision

`log/slog` everywhere via `WithLogger`; per-run logger with stable attribute keys; never content unless capture is on.

## Context and evidence

Users wrap with zerolog/zap handlers in their own code; no logging abstraction of our own.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
