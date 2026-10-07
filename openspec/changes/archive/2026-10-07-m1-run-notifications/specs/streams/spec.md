# Spec Delta

## MODIFIED Requirements

### Requirement: Run notices

A `RunNotice` is the out-of-band signal that a run reached a terminal or waiting state: `NoticeFinished`, `NoticeFailed`, `NoticeCancelled` with `Reason` = the `StopReason` constant name (e.g. `"StopCompleted"`), `NoticeSuspended` with `Reason` = the `SuspendReason` constant name (e.g. `"HumanApproval"`). It carries ids and a reason word only — never a message, a result, an approval payload or an error detail; consumers read those through `Inspect`, `Attach` or the stores. Notices are written by `Runs.Finish` and `Runs.Suspend` in the same transaction as the state change (`stores`) and delivered by `std/notify` to the stack's `Notifier` (`agent.WithNotifier`) at least once, in no guaranteed order: `ID` is the consumer's idempotency key. Delivery retries with exponential backoff (1 s doubling to 1 h) for up to 72 h, then the notice is dropped with an audit record `notice_dead`; `gohan.notice.delivered{kind}`, `gohan.notice.retried`, `gohan.notice.dead`. `std/notify.Only(kinds...)` filters. `std/notify/webhook` is the HTTP `Notifier`: `Endpoint func(tenant string) (url, secret string, ok bool)` gives each tenant its own destination (no endpoint → the notice is acknowledged and `gohan.notice.unrouted` increments); requests follow Standard Webhooks — JSON body of the notice, `webhook-id`, `webhook-timestamp`, `webhook-signature` (HMAC-SHA256, `v1,` prefix), receivers reject timestamps older than five minutes; only a 2xx acknowledges, 3xx/4xx/5xx retry; the URL passes the stack's `EgressPolicy` (`tools`). A Go host passes any `Notifier`. `Detached()` without a `Notifier` is allowed (`Attach` polling stays valid) and logs a warning at `Build`.

A webhook delivery refused by the stack's `EgressPolicy` is a permanent delivery failure: it carries no HTTP response, and `std/notify` MUST record it as `egress_denied` with the resolved host and the refusal reason only — no URL, path, query, secret or body — and MUST count `gohan.egress.requests{tool: "notice", host, decision}`. The refused notice MUST be acknowledged as undeliverable without a retry: only the `egress_denied` record is written for it, and `notice_dead` is reserved for the 72 h budget. The dead-letter audit record for the 72 h budget MUST be the `notice_dead` kind (`stores`).

#### Scenario: notice is thin
ID: `streams.notice-thin-no-content`
- WHEN a run finishes with a two-paragraph reply and a tenant webhook is configured
- THEN the delivered body has `id`, `kind: "finished"`, `tenant`, `session_id`, `run_id`, `reason`, `at` and nothing else

#### Scenario: notice retried then dead-lettered
ID: `streams.notice-retry-and-dead-letter`
- WHEN the tenant endpoint answers 503 for 72 hours
- THEN deliveries back off from 1 s to 1 h, the notice is then dropped, `notice_dead` is audited with the notice id and `gohan.notice.dead` increments

#### Scenario: webhook signed per Standard Webhooks
ID: `streams.webhook-standard-signed`
- WHEN a notice is delivered
- THEN the request carries `webhook-id` = notice id, `webhook-timestamp` and `webhook-signature` computed with the tenant's secret over `id.timestamp.body`, and a receiver with the secret verifies it
