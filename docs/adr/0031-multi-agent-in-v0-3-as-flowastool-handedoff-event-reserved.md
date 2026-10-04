# ADR-0031: Multi-agent in v0.3 as `FlowAsTool`; `HandedOff` event reserved now. *(ADR-0095 removed `HandedOff`; sub-flow attribution is carried by `ParentRunID`/`Depth` and subagent events.)*

Status: accepted · Amended by ADR-0091 (sub-flow contract) and ADR-0095 (`HandedOff` removed). · Origin: gohan-spec v0.13 decision D31

## Decision

Multi-agent in v0.3 as `FlowAsTool`; `HandedOff` event reserved now. *(ADR-0095 removed `HandedOff`; sub-flow attribution is carried by `ParentRunID`/`Depth` and subagent events.)*

## Context and evidence

Non-breaking path to handoffs.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
