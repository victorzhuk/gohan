# Tasks

Every owned scenario id below names one exact subtest. Each task lists the ids it owns. Lane acceptance is all six owned ids, each bound to one runnable path: `tools.egress-body-read-bounded` and `tools.egress-per-request-transport-released` on the `web_fetch` public path, and `tools.egress-early-error-cleanup`, `tools.egress-oversize-cleanup`, `tools.egress-close-idempotent` and `tools.egress-cleanup-preserves-checks` at the `std/egress` seam. Task 3.4 records the evidence that every selector matched a test. The milestone regression floor is M1. Global M2 is a milestone gate and does not block this lane.

## 1. Response lifetime in `std/egress`

- [x] 1.1 Move the policy time limit off the request return path: replace `defer cancel()` in `transport.RoundTrip` with an explicit release closure that the success path hands to the response body, and call release on every early return (`validatedIPs` failure, proxy-scheme refusal, `RoundTrip` error). Keep the `denied(..., "timeout")` mapping in the `RoundTrip` error branch. Owns `tools.egress-body-read-bounded`, `tools.egress-early-error-cleanup`. verify: `timeout 180 go test -timeout 2m ./std/egress -run 'TestEgressTimeout|TestEgressBodyReadBounded|TestEgressEarlyErrorCleanup'`.
  - evidence: `std/egress/client.go:84-88` (`defer cancel()` today), `:90-93,96-97,116-122` (early returns), `:118-119` (timeout mapping); design D1.
- [x] 1.2 Add the release hook to the body wrapper: a `release func()` field, a `sync.Once`, and an explicit `Close() error` on `limitedBody`; run release on `Close` and on a read that returns `io.EOF`. Keep `Read`'s byte cap and the over-limit `deny` path unchanged. Owns `tools.egress-close-idempotent`. verify: `timeout 180 go test -timeout 2m ./std/egress -run 'TestEgressLimitedBodyCapsUnderlyingRead|TestEgressBodyCloseIdempotent'`.
  - evidence: `std/egress/client.go:257-288` (`limitedBody`, `Read`), `:127-131` (wrapper construction); design D3.
- [x] 1.3 Make the oversize and early-error paths release the transport and cancel the limit, so no exit path returns with a live transport. Owns `tools.egress-oversize-cleanup`. verify: `timeout 180 go test -timeout 2m ./std/egress -run 'TestEgressOversizeCleanup|TestEgressEarlyErrorCleanup'`.
  - evidence: `std/egress/client.go:123-126` (oversize refusal closes the body then returns); design D3.

## 2. Transport ownership and the reuse gate

- [x] 2.1 Keep the per-request `http.Transport` and call `CloseIdleConnections` from the release closure; add no shared transport and no `MaxIdleConnsPerHost` tuning. Owns `tools.egress-per-request-transport-released`. verify: `timeout 180 go test -timeout 2m ./std/egress -run 'TestEgressPerRequestTransportReleased'`.
  - evidence: `std/egress/client.go:94-115` (per-request transport), `:116` (exchange); design D2, D6.
- [x] 2.2 Prove the request checks are unchanged: no DNS cache, no pooled resolver, identical redirect, proxy, and checked-dial behavior. Owns `tools.egress-cleanup-preserves-checks`. verify: `timeout 180 go test -timeout 2m ./std/egress -run 'TestEgressRebindingSafe|TestEgressRedirectPrivateRefused|TestEgressHTTPProxyChecked|TestEgressCrossHostRedirectStripsAuthorization'`.
  - evidence: `std/egress/client.go:148-256` (`validateRedirect`, `validatedIPs`, `dialChecked`, `hostAllowed`), `:76-80` (redirect closure); `std/egress/client_test.go:36,65,160,191`; design D4.

## 3. Tests for the lifetime behavior

- [x] 3.1 Add the subtest `tools.egress-body-read-bounded`: a server writes response headers and then stalls, and the body read stops at the policy limit with a permanent failure instead of hanging. Owns `tools.egress-body-read-bounded`. verify: `timeout 180 go test -timeout 2m ./std/egress -run 'TestEgressBodyReadBounded'`.
  - evidence: `std/egress/client_test.go:91-94,147-150` (existing `httptest` shape and header-phase timeout); design D5.
