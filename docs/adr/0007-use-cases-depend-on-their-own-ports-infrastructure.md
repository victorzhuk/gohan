# ADR-0007: Use cases depend on their own ports; infrastructure implements them over `gohan.Flow[In, Out]`; chat uses `gohan.Conversation`

Status: accepted · Origin: gohan-spec v0.13 decision D7

## Decision

Use cases depend on their own ports; infrastructure implements them over `gohan.Flow[In, Out]`; chat uses `gohan.Conversation`.

## Context and evidence

Backend migration touches wiring only.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
