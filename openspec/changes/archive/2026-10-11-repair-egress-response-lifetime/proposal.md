# Proposal: repair-egress-response-lifetime

## Why

`std/egress` binds its time limit to the request only, not to the response. `transport.RoundTrip` creates a timeout context and cancels it with `defer cancel()`, so the limit ends when `RoundTrip` returns and before the caller reads the body (std/egress/client.go:84-88). The same function builds a fresh `http.Transport` per request and never closes it, so every response can leave idle connections and their goroutines behind (std/egress/client.go:94,116,127-131). The `tools` Egress requirement promises scheme, size and time enforcement across the whole exchange (openspec/specs/tools/spec.md:157-159). The public `web_fetch` tool reads the body after `client.Do` returns (std/tool/http/fetch.go:26-32), so the broken bound is on the shipped path.

## What Changes

- Keep the policy time limit in force until the response body is closed or exhausted, instead of cancelling it when `RoundTrip` returns.
- Release the per-request transport and its idle connections exactly once, on end of file, on close, on a size-limit refusal, and on an early error.
- Keep the checked-address dial, the proxy hop validation, and the redirect rules unchanged.
- Add tests for a slow body after headers, an early close, a deadline, an error path, connection release, a repeated close, and the rebinding regression.
- Keep the lack of transport pooling a design non-goal. Do not add a retry policy, a shared connection pool, or a DNS cache. Reuse stays out until a measurement justifies it.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `tools`: extend the Egress requirement's promise with body-lifetime bounding and per-request transport release in `openspec/specs/tools/spec.md:157-189`.

## Impact

The change affects `std/egress/client.go` and its tests only; `std/tool/http/fetch.go` keeps its signature and its detached behavior, and `core` is untouched. No exported API changes, so `docs/design/types.md` needs no regeneration beyond a confirmation run. No third-party dependency is added. Output sanitization, sandbox adapter conformance, and foreign-runtime governance stay with their own owners.
