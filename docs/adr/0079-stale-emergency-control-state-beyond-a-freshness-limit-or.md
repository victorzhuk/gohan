# ADR-0079: Stale emergency-control state (beyond a freshness limit, or evaluation failure returning the SDK default) suspends new effects with `AwaitingControl` until fresh state or a maximum wait; in-flight effects follow existing completion rules

Status: accepted · Origin: gohan-spec v0.13 decision D79

## Decision

Stale emergency-control state (beyond a freshness limit, or evaluation failure returning the SDK default) suspends new effects with `AwaitingControl` until fresh state or a maximum wait; in-flight effects follow existing completion rules.

## Context and evidence

OpenFeature returns supplied defaults on failure; a cached "allow" must not outlive an emergency disable.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
