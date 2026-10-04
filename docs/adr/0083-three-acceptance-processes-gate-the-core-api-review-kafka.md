# ADR-0083: Three acceptance processes gate the core API review: Kafka refunds (redelivery, lost acks), Temporal travel fulfilment (supplier uncertainty, compensation ownership), Camunda invoice approval (long waits, revoked authority, duplicate workers), each also exercised with a flag outage and a definition version change; plus `catalog-enrichment` for marketplace batch

Status: accepted · Origin: gohan-spec v0.13 decision D83

## Decision

Three acceptance processes gate the core API review: Kafka refunds (redelivery, lost acks), Temporal travel fulfilment (supplier uncertainty, compensation ownership), Camunda invoice approval (long waits, revoked authority, duplicate workers), each also exercised with a flag outage and a definition version change; plus `catalog-enrichment` for marketplace batch.

## Context and evidence

Async contracts must be exercised before core freezes.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
