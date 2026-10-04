# ADR-0133: Run notices — outbox in `Runs`, thin events, pluggable sink

Status: accepted · Origin: grill round 59 (2026-09-30)

## Decision

`Runs.Finish` and `Runs.Suspend` write a `RunNotice{ID, Kind, Tenant, SessionID, RunID, Reason, At}` in the same transaction as the state change; `Runs.Notices`/`AckNotice` let a per-pod dispatcher claim and acknowledge them. `std/notify` delivers to the stack's `Notifier` at least once, unordered, with exponential backoff up to 72 h and a `notice_dead` audit record after that. `std/notify/webhook` implements Standard Webhooks with a per-tenant endpoint and secret. Notices carry ids and a reason word only; content is read through `Inspect`, `Attach` or the stores.

## Context and evidence

Provider platforms deliver background-run outcomes as thin, signed webhook events (`response.completed|failed|cancelled|incomplete`), at least once with backoff up to 72 hours, unordered, with the event id as the idempotency key and reconciliation against the API. gohan's detached runs, suspensions and failures were visible only to a client holding `Attach`; an approver of a `HumanApproval` was never told, and a crash after `Finish` could lose the fact that the run ended.

## Consequences

`streams` v1.7 (`NoticeKind`, `RunNotice`, `Notifier`, rule block, three scenarios), `stores` v1.10 (`Notices`, `AckNotice`, outbox rule, one scenario), `suspension` v1.3, `identity` v1.6, `build` v1.5 (`WithNotifier`), telemetry attribute, layout `std/notify`; task 10; delivery scenarios M1.
