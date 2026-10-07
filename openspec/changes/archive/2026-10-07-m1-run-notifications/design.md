# Design: m1-run-notifications

## Context

The `streams` *Run notices* requirement carries the whole delivery contract: a `RunNotice` of seven fields, written by `Runs.Finish`/`Runs.Suspend` in the state-change transaction, delivered by `std/notify` to the stack's `Notifier` (`agent.WithNotifier`) at least once and unordered, retried with exponential backoff (1 s doubling to 1 h) for up to 72 h, then dropped with a `notice_dead` audit record and `gohan.notice.dead`; `std/notify.Only(kinds...)` filters, and `std/notify/webhook` is the HTTP `Notifier` whose `Endpoint func(tenant string) (url, secret string, ok bool)` gives each tenant its own destination (no endpoint → the notice is acknowledged and `gohan.notice.unrouted` increments) and whose requests follow Standard Webhooks (openspec/specs/streams/spec.md:134). The outbox half of that contract ships (`core/stores/runs_notices.go`); the delivery half does not (openspec/changes/m1/design.md:184-187,208). The egress lane the delivery must ride is implemented (`std/egress.Client`, `std/tool/http`), the network declaration and audit fields are sealed (ADR-0156), and the run-notice decision is recorded (ADR-0133). Policy values and implementations belong in `std`; `core` holds ports and types only (docs/design/architecture.md:47,101-121).

## Goals / Non-Goals

**Goals:** Claim and deliver the shipped notice outbox through a per-pod dispatcher, implement the Standard Webhooks sender with per-tenant endpoint isolation, and turn the four owned scenarios green on the public path.

**Non-Goals:** This change adds no generic event bus and no second delivery channel; it adds no delivery guarantee beyond what the scenarios state (at-least-once, unordered, `ID` as the idempotency key). It adds no per-run or per-request custom endpoint: the `background` `webhook` request override belongs to `interop` (openspec/specs/interop/spec.md:48). It adds no transport module and no HTTP handler, and it does not implement `std.ExplainHandler` (openspec/changes/m1/design.md:213-217). Notices stay thin — no message, result, payload or error detail rides a notice.

## Decisions

### D1 — The sink stays a `types.Notifier`; `std/notify` owns delivery

`std/notify` holds a dispatcher constructed from `stores.Runs` and the stack's `types.Notifier`, and offers one deterministic pass (`Deliver(ctx)` claims a batch, delivers, acknowledges) plus a loop (`Run(ctx)`) the host starts once per pod. The per-pass seam is what makes the retry clock testable without a running server. `Notifier` is a core port; the dispatcher, its backoff policy and the webhook sender are `std` (ADR-0139; docs/design/architecture.md:47). `Only(kinds...)` restricts which notice kinds are delivered. The dispatcher claims through `Runs.Notices(limit)` and acknowledges with `Runs.AckNotice(id)`; an unacknowledged claim returns to pending and a redelivery carries the same notice id, which is the consumer's idempotency key (`stores.notice-written-with-finish`).

### D2 — Standard Webhooks signature and key handling

The body is the `RunNotice` JSON: exactly `id`, `kind`, `tenant`, `session_id`, `run_id`, `reason`, `at` (core/types/event.go). The sender sends `webhook-id` = the notice id, `webhook-timestamp` = the Unix seconds at send, and `webhook-signature: v1,<base64(HMAC-SHA256(secret, "{id}.{timestamp}.{body}"))>` — the signed string is `id.timestamp.body`, as the scenario states. The secret is the tenant's value from the endpoint registry; a `whsec_` prefix is stripped and the remainder base64-decoded before the HMAC. A receiver holding the secret recomputes over the three values it received and rejects a `webhook-timestamp` older than five minutes (openspec/specs/streams/spec.md:134,146-149). The timestamp is bound inside the signed string, so a replay cannot keep the body and move the clock.

**Key rotation.** The registry returns one secret per tenant per call, so the wire format carries no key id beyond the `v1,` scheme version. Rotation is a receiver-first operation: the receiver accepts both the outgoing and the incoming secret during the window, then the registry value is swapped to the new secret. A sender whose registry still holds the old secret keeps verifying against the receiver's still-accepted old value, and no delivery is lost. Emitting two signatures per request was rejected: the fixed `Endpoint` shape returns one secret, and changing it would re-open a contract the spec seals.

### D3 — Per-tenant endpoint registry and its isolation invariant

The registry is the `Endpoint func(tenant string) (url, secret string, ok bool)` the spec fixes. The invariant: the dispatcher calls it with `notice.Tenant` and nothing else; a delivery to a tenant never consults, falls back to, or defaults to another tenant's entry. `ok == false`, an empty URL or an empty secret route to no destination — the notice is acknowledged and `gohan.notice.unrouted` increments, per the requirement. There is no global default endpoint and no cross-tenant cache: the lookup runs once per delivery and the URL and secret stay bound to the notice they were resolved for. `identity.notice-endpoint-per-tenant` is this invariant observed from two tenants.

### D4 — Failed delivery classification

The webhook sender returns a `*types.ModelError` for every non-2xx outcome, so the dispatcher classifies with one type the tree already retries on:

- `429` → `ClassRateLimited`, with `ModelError.RetryAfter` from the `Retry-After` header; the delay honors it when present.
- `408`, `5xx`, transport failure and timeout → `ClassTransient`.
- `3xx` and every other `4xx` → `ClassPermanent`.

