# Design: repair-egress-response-lifetime

## Context

See proposal.md — Why. The client is one file: `std/egress/client.go`. `Client` builds one `http.Client` with a private `transport` (std/egress/client.go:41-74). `RoundTrip` resolves and checks the host once (std/egress/client.go:90-92), builds a per-request `http.Transport` (std/egress/client.go:94-115), performs the exchange (std/egress/client.go:116), refuses an oversize `Content-Length` (std/egress/client.go:123-126), and wraps the body in `limitedBody` (std/egress/client.go:127-131). The timeout context is cancelled by `defer cancel()`, which runs as `RoundTrip` returns (std/egress/client.go:84-88). `limitedBody` embeds `io.ReadCloser` and has no `Close` method of its own (std/egress/client.go:257-288). The shipped tool reads and closes the body itself (std/tool/http/fetch.go:26-32). Existing tests already stub the resolver and dialer through package variables (std/egress/client.go:19-22; std/egress/client_test.go:18-20,65-68) and cover the header-phase timeout (std/egress/client_test.go:147-150).

## Goals / Non-Goals

**Goals:**

- Keep the policy time limit in force for the whole exchange, including the body read.
- Release the per-request transport exactly once on every exit path.
- Keep the checked-address dial, proxy validation, and redirect rules byte-for-byte in behavior.

**Non-Goals:**

- No retry, backoff, or circuit breaker. `web_fetch` has no retry policy today and this change adds none.
- No shared connection pool, no `MaxIdleConnsPerHost` tuning, no DNS cache. Reuse needs a measurement first.
- No change to `std/tool/http/fetch.go` signatures, to output storage, to redaction, or to sandbox adapters. Persisted-output sanitization belongs to its owner.

## Decisions

### D1 — The timeout cancel belongs to the body lifetime

Remove `defer cancel()` (std/egress/client.go:87). When `cfg.timeout > 0`, create the context and keep its `cancel` for the response path only. On success, hand `cancel` to the response wrapper, so the timer ends when the caller closes the body or reads to end of file. On every path that returns before a response exists — `validatedIPs` failure, proxy-scheme refusal, and a `RoundTrip` error (std/egress/client.go:90-93,96-97,116-122) — call the release function before returning.

Alternative considered: set `http.Client.Timeout` on the client in `Client()` and delete the per-request context. Rejected for this slice: the timeout is a policy value that the transport already maps to a `denied(..., "timeout")` egress refusal (std/egress/client.go:118-119), and moving it would move that mapping out of the transport and change the model-visible result of `TestEgressTimeout` (std/egress/client_test.go:147-150). The transport keeps ownership of the bound.

Consequence to state, not hide: a caller that never closes the body keeps the timer until the deadline fires. The bound self-expires, and the deadline remains a hard bound either way.

### D2 — The per-request transport stays; its idle connections are released

Keep the per-request `http.Transport` (std/egress/client.go:94-115). Do not share it across requests: a shared transport would hold a connection to one checked address open for the next caller, and the current model is one checked dial per request. Instead, capture the transport in the release function and call `CloseIdleConnections` when the response ends. `CloseIdleConnections` is safe on a transport with no idle connections, so this is cleanup, not a reclamation claim.

Alternative considered: clone `http.DefaultTransport` once and pool connections. Rejected here: it is a measured optimization (D6) and it would keep sockets past the response lifetime the policy bounds.

### D3 — One release path, idempotent

Add a release closure and a `sync.Once` to the response wrapper:

- `release` calls `base.CloseIdleConnections()` and then `cancel` when a cancel exists.
- `limitedBody` gains a `release func()` field, a `once sync.Once`, and an explicit `Close() error` method that runs `once.Do(release)` and then closes the embedded body.
- `Read` runs the release on every terminal read result, not on `io.EOF` alone. A terminal result is `io.EOF` (with or without final bytes) or any other read error, including the policy deadline error a stalled body returns. On a terminal read error, `Read` closes the embedded body, runs the release through the same `sync.Once`, and returns the bytes it read together with the original error unchanged. The caller never has to close the body to release the transport, and a deadline read releases before any explicit caller `Close`.
- The oversize `Content-Length` path (std/egress/client.go:123-126) calls `release` with the body close.

Because the wrapper is single-consumer by contract and `once` guards the release, a double `Close`, a `Close` after end of file, and the size-limit path are all safe. No goroutine and no background timer is added.

### D4 — Preserve the request checks exactly

`validateRedirect`, `literalAllowed`, `validatedIPs`, `dialChecked`, `deniedAddress`, `hostAllowed`, the redirect `CheckRedirect` closure, and the cross-host `Authorization` strip stay untouched (std/egress/client.go:76-80,148-256). The checked address that `dialChecked` resolves is still the dialed address; no DNS cache or shared resolver is introduced. The proxy branch keeps its per-request `DialContext` over `dialChecked` (std/egress/client.go:100-108).

### D5 — Prove the lifetime on the public path and at the package seam

Use the existing seams: `resolveHost` and `dialAddress` are package variables the tests replace (std/egress/client.go:19-22; std/egress/client_test.go:65-68), and the tests drive a real `http.Client` against `httptest` servers (std/egress/client_test.go:91-94,147-150). New tests: a server that writes headers then stalls before the body (body bound); a caller that closes the body early; a deadline during the body read; a refused request with a dial counter at zero (no idle connection); a repeated `Close`; a rebound resolution that must not be dialed; and a public-path `web_fetch` call through the built stack that reads a slow body (std/tool/http/scenarios_test.go runs `web_fetch` through `gohan.Build`). Use a short `-timeout 2m` for the unit module and `-timeout 10m` for the public-path one.

### D6 — Lack of pooling stays a design non-goal

No pooling, no `MaxIdleConnsPerHost` change, and no performance claim ship in this change. The `Egress connection ownership` requirement carries the per-request ownership promise and states the gate: a reuse change needs a recorded measurement that the checked-address dial, proxy validation, and redirect rules still hold. A later change may take that on. This one only makes release correct, and its acceptance checks that no shared pool, no DNS cache and no pooled resolver appear.

### D7 — Main-spec, registry, and ADR edits land at apply time

This delta adds the requirements above to `openspec/specs/tools/spec.md` and registers the proposed scenario IDs in the scenario registry. Sealing the Contract wording needs one ADR; no ADR number is reserved during authoring. `docs/design/types.md` has no exported-surface change to absorb, so the types task only confirms the index is unchanged (`task spec:types`). No main-spec edit lands before apply.

## Risks / Trade-offs

- [Risk] A caller that does not close the body keeps the transport until the deadline. → Mitigated by D1's self-expiring timer and by the shipped tools, which close the body (std/tool/http/fetch.go:31).
- [Risk] Releasing the transport could mask a connection that a future pooled design expects to keep. → Accepted: D6 requires the measurement before reuse, and release is explicit.
- [Risk] Moving `cancel` out of `defer` can miss an early return and leak the timer. → Mitigated by D3's single release closure on every early-return path and by the early-error test.

## Migration Plan

No data migration and no API change. At apply time, land the main-spec and registry edits under the new ADR, run the `std/egress` and `std/tool/http` test modules, and run `task spec:types` to confirm the types index is unchanged. Rollback is the reverse patch of `std/egress/client.go`.
