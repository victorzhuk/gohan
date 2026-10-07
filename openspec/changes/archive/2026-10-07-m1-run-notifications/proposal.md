# Proposal: m1-run-notifications

## Why

The `streams` *Run notices* requirement declares the out-of-band signal that a run reached a terminal or waiting state and fixes its delivery contract, but no `std/notify` or `std/notify/webhook` exists (openspec/specs/streams/spec.md:132-149; openspec/changes/m1/design.md:208). The `Runs` notice outbox ships (`core/stores/runs_notices.go`): `Finish` and `Suspend` append the notice in the state-change transaction, and `Notices`/`AckNotice` claim and acknowledge it, but nothing claims a delivery, so a `Detached` run, a `HumanApproval` suspension or a failure stays invisible to a client that is not attached. Three registered scenarios are left `integration gap` or `missing` for exactly that absent delivery path, and one endpoint-isolation scenario has no implementation at all (openspec/changes/m1/design.md:184-187).

## What Changes

- Add `std/notify`: the per-pod dispatcher that claims pending notices from `Stores.Runs`, delivers each to the stack's `Notifier`, acknowledges only on success, retries with the required exponential backoff, and dead-letters after 72 h with the `notice_dead` audit record and `gohan.notice.dead`.
- Add `std/notify/webhook`: the Standard Webhooks HTTP `Notifier`, carrying the per-tenant `Endpoint` registry and signing each request over `id.timestamp.body` with the tenant's own secret.
- Add the `agent.WithNotifier(Notifier)` build option the requirement names, and warn at `Build` when a `Detached` flow has no notifier.
- Preserve and cover the four scenarios this lane owns: `streams.notice-retry-and-dead-letter`, `streams.webhook-standard-signed`, `suspension.approval-notice-delivered`, `identity.notice-endpoint-per-tenant`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `streams`: the *Run notices* requirement already carries the delivery contract this change implements, including the `streams.webhook-standard-signed` scenario, so the delta reproduces it and adds only the notice-path egress audit sentence the requirement lacked (openspec/specs/streams/spec.md:132-149).
- `identity`: the *Identity* requirement already carries the `identity.notice-endpoint-per-tenant` scenario, so the delta reproduces it and adds only the endpoint-isolation invariant stated as a normative rule (openspec/specs/identity/spec.md:135-138).

## Impact

The design adds one build option to `core/build_options.go`, one new `std/notify` package and the `std/notify/webhook` package beside it, and one `AuditKind` value (`notice_dead`) to the `stores` kind list; it adds no third-party dependency and no transport module. Webhook delivery rides the shipped `std/egress.Client` and the `types.EgressPolicy` the `tools` requirement already defines (openspec/specs/tools/spec.md:161; ADR-0156). The lane is a `std`/`core` change only — adapter and `interop` work is out of scope (openspec/specs/interop/spec.md:48).