The requirement is explicit that only a 2xx acknowledges and that 3xx/4xx/5xx all retry (openspec/specs/streams/spec.md:134), so retry is not keyed on `Retryable()`: the dispatcher retries every non-2xx delivery on the one schedule and the class selects the delay shape — exponential 1 s doubling to a 1 h cap, bounded by 72 h total. When the 72 h budget is exhausted the notice is dropped, `Stores.AuditLog` receives a `notice_dead` record carrying the notice id, and `gohan.notice.dead` increments; a successful delivery counts `gohan.notice.delivered{kind}` and each retry counts `gohan.notice.retried`. Local, non-HTTP refusals are terminal rather than retried, because no clock change alters them: a missing endpoint acknowledges as unrouted (D3), and a `types.EgressDenied` from the governed client is acknowledged as undeliverable — the notice leaves the outbox and is never retried. Only the `egress_denied` record (D5) is written for it; `notice_dead` is reserved for the 72 h budget. Retrying a policy refusal would only spin the dispatcher.

### D5 — The sender rides the audited egress path

The dispatcher builds one `std/egress.Client(policy)` from the notice lane's `types.EgressPolicy`, and the webhook sender performs every POST through that client — it never opens its own `http.Client` and never dials on its own, mirroring `std/tool/http.Fetch` reading the client the flow resolved for it. A destination outside the policy is refused as `types.EgressDenied` before any connection, so a tenant endpoint cannot reach an address the stack did not allow. Because a notice POST is not a tool call, the driver's `classifyFailure` does not see it: the dispatcher appends the `egress_denied` audit record itself — `stores.AuditRecord{Kind: stores.AuditEgressDenied, Tenant, Host, Reason}` through `AuditLogFrom(ctx)` — and counts `gohan.egress.requests{tool: "notice", host, decision: "denied"}`. The refused notice is then acknowledged as undeliverable and never retried, so only the `egress_denied` record is written for it; `notice_dead` stays reserved for the 72 h budget (D4). The record and the metric carry the resolved host and the decision only; no URL, path, query, user-info, secret or body reaches an audit field or a metric attribute (ADR-0156 §4).

### D6 — Build wiring and the detached warning

`agent.WithNotifier(n types.Notifier)` becomes the build option the requirement names; it records the stack's sink, and the host builds the dispatcher from the stack's `Runs` store and that sink. `Build` does not spawn a goroutine — the library hands out the pieces and the host runs `Run(ctx)`. A `Detached()` flow without a notifier is allowed and logs one warning at `Build`: `Attach` polling stays a valid way to see the run's events, and the warning names the missing sink rather than failing the build (openspec/specs/streams/spec.md:134).

### D7 — Public-path proofs without external network access

Every scenario test drives the shipped outbox (`core/stores` memory `Runs`), the real dispatcher and the real sender against `httptest` servers, with a fake clock so the 72 h backoff runs in milliseconds. No test reaches the network. The four owned scenarios are subtests named exactly their IDs, registered through the public `Build`/`WithNotifier` path:

- `streams.webhook-standard-signed` — a receiver with the tenant secret recomputes the signature over `id.timestamp.body` and accepts; a receiver with a wrong secret, or a tampered body, rejects.
- `streams.notice-retry-and-dead-letter` — a 503 endpoint drives backoff from 1 s to 1 h, the notice is dropped at 72 h, the `notice_dead` audit record carries the notice id and `gohan.notice.dead` increments.
- `suspension.approval-notice-delivered` — a `Detached` run suspending `HumanApproval` produces a `RunNotice{Kind: NoticeSuspended, Reason: "HumanApproval"}` at the notifier, and `Inspect(runID)` returns the `ApprovalRequest`.
- `identity.notice-endpoint-per-tenant` — `Endpoint` returns a different URL and secret for `t1` and `t2`, each notice lands only on its own tenant's server signed with that tenant's secret, and a tenant with no endpoint receives nothing.

## Risks / Trade-offs

- [Risk] A notice POST is not a tool call, so the driver's egress audit does not cover it. → Accepted and bounded by D5: the dispatcher records the `egress_denied` record and metric itself, with host and reason only.
- [Risk] Retrying a permanent-class 4xx for 72 h wastes dispatcher cycles. → Accepted: the requirement states that only a 2xx acknowledges and that 4xx retries; the delay is capped at 1 h and the dead-letter record arrives at 72 h.
- [Risk] A receiver that rotates its secret before the registry is swapped loses deliveries. → Accepted and stated in D2: rotation is receiver-first (accept both, then swap), so no window exists where the sender's secret is unknown to the receiver.

## Migration Plan

No data migration is required. The change adds `std/notify` and `std/notify/webhook` and the `AuditNoticeDead AuditKind = "notice_dead"` value to the `stores` kind list; `agent.WithNotifier(Notifier)` moves from the build contract's illustrative list to a named option (openspec/specs/build/spec.md:58). The `streams` and `identity` main specs already carry every normative fact this change implements, so no requirement text is edited: the delta files record that and defer the `docs/design/types.md` regeneration to the change that first names new identifiers. Keep the module pre-v1 and create no release tag during M1 authoring (openspec/changes/m1/design.md:20-27). The identity delta adds one sentence to the Identity requirement; the archive merge must union it with the other change's identity delta, not replace the body.
