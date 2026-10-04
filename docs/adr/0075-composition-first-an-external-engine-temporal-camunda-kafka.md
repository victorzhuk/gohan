# ADR-0075: Composition first: an external engine (Temporal, Camunda, Kafka consumers) owns the business process — process retries, timers, compensation — and calls gohan `Invoke`/`Resume` as activities, service tasks or message handlers; gohan owns bounded runs and their recovery. Engine-driven loops are a later mode, enabled by D76

Status: accepted · Origin: gohan-spec v0.13 decision D75

## Decision

Composition first: an external engine (Temporal, Camunda, Kafka consumers) owns the business process — process retries, timers, compensation — and calls gohan `Invoke`/`Resume` as activities, service tasks or message handlers; gohan owns bounded runs and their recovery. Engine-driven loops are a later mode, enabled by D76.

## Context and evidence

Two systems recovering the same execution is the failure to avoid; the boundary must be explicit.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
