# ADR-0061: `adapter/adkgo` targets `google.golang.org/adk/v2` (`agent.Context`, graph engine, native pause/resume)

Status: accepted · Origin: gohan-spec v0.13 decision D61

## Decision

`adapter/adkgo` targets `google.golang.org/adk/v2` (`agent.Context`, graph engine, native pause/resume).

## Context and evidence

adk-go v2 is the maintained line; v1 receives backports only.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