- [x] 3.2 Add the subtests `TestEgressEarlyErrorCleanup`, `TestEgressOversizeCleanup`, `TestEgressBodyCloseIdempotent`, and `TestEgressBodyDeadline`: an early refusal before a response, an oversize `Content-Length` refusal, a caller that closes the body twice or after end of file, and a deadline during the body read. Owns `tools.egress-early-error-cleanup`, `tools.egress-oversize-cleanup`, `tools.egress-close-idempotent`. verify: `timeout 180 go test -timeout 2m ./std/egress -run 'TestEgressEarlyErrorCleanup|TestEgressOversizeCleanup|TestEgressBodyCloseIdempotent|TestEgressBodyDeadline'`.
  - evidence: `std/egress/client_test.go:118-121` (`limitedBody` unit shape), `:137-140` (dial counter at zero); design D3, D5.
- [x] 3.3 Add `TestEgressCleanupPreservesChecks`: the checked address is dialed, the later resolution is not dialed, the second resolution to loopback opens no connection, and release does not change that. Owns `tools.egress-cleanup-preserves-checks`. verify: `timeout 180 go test -timeout 2m ./std/egress -run 'TestEgressCleanupPreservesChecks'`.
  - evidence: `std/egress/client_test.go:65-68` (existing rebinding stub); design D4, D5.
- [x] 3.3a Require the deadline test to prove cleanup before any caller `Close`: `TestEgressBodyDeadline` asserts the release ran while the read error is still the caller's, before the test closes the body, and that the returned bytes and error are unchanged. Owns `tools.egress-body-read-bounded` on the deadline path. verify: the same command as 3.2, with the assertion order visible in the test body.
  - evidence: design D3 (terminal read error releases, bytes and error preserved).
- [x] 3.4 Drive the lifetime on the public path through `gohan.Build` and `web_fetch` with a slow body, and register subtests for all six owned ids. The two ids that name caller-visible behaviour run here: `tools.egress-body-read-bounded` and `tools.egress-per-request-transport-released`. The other four are seam ids and bind to the `std/egress` package tests, because the caller-visible result is identical in both cases and only the package seam can observe the release: `tools.egress-early-error-cleanup` → `TestEgressEarlyErrorCleanup`, `tools.egress-oversize-cleanup` → `TestEgressOversizeCleanup`, `tools.egress-close-idempotent` → `TestEgressBodyCloseIdempotent`, `tools.egress-cleanup-preserves-checks` → `TestEgressCleanupPreservesChecks`. verify: `timeout 600 go test -timeout 10m ./std/tool/http -run 'TestEgressScenarios'` plus the `std/egress` command in 3.2 and 3.3; then run `timeout 60 go test -timeout 2m ./std/egress ./std/tool/http -run 'TestEgress' -v` and record each of the six selectors above in the output. A selector that matched no test fails this task.
  - evidence: `std/tool/http/fetch.go:22-35` (the tool reads the client body), `std/tool/http/scenarios_test.go:41-102` (public-path harness); design D5.

## 4. Documentation and main spec, registry and ADR at apply time

- [x] 4.1 Document the lifetime rule in the `std/egress` package doc comment and on `transport.RoundTrip`: the time limit ends with the body, and release runs once per response. verify: reading `std/egress/client.go:76-92` shows both sentences match D1 and D3.
- [x] 4.2 Land the `tools` main-spec edit under a new ADR: add the two requirements from this delta to `openspec/specs/tools/spec.md` next to the Egress requirement at `openspec/specs/tools/spec.md:157-189`, and record the sanitize-independent release decision. No ADR number is reserved during authoring. verify: `timeout 300 task spec` and `timeout 60 openspec validate repair-egress-response-lifetime --strict --no-interactive`.
  - evidence: `openspec/specs/tools/spec.md:157-189`; design D7.
- [x] 4.3 Register the six proposed scenario ids in the scenario registry: `tools.egress-body-read-bounded`, `tools.egress-early-error-cleanup`, `tools.egress-oversize-cleanup`, `tools.egress-close-idempotent`, `tools.egress-cleanup-preserves-checks`, `tools.egress-per-request-transport-released`. verify: `timeout 300 task spec` reports no missing id for `tools`.
  - evidence: this change's `specs/tools/spec.md`; design D7.
- [x] 4.4 Confirm the types index needs no regeneration: no exported type or signature changes, so `docs/design/types.md` stays byte-identical. verify: `timeout 300 task spec:types`.
  - evidence: `std/egress/client.go:257-288` (`limitedBody` and `transport` are unexported); design D7.

## 5. Repository gates

- [x] 5.1 Run the package and repository gates after the edits and resolve failures. verify: `timeout 900 task lint && timeout 1200 task test`.
  - evidence: `std/egress/client.go`, `std/egress/client_test.go`, `std/tool/http/scenarios_test.go`.
- [x] 5.2 Validate the authored change package. verify: `timeout 60 openspec validate repair-egress-response-lifetime --strict --no-interactive`.
  - evidence: the CLI reports the change as valid.