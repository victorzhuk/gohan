# ADR-0123: User feedback is a scored, owner-checked signal outside the model's context

Status: accepted · Origin: grill round 49 (2026-09-30)

## Decision

`Stack.Feedback` records `Feedback{Target, Name, Value, Comment, Correction, Source}` through a `FeedbackStore` port: owner-checked, idempotent per `(Target, Name, Subject)`, audited, emitted as `FeedbackRecorded` on the run's `EventLog`, and counted as `gohan.feedback{name, flow, release, variant}`. Comments and corrections carry `OriginUser`, cascade on erase, and are never assembled, noted or memorised. `evals.Import(FromFeedback(name))` turns scored targets into cases with the redacted correction as expected output; online sampling may weight by feedback and says so.

## Context and evidence

Observability platforms model feedback as scores on traces with stable ids (overwrite, not duplicate), accept explicit and implicit signals alike, and turn negative scores plus corrections into evaluation datasets, warning about selection bias, sparsity and PII in free text. gohan had offline and online evals and a session importer but no way for a transport to record a rating or correction, link it to release and variant, or keep it out of the model's context.

## Consequences

`flow` v1.2 (`Feedback` types, `Stack.Feedback`, three scenarios), `stores` v1.4 (`FeedbackStore`, one scenario), `build` (`Stores.Feedback`), `streams` v1.3 (`FeedbackRecorded`, one scenario), `telemetry` v1.1 (metric, one scenario), `release` v1.2 (`FromFeedback`, one scenario); task 12; telemetry M1, import M4.
