# Tasks

Delivered in `feat(streams): deliver run notices through the governed webhook path`. A task is done when its listed scenario IDs pass and `task lint`, `task spec` and `task test` are green.

## 1. Notifier sink and dispatcher

- [x] 1.1 Add `agent.WithNotifier(n types.Notifier)` to `core/build_options.go`, store the sink on the build config, and log one warning at `Build` when a `Detached()` flow has no notifier; verify `go test -short -timeout 2m ./core/... -run 'TestNotifierBuild'`.
  - evidence: `core/build_options.go` `WithNotifier`; `core/notice_build_test.go` (`TestNotifierBuild`)
- [x] 1.2 Add the `notice_dead` value as `stores.AuditNoticeDead AuditKind = "notice_dead"` in `core/stores/audit.go`; verify `go test -short -timeout 2m ./core/... -run 'TestAuditNotice'`.
  - evidence: `core/stores/audit.go` (`AuditNoticeDead`)
- [x] 1.3 Implement `std/notify`: the dispatcher over `stores.Runs` and `types.Notifier` with a deterministic `Deliver(ctx)` pass, a `Run(ctx)` loop, `Only(kinds ...types.NoticeKind)`, at-least-once claim/acknowledge, the exponential 1 s→1 h backoff bounded by a 72 h budget, and the `notice_dead` audit record plus `gohan.notice.delivered{kind}`, `gohan.notice.retried`, `gohan.notice.dead`, `gohan.notice.unrouted` counts; verify `go test -short -timeout 2m ./std/notify/... -run 'TestDispatch'` and assert an unacknowledged claim is redelivered with the same notice id.
  - evidence: `std/notify/notify.go` (dispatcher); `std/notify/dispatcher_test.go`
- [x] 1.4 Map a non-2xx delivery to `*types.ModelError` (`429` → `ClassRateLimited` with `RetryAfter`, `408`/`5xx`/transport/timeout → `ClassTransient`, `3xx`/other `4xx` → `ClassPermanent`) and retry every non-2xx on the one schedule with the class selecting the delay shape; verify `go test -short -timeout 2m ./std/notify/... -run 'TestClassify'`.
  - evidence: `std/notify/notify.go` (class mapping, retry schedule); `std/notify/dispatcher_test.go`
- [x] 1.5 Thread the suspension's `SuspendReason` into the notice `Reason` in `core/stores/runs_notices.go` (`noticeFor` currently collapses every suspension to `types.StopSuspended`), and map a cancelled run to `types.NoticeCancelled` with `Reason = string(types.StopCancelled)`, so the notice `Reason` is the `SuspendReason`/`StopReason` constant name the requirement names; verify `go test -short -timeout 2m ./core/stores/... -run 'TestNoticeReason'` and the `suspension.approval-notice-delivered` subtest sees `Reason: "HumanApproval"`.
  - evidence: `core/stores/runs_notices.go` (`SuspendReasonRecorder`/`FinishReasonRecorder`, notice `Reason` = SuspendReason constant name, `NoticeCancelled`); `std/notify/scenarios_test.go` (`suspension.approval-notice-delivered`)

## 2. Webhook sender and endpoint registry

- [x] 2.1 Implement `std/notify/webhook` as the Standard Webhooks `Notifier`: `Endpoint func(tenant string) (url, secret string, ok bool)`, JSON body of the notice, `webhook-id`, `webhook-timestamp`, `webhook-signature: v1,<base64(HMAC-SHA256(secret, id.timestamp.body))>` with a stripped `whsec_` prefix; verify `go test -short -timeout 2m ./std/notify/... -run 'TestWebhookSign'`.
  - evidence: `std/notify/webhook/` (Standard Webhooks HMAC-SHA256); `std/notify/scenarios_test.go` (`streams.webhook-standard-signed`)
- [x] 2.2 Enforce endpoint isolation: the registry is called with `notice.Tenant` only, a missing or empty entry acknowledges the notice and increments `gohan.notice.unrouted`, and no delivery reads another tenant's entry or a default; verify `go test -short -timeout 2m ./std/notify/... -run 'TestEndpointIsolation'`.
  - evidence: `std/notify/notify.go`; `std/notify/scenarios_test.go` (`identity.notice-endpoint-per-tenant`)
- [x] 2.3 Send every POST through the dispatcher's single `std/egress.Client(policy)` and never a private client; on a `types.EgressDenied` append the `egress_denied` audit record (host and reason only), count `gohan.egress.requests{tool: "notice", host, decision: "denied"}`, and acknowledge the notice as undeliverable without retrying it (never `notice_dead`); verify `go test -short -timeout 2m ./std/notify/... -run 'TestGovernedEgress'` and assert no URL, query, secret or body appears in an audit field.
  - evidence: `std/notify/notify.go` (governed egress path); `std/notify/dispatcher_test.go`, `std/notify/scenarios_test.go`

## 3. Scenario behavior and package gates

- [x] 3.1 Add a top-level `TestRunNotices` whose public-path subtests are named exactly `streams.webhook-standard-signed`, `streams.notice-retry-and-dead-letter`, `suspension.approval-notice-delivered` and `identity.notice-endpoint-per-tenant`, driving the shipped outbox, the real dispatcher and the real sender against `httptest` servers with a fake clock; verify `go test -short -timeout 2m ./std/notify/... -run 'TestRunNotices'`.
  - evidence: `std/notify/scenarios_test.go` (`TestRunNotices` with the four ID-named subtests)
- [x] 3.2 Run the repository wrappers and resolve failures; verify `task lint` and `task test`.
  - evidence: driver-run `task lint` 0 issues; `task test` green across root, `adapter/otel`, `adapter/openai`, `adapter/anthropic`, `examples`
- [x] 3.3 Validate the change after all required M1 owners land; verify `openspec validate m1-run-notifications --strict --no-interactive` and `task spec:gate GATE=M1`.
  - evidence: driver-run `task spec:gate GATE=M1` `unregistered=0`, `missing_in_scope=10`, all ten owned by lanes not yet authored
